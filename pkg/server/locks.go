package server

import (
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// idLocker serializes operations per ID and fails fast on contention.
type idLocker struct {
	mu    sync.Mutex
	locks map[string]struct{}
}

func newIDLocker() *idLocker {
	return &idLocker{locks: map[string]struct{}{}}
}

// tryAcquire locks the ID and reports false if it is already locked.
func (l *idLocker) tryAcquire(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.locks[id]; ok {
		return false
	}
	l.locks[id] = struct{}{}

	return true
}

func (l *idLocker) release(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.locks, id)
}

// acquire locks the ID or returns an Aborted status so the sidecar retries; release is safe to call twice.
func (l *idLocker) acquire(id string) (release func(), err error) {
	if !l.tryAcquire(id) {
		return nil, status.Errorf(codes.Aborted, "an operation on %s is already in progress", id)
	}
	return sync.OnceFunc(func() { l.release(id) }), nil
}
