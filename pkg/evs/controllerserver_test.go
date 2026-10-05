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

func statusAfterFirstGet(s string) func(v *cloudvolumes.Volume) {
	n := 0
	return func(v *cloudvolumes.Volume) {
		if n++; n > 1 {
			v.Status = s
		}
	}
}
