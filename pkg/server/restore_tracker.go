package server

import (
	"context"
	"sync"
)

// restoreJob is a block copy from a snapshot into a restored volume.
type restoreJob struct {
	snapshotID string
	done       chan struct{}
	err        error
}

func (j *restoreJob) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

// restoreTracker keeps restore copies running across CreateVolume retries of the provisioner.
type restoreTracker struct {
	mu   sync.Mutex
	jobs map[string]*restoreJob
}

func newRestoreTracker() *restoreTracker {
	return &restoreTracker{jobs: map[string]*restoreJob{}}
}

// start runs copyFn in the background for the volume unless a job for it already exists.
func (t *restoreTracker) start(volumeID, snapshotID string, copyFn func(context.Context) error) *restoreJob {
	t.mu.Lock()
	defer t.mu.Unlock()

	if job, ok := t.jobs[volumeID]; ok {
		return job
	}

	job := &restoreJob{snapshotID: snapshotID, done: make(chan struct{})}
	t.jobs[volumeID] = job

	go func() {
		// Detached from the request context so a provisioner timeout does not abort the copy.
		job.err = copyFn(context.Background())
		close(job.done)
	}()

	return job
}

// forget drops a finished job so a failed copy is restarted on the next retry.
func (t *restoreTracker) forget(volumeID string, job *restoreJob) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.jobs[volumeID] == job {
		delete(t.jobs, volumeID)
	}
}

// isTargetInUse reports whether a copy into the volume is still running.
func (t *restoreTracker) isTargetInUse(volumeID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	job, ok := t.jobs[volumeID]
	return ok && !job.finished()
}

// isSourceInUse reports whether a copy from the snapshot is still running.
func (t *restoreTracker) isSourceInUse(snapshotID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, job := range t.jobs {
		if job.snapshotID == snapshotID && !job.finished() {
			return true
		}
	}
	return false
}
