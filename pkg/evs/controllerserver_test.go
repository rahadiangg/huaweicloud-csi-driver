package evs

import (
	"testing"

	"github.com/chnsz/golangsdk/openstack/evs/v2/cloudvolumes"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ctx = context.Background()

func rwo() *csi.VolumeCapability {
	return &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("code = %v, want %v (err: %v)", got, want, err)
	}
}

// The seam: publish attaches through the API and hands back the device path.
func TestControllerPublish_Attaches(t *testing.T) {
	f := newFakeAPI("node-a")
	id := f.add(cloudvolumes.Volume{Status: "available", Size: 10}, "")
	d := newFakeDriver(f)

	resp, err := d.cs.ControllerPublishVolume(ctx, &csi.ControllerPublishVolumeRequest{
		VolumeId: id, NodeId: "node-a", VolumeCapability: rwo()})
	if err != nil {
		t.Fatal(err)
	}
	if resp.PublishContext["DevicePath"] != "/dev/vdb" || f.called("AttachVolume") != 1 {
		t.Fatalf("publish: %+v, attach calls %d", resp, f.called("AttachVolume"))
	}
}

// Unpublish: success only when the volume no longer hangs on the server, or the server is gone
// (upstream #175) — never while attached to a server that still exists.
func TestControllerUnpublish(t *testing.T) {
	inUse := func(servers ...string) cloudvolumes.Volume {
		v := cloudvolumes.Volume{Status: "in-use", Size: 10}
		for _, s := range servers {
			v.Attachments = append(v.Attachments, cloudvolumes.Attachment{ServerID: s})
		}
		return v
	}
	errDetach := status.Error(codes.Internal, "Ecs.0111 or anything else")
	for _, tc := range []struct {
		name      string
		vol       *cloudvolumes.Volume // nil = the volume does not exist
		servers   []string             // servers that still exist
		detachErr error
		onGet     func(v *cloudvolumes.Volume)
		want      codes.Code
		detaches  int
		attached  bool // node-a still attached afterwards
	}{
		{name: "volume gone", want: codes.OK},
		{name: "available, nothing attached", vol: &cloudvolumes.Volume{Status: "available"}, servers: []string{"node-a"}, want: codes.OK},
		{name: "attached elsewhere only", vol: ptr(inUse("node-b")), servers: []string{"node-a", "node-b"}, want: codes.OK},
		{name: "attaching, not listed yet", vol: &cloudvolumes.Volume{Status: "attaching"}, servers: []string{"node-a"}, want: codes.Aborted},
		{name: "attaching to this node", vol: &cloudvolumes.Volume{Status: "attaching", Attachments: []cloudvolumes.Attachment{{ServerID: "node-a"}}},
			servers: []string{"node-a"}, want: codes.Aborted, attached: true},
		{name: "in-use here, server alive: detach", vol: ptr(inUse("node-a")), servers: []string{"node-a"}, want: codes.OK, detaches: 1},
		{name: "in-use here, server deleted (#175)", vol: ptr(inUse("node-a")), detachErr: status.Error(codes.NotFound, "server gone"),
			want: codes.OK, detaches: 1, attached: true},
		{name: "detach fails, server alive, still attached", vol: ptr(inUse("node-a")), servers: []string{"node-a"}, detachErr: errDetach,
			want: codes.Internal, detaches: 1, attached: true},
		{name: "detach errors but the volume is free", vol: ptr(inUse("node-a")), servers: []string{"node-a"}, detachErr: errDetach,
			onGet: freeAfterFirstGet(), want: codes.OK, detaches: 1},
		{name: "detach job unreadable, volume detaching then free", vol: ptr(inUse("node-a")), servers: []string{"node-a"}, detachErr: errDetach,
			onGet: detachingThenFree(), want: codes.OK, detaches: 1},
		{name: "detaching here, finishes", vol: &cloudvolumes.Volume{Status: "detaching", Attachments: []cloudvolumes.Attachment{{ServerID: "node-a"}}},
			servers: []string{"node-a"}, onGet: freeAfterFirstGet(), want: codes.OK},
		{name: "detaching here, bounces back to in-use", vol: &cloudvolumes.Volume{Status: "detaching", Attachments: []cloudvolumes.Attachment{{ServerID: "node-a"}}},
			servers: []string{"node-a"}, onGet: statusAfterFirstGet("in-use"), want: codes.Aborted, attached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(tc.servers...)
			id := "00000000-0000-4000-8000-00000000dead"
			if tc.vol != nil {
				id = f.add(*tc.vol, "")
			}
			f.detachErr, f.onGet = tc.detachErr, tc.onGet
			d := newFakeDriver(f)

			_, err := d.cs.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{VolumeId: id, NodeId: "node-a"})
			wantCode(t, err, tc.want)
			if got := f.called("DetachVolume"); got != tc.detaches {
				t.Fatalf("detach calls = %d, want %d", got, tc.detaches)
			}
			if tc.vol != nil {
				if got := attachedTo(f.volumes[id], "node-a"); got != tc.attached {
					t.Fatalf("still attached = %v, want %v", got, tc.attached)
				}
			}
		})
	}
}

