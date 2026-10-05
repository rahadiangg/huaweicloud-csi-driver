package evs

import (
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// volumeLocks allows one in-flight controller operation per volume: the driver ignores request
// contexts, so a sidecar's retry after its timeout could otherwise run alongside the first call.
type volumeLocks struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func (l *volumeLocks) tryAcquire(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, busy := l.ids[id]; busy {
		return false
	}
	if l.ids == nil {
		l.ids = map[string]struct{}{}
	}
	l.ids[id] = struct{}{}
	return true
}

func (l *volumeLocks) release(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.ids, id)
}

// lock takes the volume (or, for CreateVolume, the requested name); a concurrent call gets Aborted.
func (cs *ControllerServer) lock(id string) (func(), error) {
	if !cs.Driver.locks.tryAcquire(id) {
		return nil, status.Errorf(codes.Aborted, "An operation on volume %s is already in progress", id)
	}
	return func() { cs.Driver.locks.release(id) }, nil
}
