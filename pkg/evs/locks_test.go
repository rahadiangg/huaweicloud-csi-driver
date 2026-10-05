package evs

import (
	"sync"
	"testing"

	"github.com/chnsz/golangsdk/openstack/evs/v2/cloudvolumes"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A call for a volume with an operation in flight is Aborted and touches nothing.
func TestVolumeLock_Aborted(t *testing.T) {
	f := newFakeAPI("node-a")
	id := f.add(cloudvolumes.Volume{Status: "in-use", Size: 10, Attachments: []cloudvolumes.Attachment{{ServerID: "node-a"}}}, "")
	d := newFakeDriver(f)
	if !d.locks.tryAcquire(id) || !d.locks.tryAcquire("pvc-1") {
		t.Fatal("could not take the locks")
	}

	ops := map[string]func() error{
		"create": func() error { _, err := d.cs.CreateVolume(ctx, createReq("pvc-1", 0, 0, nil)); return err },
		"delete": func() error { _, err := d.cs.DeleteVolume(ctx, &csi.DeleteVolumeRequest{VolumeId: id}); return err },
		"publish": func() error {
			_, err := d.cs.ControllerPublishVolume(ctx, &csi.ControllerPublishVolumeRequest{VolumeId: id, NodeId: "node-a", VolumeCapability: rwo()})
			return err
		},
		"unpublish": func() error {
			_, err := d.cs.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{VolumeId: id, NodeId: "node-a"})
			return err
		},
		"expand": func() error {
			_, err := d.cs.ControllerExpandVolume(ctx, &csi.ControllerExpandVolumeRequest{VolumeId: id, CapacityRange: &csi.CapacityRange{RequiredBytes: 20 * gi}})
			return err
		},
	}
	for name, op := range ops {
		wantCode(t, op(), codes.Aborted)
		for _, c := range []string{"CreateVolume", "DeleteVolume", "AttachVolume", "DetachVolume", "ExpandVolume"} {
			if f.called(c) != 0 {
				t.Fatalf("%s while locked called %s", name, c)
			}
		}
	}

	d.locks.release(id)
	d.locks.release("pvc-1")
	if err := ops["unpublish"](); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

// Concurrent unpublishes of one volume: exactly one detach, the rest Aborted or a no-op — never two detaches.
func TestVolumeLock_Concurrent(t *testing.T) {
	f := newFakeAPI("node-a")
	id := f.add(cloudvolumes.Volume{Status: "in-use", Size: 10, Attachments: []cloudvolumes.Attachment{{ServerID: "node-a"}}}, "")
	d := newFakeDriver(f)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.cs.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{VolumeId: id, NodeId: "node-a"})
			if c := status.Code(err); c != codes.OK && c != codes.Aborted {
				t.Errorf("unexpected %v", err)
			}
		}()
	}
	wg.Wait()
	if n := f.called("DetachVolume"); n != 1 {
		t.Fatalf("detach calls = %d, want 1", n)
	}
}
