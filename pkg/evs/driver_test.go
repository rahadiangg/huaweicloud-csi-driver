package evs

import (
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestDriver_NoListCapabilities(t *testing.T) {
	d := newFakeDriver(newFakeAPI())
	for _, c := range d.cscap {
		switch c.GetRpc().GetType() {
		case csi.ControllerServiceCapability_RPC_LIST_VOLUMES, csi.ControllerServiceCapability_RPC_LIST_VOLUMES_PUBLISHED_NODES:
			t.Fatalf("advertises %v", c.GetRpc().GetType())
		}
	}
}