func TestControllerUnpublish_EmptyIDs(t *testing.T) {
	d := newFakeDriver(newFakeAPI())
	_, err := d.cs.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{NodeId: "node-a"})
	wantCode(t, err, codes.InvalidArgument)
	_, err = d.cs.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{VolumeId: "v"})
	wantCode(t, err, codes.InvalidArgument)
}

func ptr(v cloudvolumes.Volume) *cloudvolumes.Volume { return &v }

// freeAfterFirstGet: the first read sees the volume as stored, later reads see it detached.
func freeAfterFirstGet() func(v *cloudvolumes.Volume) {
	n := 0
	return func(v *cloudvolumes.Volume) {
		if n++; n > 1 {
			v.Status, v.Attachments = "available", nil
		}
	}
}

// detachingThenFree: in-use on the first read, detaching on the second (the
// detach was accepted), free from the third on — what EVS showed live when
// the detach job ID came back empty.
func detachingThenFree() func(v *cloudvolumes.Volume) {
	n := 0
	return func(v *cloudvolumes.Volume) {
		switch n++; {
		case n == 2:
			v.Status = "detaching"
		case n > 2:
			v.Status, v.Attachments = "available", nil
		}
	}
}

func statusAfterFirstGet(s string) func(v *cloudvolumes.Volume) {
	n := 0
	return func(v *cloudvolumes.Volume) {
		if n++; n > 1 {
			v.Status = s
		}
	}
}

// Publish: a volume busy on another node is a FailedPrecondition (the attacher retries), not Internal.
func TestControllerPublish(t *testing.T) {
	at := func(status, server string) *cloudvolumes.Volume {
		v := &cloudvolumes.Volume{Status: status, Size: 10}
		if server != "" {
			v.Attachments = []cloudvolumes.Attachment{{ServerID: server, Device: "/dev/vdb"}}
		}
		return v
	}
	for _, tc := range []struct {
		name    string
		vol     *cloudvolumes.Volume
		node    string
		want    codes.Code
		attachs int
	}{
		{name: "available: attach", vol: at("available", ""), node: "node-a", want: codes.OK, attachs: 1},
		{name: "already in-use here: idempotent", vol: at("in-use", "node-a"), node: "node-a", want: codes.OK},
		{name: "attaching here: wait", vol: at("attaching", "node-a"), node: "node-a", want: codes.OK},
		{name: "in-use elsewhere", vol: at("in-use", "node-b"), node: "node-a", want: codes.FailedPrecondition},
		{name: "attaching elsewhere", vol: at("attaching", "node-b"), node: "node-a", want: codes.FailedPrecondition},
		{name: "detaching elsewhere", vol: at("detaching", "node-b"), node: "node-a", want: codes.FailedPrecondition},
		{name: "detaching here", vol: at("detaching", "node-a"), node: "node-a", want: codes.FailedPrecondition},
		{name: "volume in error", vol: at("error", ""), node: "node-a", want: codes.Internal},
		{name: "volume gone", node: "node-a", want: codes.NotFound},
		{name: "server gone", vol: at("available", ""), node: "node-x", want: codes.NotFound},
		{name: "empty node id", vol: at("available", ""), node: "", want: codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI("node-a", "node-b")
			id := "00000000-0000-4000-8000-00000000dead"
			if tc.vol != nil {
				id = f.add(*tc.vol, "")
			}
			d := newFakeDriver(f)
			_, err := d.cs.ControllerPublishVolume(ctx, &csi.ControllerPublishVolumeRequest{
				VolumeId: id, NodeId: tc.node, VolumeCapability: rwo()})
			wantCode(t, err, tc.want)
			if got := f.called("AttachVolume"); got != tc.attachs {
				t.Fatalf("attach calls = %d, want %d", got, tc.attachs)
			}
		})
	}
}

const gi = int64(1) << 30

