package evs

import (
	"errors"
	"os"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/huaweicloud/huaweicloud-csi-driver/pkg/utils/mounts"
)

// fakeMount: only what the node paths under test touch (anything else panics on the nil IMount).
type fakeMount struct {
	mounts.IMount
	devices   map[string]string // volume ID → device path
	lookups   []string
	unmounted []string
}

func (m *fakeMount) GetDevicePath(id string) (string, error) {
	m.lookups = append(m.lookups, id)
	if p, ok := m.devices[id]; ok {
		return p, nil
	}
	return "", errors.New("no such device")
}

func (m *fakeMount) UnmountPath(p string) error {
	m.unmounted = append(m.unmounted, p)
	return nil
}

type fakeMetadata struct{}

func (fakeMetadata) GetInstanceID() (string, error)       { return "node-a", nil }
func (fakeMetadata) GetAvailabilityZone() (string, error) { return "ap-southeast-4a", nil }

// keylessNode: a node plugin started without a cloud config.
func keylessNode(m *fakeMount) *EvsDriver {
	d := NewDriver(nil, "", "", "")
	d.SetupDriver(m, fakeMetadata{})
	return d
}

const volID = "0f1e2d3c-4b5a-4968-8776-655443322110"

func TestKeyless_NoControllerNoAPI(t *testing.T) {
	d := keylessNode(&fakeMount{})
	if d.api != nil || d.cs != nil || d.controller() != nil {
		t.Fatalf("keyless driver has api %v cs %v controller %v", d.api, d.cs, d.controller())
	}
}

// Over gRPC: without credentials the controller service is not registered → Unimplemented.
func TestKeyless_ControllerUnimplemented(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "evs") // ParseEndpoint lowercases, keep the path lower case
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := dir + "/csi.sock"

	d := keylessNode(&fakeMount{})
	s := NewNonBlockingGRPCServer()
	s.Start("unix://"+sock, d.ids, d.controller(), d.ns)
	defer s.ForceStop()

	conn, err := grpc.Dial("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = csi.NewControllerClient(conn).CreateVolume(ctx, createReq("pvc-1", 0, 0, nil))
	wantCode(t, err, codes.Unimplemented)
	info, err := csi.NewNodeClient(conn).NodeGetInfo(ctx, &csi.NodeGetInfoRequest{})
	if err != nil || info.NodeId != "node-a" || info.AccessibleTopology.Segments[topologyKey] != "ap-southeast-4a" {
		t.Fatalf("NodeGetInfo = %+v, %v", info, err)
	}
}

func TestKeyless_DevicePath(t *testing.T) {
	m := &fakeMount{devices: map[string]string{volID: "/dev/disk/by-id/virtio-0f1e2d3c-4b5a-4968-8"}}
	p, err := getDevicePath(nil, volID, m)
	if err != nil || p != m.devices[volID] {
		t.Fatalf("path %q err %v", p, err)
	}
	if _, err := getDevicePath(nil, "absent-volume", m); err == nil {
		t.Fatal("an absent device must be an error")
	}
}

// Block mode stages without mounting, and without the cloud API.
func TestKeyless_StageBlock(t *testing.T) {
	m := &fakeMount{devices: map[string]string{volID: "/dev/vdb"}}
	d := keylessNode(m)
	_, err := d.ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
		VolumeId: volID, StagingTargetPath: "/stage",
		VolumeCapability: &csi.VolumeCapability{AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{
		VolumeId: "absent", StagingTargetPath: "/stage",
		VolumeCapability: &csi.VolumeCapability{AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}}}})
	wantCode(t, err, codes.Internal)
}

func TestKeyless_EphemeralRefused(t *testing.T) {
	d := keylessNode(&fakeMount{})
	_, err := d.ns.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
		VolumeId: volID, StagingTargetPath: "/stage", TargetPath: "/target", VolumeCapability: rwo(),
		VolumeContext: map[string]string{"csi.storage.k8s.io/ephemeral": "true"}})
	wantCode(t, err, codes.InvalidArgument)
}

func TestKeyless_Unpublish(t *testing.T) {
	m := &fakeMount{}
	d := keylessNode(m)
	if _, err := d.ns.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{VolumeId: volID, TargetPath: "/target"}); err != nil {
		t.Fatal(err)
	}
	if len(m.unmounted) != 1 || m.unmounted[0] != "/target" {
		t.Fatalf("unmounted %v", m.unmounted)
	}
}

// With credentials and an empty WWN, the device lookup never probes an empty serial.
func TestDevicePath_EmptyWWN(t *testing.T) {
	f := newFakeAPI()
	id := f.add(cloudvolumesVolume(), "")
	m := &fakeMount{devices: map[string]string{id: "/dev/vdb"}}
	p, err := getDevicePath(f, id, m)
	if err != nil || p != "/dev/vdb" {
		t.Fatalf("path %q err %v", p, err)
	}
	for _, l := range m.lookups {
		if l == "" {
			t.Fatal("probed an empty WWN")
		}
	}
}
