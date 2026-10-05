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
