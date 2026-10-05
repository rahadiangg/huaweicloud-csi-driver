package evs

import (
	"fmt"
	"strings"
	"sync"

	"github.com/chnsz/golangsdk"
	"github.com/chnsz/golangsdk/openstack/evs/v2/cloudvolumes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/util/wait"
)

// fakeAPI is an in-memory EVS/ECS: jobs complete synchronously.
type fakeAPI struct {
	mu      sync.Mutex
	n       int
	volumes map[string]*cloudvolumes.Volume
	eps     map[string]string // volume ID → enterprise project
	servers map[string]bool

	lastCreate *cloudvolumes.CreateOpts
	calls      []string

	detachErr error // DetachVolume returns this and changes nothing
	// onGet, if set, mutates a volume on every GetVolume (e.g. a job finishing).
	onGet func(v *cloudvolumes.Volume)
}

func newFakeAPI(servers ...string) *fakeAPI {
	f := &fakeAPI{volumes: map[string]*cloudvolumes.Volume{}, eps: map[string]string{}, servers: map[string]bool{}}
	for _, s := range servers {
		f.servers[s] = true
	}
	return f
}

func (f *fakeAPI) call(s string) { f.calls = append(f.calls, s) }

// add stores a volume and returns its ID (36 chars, like an EVS UUID).
func (f *fakeAPI) add(v cloudvolumes.Volume, ep string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if v.ID == "" {
		v.ID = fmt.Sprintf("%08d-0000-4000-8000-000000000000", f.n)
	}
	f.volumes[v.ID] = &v
	f.eps[v.ID] = ep
	return v.ID
}

func (f *fakeAPI) attach(id, server string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.volumes[id]
	v.Status = "in-use"
	v.Attachments = append(v.Attachments, cloudvolumes.Attachment{ServerID: server, Device: "/dev/vdb"})
}

func (f *fakeAPI) GetVolume(id string) (*cloudvolumes.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("GetVolume")
	v, ok := f.volumes[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "Error, volume %s does not exist", id)
	}
	if f.onGet != nil {
		f.onGet(v)
	}
	c := *v
	c.Attachments = append([]cloudvolumes.Attachment(nil), v.Attachments...)
	return &c, nil
}

// ListVolumes filters like EVS: name is a fuzzy (substring) match.
func (f *fakeAPI) ListVolumes(opts cloudvolumes.ListOpts) ([]cloudvolumes.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("ListVolumes")
	var out []cloudvolumes.Volume
	for id, v := range f.volumes {
		if opts.Name != "" && !strings.Contains(v.Name, opts.Name) {
			continue
		}
		if opts.EnterpriseProjectID != "" && f.eps[id] != opts.EnterpriseProjectID {
			continue
		}
		out = append(out, *v)
	}
	return out, nil
}

func (f *fakeAPI) CreateVolume(opts *cloudvolumes.CreateOpts) (string, error) {
	f.mu.Lock()
	f.call("CreateVolume")
	f.lastCreate = opts
	f.mu.Unlock()
	o := opts.Volume
	return f.add(cloudvolumes.Volume{Name: o.Name, Size: o.Size, Status: "available", AvailabilityZone: o.AvailabilityZone},
		o.EnterpriseProjectID), nil
}

func (f *fakeAPI) DeleteVolume(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("DeleteVolume")
	if _, ok := f.volumes[id]; !ok {
		return golangsdk.ErrDefault404{}
	}
	delete(f.volumes, id)
	return nil
}

func (f *fakeAPI) ExpandVolume(id string, newSizeGB int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("ExpandVolume")
	f.volumes[id].Size = newSizeGB
	return nil
}

func (f *fakeAPI) AttachVolume(serverID, volumeID string) error {
	f.mu.Lock()
	f.call("AttachVolume")
	known := f.servers[serverID]
	f.mu.Unlock()
	if !known {
		return status.Errorf(codes.NotFound, "Error, ECS instance %s does not exist", serverID)
	}
	f.attach(volumeID, serverID)
	return nil
}

func (f *fakeAPI) WaitForVolumeAttaching(volumeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("WaitForVolumeAttaching")
	f.volumes[volumeID].Status = "in-use"
	return nil
}

func (f *fakeAPI) DetachVolume(serverID, volumeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("DetachVolume")
	if f.detachErr != nil {
		return f.detachErr
	}
	v := f.volumes[volumeID]
	var keep []cloudvolumes.Attachment
	for _, a := range v.Attachments {
		if a.ServerID != serverID {
			keep = append(keep, a)
		}
	}
	v.Attachments = keep
	if len(keep) == 0 {
		v.Status = "available"
	}
	return nil
}

func (f *fakeAPI) GetServer(serverID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("GetServer")
	if !f.servers[serverID] {
		return status.Errorf(codes.NotFound, "Error, ECS instance %s does not exist", serverID)
	}
	return nil
}

func (f *fakeAPI) called(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

// newFakeDriver: a driver whose controller and node talk to f.
func newFakeDriver(f *fakeAPI) *EvsDriver {
	d := NewDriver(nil, "", "", "")
	d.api = f
	d.poll = fastPoll
	d.cs = &ControllerServer{Driver: d}
	return d
}

// fastPoll: the WaitForCompleted contract (condition first, timeout after a few tries) with no sleep.
func fastPoll(condition wait.ConditionFunc) error {
	for i := 0; i < 5; i++ {
		ok, err := condition()
		if err != nil || ok {
			return err
		}
	}
	return wait.ErrWaitTimeout
}

func cloudvolumesVolume() cloudvolumes.Volume { return cloudvolumes.Volume{Status: "in-use", Size: 10} }