func createReq(name string, required, limit int64, params map[string]string, caps ...*csi.VolumeCapability) *csi.CreateVolumeRequest {
	if len(caps) == 0 {
		caps = []*csi.VolumeCapability{rwo()}
	}
	r := &csi.CreateVolumeRequest{Name: name, VolumeCapabilities: caps, Parameters: params}
	if required > 0 || limit > 0 {
		r.CapacityRange = &csi.CapacityRange{RequiredBytes: required, LimitBytes: limit}
	}
	return r
}

func TestCreateVolume_Sizes(t *testing.T) {
	for _, tc := range []struct {
		name            string
		required, limit int64
		wantGB          int
		want            codes.Code
	}{
		{name: "no range: default 10", wantGB: 10},
		{name: "1Gi rounds up to the EVS minimum", required: gi, wantGB: 10},
		{name: "10.5Gi rounds up", required: 10*gi + gi/2, wantGB: 11},
		{name: "32TiB is the maximum", required: 32768 * gi, wantGB: 32768},
		{name: "above 32TiB", required: 32769 * gi, want: codes.OutOfRange},
		{name: "limit below the rounded size", required: gi, limit: 5 * gi, want: codes.OutOfRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI()
			d := newFakeDriver(f)
			resp, err := d.cs.CreateVolume(ctx, createReq("pvc-1", tc.required, tc.limit, nil))
			wantCode(t, err, tc.want)
			if tc.want != codes.OK {
				if f.called("CreateVolume") != 0 {
					t.Fatal("refused request still created a disk")
				}
				return
			}
			if f.lastCreate.Volume.Size != tc.wantGB || resp.Volume.CapacityBytes != int64(tc.wantGB)*gi {
				t.Fatalf("size %d GiB, capacity %d; want %d GiB", f.lastCreate.Volume.Size, resp.Volume.CapacityBytes, tc.wantGB)
			}
		})
	}
}

func TestCreateVolume_Validation(t *testing.T) {
	rwx := &csi.VolumeCapability{AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER}}
	noMode := &csi.VolumeCapability{}
	for name, req := range map[string]*csi.CreateVolumeRequest{
		"ReadWriteMany":       createReq("pvc-1", 0, 0, nil, rwx),
		"missing access mode": createReq("pvc-1", 0, 0, nil, noMode),
		"tag without =":       createReq("pvc-1", 0, 0, map[string]string{"tags": "a"}),
		"tag with empty key":  createReq("pvc-1", 0, 0, map[string]string{"tags": "a=b,=c"}),
		"empty name":          createReq("", 0, 0, nil),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeAPI()
			_, err := newFakeDriver(f).cs.CreateVolume(ctx, req)
			wantCode(t, err, codes.InvalidArgument)
			if f.called("CreateVolume") != 0 {
				t.Fatal("invalid request still created a disk")
			}
		})
	}
}

// Tags and the enterprise project reach EVS; the lookup searches the same project.
func TestCreateVolume_TagsAndEnterpriseProject(t *testing.T) {
	f := newFakeAPI()
	d := newFakeDriver(f)
	params := map[string]string{"type": "GPSSD", "enterpriseProjectId": "ep-dodai", "tags": " dodai-plane = p1 ,team=db"}
	if _, err := d.cs.CreateVolume(ctx, createReq("pvc-1", 20*gi, 0, params)); err != nil {
		t.Fatal(err)
	}
	o := f.lastCreate.Volume
	if o.EnterpriseProjectID != "ep-dodai" || o.Tags["dodai-plane"] != "p1" || o.Tags["team"] != "db" || len(o.Tags) != 2 {
		t.Fatalf("create opts: ep %q tags %v", o.EnterpriseProjectID, o.Tags)
	}
	// a retry finds the disk in that project instead of creating a second one
	if _, err := d.cs.CreateVolume(ctx, createReq("pvc-1", 20*gi, 0, params)); err != nil {
		t.Fatal(err)
	}
	if n := f.called("CreateVolume"); n != 1 {
		t.Fatalf("retry created a duplicate: %d creates", n)
	}
}

