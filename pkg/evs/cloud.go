package evs

import (
	"github.com/chnsz/golangsdk/openstack/evs/v2/cloudvolumes"

	"github.com/huaweicloud/huaweicloud-csi-driver/pkg/config"
	"github.com/huaweicloud/huaweicloud-csi-driver/pkg/evs/services"
)

// evsAPI is the slice of the EVS/ECS API the controller and node use — a fake in tests.
type evsAPI interface {
	GetVolume(id string) (*cloudvolumes.Volume, error)
	ListVolumes(opts cloudvolumes.ListOpts) ([]cloudvolumes.Volume, error)
	CreateVolume(opts *cloudvolumes.CreateOpts) (string, error)
	DeleteVolume(id string) error
	ExpandVolume(id string, newSizeGB int) error
	AttachVolume(serverID, volumeID string) error
	WaitForVolumeAttaching(volumeID string) error
	DetachVolume(serverID, volumeID string) error
	GetServer(serverID string) error
}

// cloudAPI forwards to the services package with the driver's credentials.
type cloudAPI struct {
	cc *config.CloudCredentials
}

func (c cloudAPI) GetVolume(id string) (*cloudvolumes.Volume, error) {
	return services.GetVolume(c.cc, id)
}

func (c cloudAPI) ListVolumes(opts cloudvolumes.ListOpts) ([]cloudvolumes.Volume, error) {
	return services.ListVolumes(c.cc, opts)
}

func (c cloudAPI) CreateVolume(opts *cloudvolumes.CreateOpts) (string, error) {
	return services.CreateVolumeCompleted(c.cc, opts)
}

func (c cloudAPI) DeleteVolume(id string) error {
	return services.DeleteVolume(c.cc, id)
}

func (c cloudAPI) ExpandVolume(id string, newSizeGB int) error {
	return services.ExpandVolume(c.cc, id, newSizeGB)
}

func (c cloudAPI) AttachVolume(serverID, volumeID string) error {
	return services.AttachVolumeCompleted(c.cc, serverID, volumeID)
}

func (c cloudAPI) WaitForVolumeAttaching(volumeID string) error {
	return services.WaitForVolumeAttaching(c.cc, volumeID)
}

func (c cloudAPI) DetachVolume(serverID, volumeID string) error {
	return services.DetachVolumeCompleted(c.cc, serverID, volumeID)
}

func (c cloudAPI) GetServer(serverID string) error {
	_, err := services.GetServer(c.cc, serverID)
	return err
}
