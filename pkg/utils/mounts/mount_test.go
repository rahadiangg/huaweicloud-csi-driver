package mounts

import "testing"

// Short IDs and an empty WWN used to panic on volumeID[:20].
func TestGetDevicePathBySerialID_ShortIDs(t *testing.T) {
	m := &Mount{}
	for _, id := range []string{"", "a", "0123456789abcdefghi", "0123456789abcdefghij", "0f1e2d3c-4b5a-4968-8776-655443322110"} {
		if p := m.getDevicePathBySerialID(id); id == "" && p != "" {
			t.Fatalf("empty ID resolved to %q", p)
		}
	}
}
