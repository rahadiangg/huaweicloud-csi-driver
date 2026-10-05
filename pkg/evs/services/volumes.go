package services

import (
	"fmt"

	"github.com/chnsz/golangsdk"
	"github.com/chnsz/golangsdk/openstack/evs/v1/jobs"
	"github.com/chnsz/golangsdk/openstack/evs/v2/cloudvolumes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	log "k8s.io/klog/v2"

	"github.com/huaweicloud/huaweicloud-csi-driver/pkg/common"
	"github.com/huaweicloud/huaweicloud-csi-driver/pkg/config"
)

const (
	EvsAvailableStatus = "available"
	EvsAttachingStatus = "attaching"
	EvsInUseStatus     = "in-use"
	EvsDetachingStatus = "detaching"
	EvsCreatingStatus  = "creating"
	EvsErrorStatus     = "error"
	EvsDeletingStatus  = "deleting"
)

func CreateVolumeCompleted(c *config.CloudCredentials, otps *cloudvolumes.CreateOpts) (string, error) {
	client, err := getEvsV21Client(c)
	if err != nil {
		return "", err
	}

	job, err := cloudvolumes.Create(client, *otps).Extract()
	if err != nil {
		return "", fmt.Errorf("error creating EVS volume, error: %s, createOpts: %#v", err, otps)
	}

	log.V(4).Infof("[DEBUG] The volume creation is submitted successfully and the job is running.")
	return waitForJobFinished(c, "creation", job.JobID)
}

func GetVolume(c *config.CloudCredentials, id string) (*cloudvolumes.Volume, error) {
	client, err := getEvsV2Client(c)
	if err != nil {
		return nil, err
	}

	volume, err := cloudvolumes.Get(client, id).Extract()
	if err != nil {
		if common.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "Error, volume %s does not exist", id)
		}
		return nil, status.Errorf(codes.Internal, "Error querying volume details: %s", err)
	}
	return volume, nil
}

func ExpandVolume(c *config.CloudCredentials, id string, newSize int) error {
	client, err := getEvsV21Client(c)
	if err != nil {
		return err
	}

	opt := cloudvolumes.ExtendOpts{
		SizeOpts: cloudvolumes.ExtendSizeOpts{
			NewSize: newSize,
		},
	}
	log.V(4).Infof("[DEBUG] Expand volume %s, and the options is %#v", id, opt)

	job, err := cloudvolumes.ExtendSize(client, id, opt).Extract()
	if err != nil {
		return status.Error(codes.Internal,
			fmt.Sprintf("Error expanding, volume: %s, newSize: %v, error: %s", id, newSize, err))
	}
	log.V(4).Infof("[DEBUG] The volume expanding is submitted successfully and the job is running.")

	_, err = waitForJobFinished(c, "expanding", job.JobID)
	return err
}

func DeleteVolume(c *config.CloudCredentials, id string) error {
	client, err := getEvsV2Client(c)
	if err != nil {
		return err
	}
	return cloudvolumes.Delete(client, id, nil).Err
}

func ListVolumes(c *config.CloudCredentials, opts cloudvolumes.ListOpts) ([]cloudvolumes.Volume, error) {
	client, err := getEvsV2Client(c)
	if err != nil {
		return nil, err
	}
	log.V(4).Infof("[DEBUG] Query a volume list, and the options is %#v", opts)

	volumes, err := cloudvolumes.ListPage(client, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Error querying volume list, error: %v", err)
	}
	return volumes, nil
}

func waitForJobFinished(c *config.CloudCredentials, title, jobID string) (string, error) {
	client, err := getJobV1Client(c)
	if err != nil {
		return "", err
	}

	var volumeID string
	err = common.WaitForCompleted(func() (bool, error) {
		job, err := jobs.GetJobDetails(client, jobID).ExtractJob()
		if err != nil {
			return false, status.Error(codes.Internal,
				fmt.Sprintf("Error waiting for the %s volume job to be complete, jobID: %s", title, jobID))
		}

		if job.Status == "SUCCESS" {
			volumeID = job.Entities.VolumeID
			return true, nil
		}

		if job.Status == "FAIL" {
			return false, status.Error(codes.Internal,
				fmt.Sprintf("Error waiting for the %s volume job to be complete, job: %#v", title, job))
		}

		return false, nil
	})

	return volumeID, err
}

func getEvsV2Client(c *config.CloudCredentials) (*golangsdk.ServiceClient, error) {
	client, err := c.EvsV2Client()
	if err != nil {
		logMsg := fmt.Sprintf("Failed create EVS V2 client: %s", err)
		return nil, status.Error(codes.Internal, logMsg)
	}
	return client, nil
}

func getEvsV21Client(c *config.CloudCredentials) (*golangsdk.ServiceClient, error) {
	client, err := c.EvsV21Client()
	if err != nil {
		logMsg := fmt.Sprintf("Failed create EVS V2.1 client: %s", err)
		return nil, status.Error(codes.Internal, logMsg)
	}
	return client, nil
}

func getJobV1Client(c *config.CloudCredentials) (*golangsdk.ServiceClient, error) {
	client, err := c.EvsV1Client()
	if err != nil {
		logMsg := fmt.Sprintf("Failed create JOB V1 client: %s", err)
		return nil, status.Error(codes.Internal, logMsg)
	}
	return client, nil
}
