package server

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestIDLocker(t *testing.T) {
	locks := newIDLocker()

	release, err := locks.acquire("pvc-1")
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	_, err = locks.acquire("pvc-1")
	expectCode(t, err, codes.Aborted)

	if _, err := locks.acquire("pvc-2"); err != nil {
		t.Fatalf("acquire of another id failed: %v", err)
	}

	release()

	if _, err := locks.acquire("pvc-1"); err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}

	// A second release must not drop the lock that was acquired in between.
	release()
	_, err = locks.acquire("pvc-1")
	expectCode(t, err, codes.Aborted)
}

func TestRestoreTrackerReusesRunningJob(t *testing.T) {
	tracker := newRestoreTracker()
	unblock := make(chan struct{})
	calls := 0

	copyFn := func(context.Context) error {
		calls++
		<-unblock
		return nil
	}

	first := tracker.start("pvc-restore", "node-1#csi-lvm#snap-abc", copyFn)
	second := tracker.start("pvc-restore", "node-1#csi-lvm#snap-abc", copyFn)
	if first != second {
		t.Fatal("expected a retry to reuse the running job")
	}

	if !tracker.isTargetInUse("pvc-restore") {
		t.Error("expected target to be in use while copying")
	}
	if !tracker.isSourceInUse("node-1#csi-lvm#snap-abc") {
		t.Error("expected snapshot to be in use while copying")
	}
	if tracker.isSourceInUse("node-1#csi-lvm#snap-other") {
		t.Error("expected other snapshot not to be in use")
	}

	close(unblock)
	<-first.done

	if calls != 1 {
		t.Errorf("copy ran %d times, want 1", calls)
	}
	if tracker.isTargetInUse("pvc-restore") || tracker.isSourceInUse("node-1#csi-lvm#snap-abc") {
		t.Error("expected finished job not to be in use")
	}
}

func TestRestoreTrackerRestartsAfterFailure(t *testing.T) {
	tracker := newRestoreTracker()
	copyErr := errors.New("disk on fire")

	failed := tracker.start("pvc-restore", "snap", func(context.Context) error { return copyErr })
	<-failed.done
	if !errors.Is(failed.err, copyErr) {
		t.Fatalf("job error = %v, want %v", failed.err, copyErr)
	}

	tracker.forget("pvc-restore", failed)

	retried := tracker.start("pvc-restore", "snap", func(context.Context) error { return nil })
	<-retried.done
	if retried == failed || retried.err != nil {
		t.Fatalf("expected a fresh successful job, got %+v", retried)
	}

	// Forgetting a stale job must not drop the current one.
	current := tracker.start("pvc-other", "snap", func(context.Context) error { return nil })
	tracker.forget("pvc-other", failed)
	if tracker.jobs["pvc-other"] != current {
		t.Error("forget of a stale job removed the current job")
	}
}