// An earlier attempt's disk with the same name, by state.
func TestCreateVolume_Existing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing cloudvolumes.Volume
		onGet    func(v *cloudvolumes.Volume)
		want     codes.Code
		creates  int
		deletes  int
	}{
		{name: "available: reused", existing: cloudvolumes.Volume{Name: "pvc-1", Size: 10, Status: "available"}},
		{name: "creating: waits for it", existing: cloudvolumes.Volume{Name: "pvc-1", Size: 10, Status: "creating"},
			onGet: statusAfterFirstGet("available")},
		{name: "creating then error: deleted, retry", existing: cloudvolumes.Volume{Name: "pvc-1", Size: 10, Status: "creating"},
			onGet: statusAfterFirstGet("error"), want: codes.Aborted, deletes: 1},
		{name: "error: deleted, retry", existing: cloudvolumes.Volume{Name: "pvc-1", Size: 10, Status: "error"},
			want: codes.Aborted, deletes: 1},
		{name: "deleting: retry", existing: cloudvolumes.Volume{Name: "pvc-1", Size: 10, Status: "deleting"}, want: codes.Aborted},
		{name: "different size", existing: cloudvolumes.Volume{Name: "pvc-1", Size: 20, Status: "available"}, want: codes.AlreadyExists},
		{name: "fuzzy match only: creates", existing: cloudvolumes.Volume{Name: "pvc-1-other", Size: 10, Status: "available"}, creates: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI()
			f.add(tc.existing, "")
			f.onGet = tc.onGet
			_, err := newFakeDriver(f).cs.CreateVolume(ctx, createReq("pvc-1", 0, 0, nil))
			wantCode(t, err, tc.want)
			if c, d := f.called("CreateVolume"), f.called("DeleteVolume"); c != tc.creates || d != tc.deletes {
				t.Fatalf("creates %d deletes %d, want %d %d", c, d, tc.creates, tc.deletes)
			}
		})
	}
}

func TestDeleteVolume(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vol     *cloudvolumes.Volume
		want    codes.Code
		deletes int
	}{
		{name: "gone", want: codes.OK},
		{name: "available", vol: &cloudvolumes.Volume{Status: "available"}, deletes: 1},
		{name: "already deleting", vol: &cloudvolumes.Volume{Status: "deleting"}},
		{name: "in-use", vol: &cloudvolumes.Volume{Status: "in-use"}, want: codes.FailedPrecondition},
		{name: "attaching", vol: &cloudvolumes.Volume{Status: "attaching"}, want: codes.FailedPrecondition},
		{name: "detaching", vol: &cloudvolumes.Volume{Status: "detaching"}, want: codes.FailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI()
			id := "00000000-0000-4000-8000-00000000dead"
			if tc.vol != nil {
				id = f.add(*tc.vol, "")
			}
			_, err := newFakeDriver(f).cs.DeleteVolume(ctx, &csi.DeleteVolumeRequest{VolumeId: id})
			wantCode(t, err, tc.want)
			if got := f.called("DeleteVolume"); got != tc.deletes {
				t.Fatalf("delete calls = %d, want %d", got, tc.deletes)
			}
		})
	}
}

func TestControllerExpand(t *testing.T) {
	for _, tc := range []struct {
		name            string
		size            int
		required, limit int64
		want            codes.Code
		wantBytes       int64
		expands         int
	}{
		{name: "grows, returns the rounded size", size: 10, required: 20*gi + 1, wantBytes: 21 * gi, expands: 1},
		{name: "already larger: no API call", size: 30, required: 20 * gi, wantBytes: 30 * gi},
		{name: "above 32TiB", size: 10, required: 33000 * gi, want: codes.OutOfRange},
		{name: "limit below the rounded size", size: 10, required: 20*gi + 1, limit: 20*gi + 2, want: codes.OutOfRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI()
			id := f.add(cloudvolumes.Volume{Status: "in-use", Size: tc.size}, "")
			resp, err := newFakeDriver(f).cs.ControllerExpandVolume(ctx, &csi.ControllerExpandVolumeRequest{
				VolumeId: id, CapacityRange: &csi.CapacityRange{RequiredBytes: tc.required, LimitBytes: tc.limit}})
			wantCode(t, err, tc.want)
			if got := f.called("ExpandVolume"); got != tc.expands {
				t.Fatalf("expand calls = %d, want %d", got, tc.expands)
			}
			if err == nil && (resp.CapacityBytes != tc.wantBytes || !resp.NodeExpansionRequired) {
				t.Fatalf("resp = %+v, want %d bytes", resp, tc.wantBytes)
			}
		})
	}
}

func TestControllerExpand_Gone(t *testing.T) {
	_, err := newFakeDriver(newFakeAPI()).cs.ControllerExpandVolume(ctx, &csi.ControllerExpandVolumeRequest{
		VolumeId: "00000000-0000-4000-8000-00000000dead", CapacityRange: &csi.CapacityRange{RequiredBytes: 20 * gi}})
	wantCode(t, err, codes.NotFound)
}
