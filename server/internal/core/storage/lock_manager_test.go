package storage

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestLockManager_AcquireShare(t *testing.T) {
	lm := NewLockManager()
	lm.Acquire("row1", 1, LockModeShare)

	ch := make(chan struct{})
	go func() {
		lm.Acquire("row1", 2, LockModeShare)
		close(ch)
	}()

	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for share lock")
	}
}

func TestLockManager_AcquireUpdate(t *testing.T) {
	lm := NewLockManager()
	lm.Acquire("row1", 1, LockModeUpdate)

	var blocked atomic.Bool
	blocked.Store(true)
	ch := make(chan struct{})
	go func() {
		lm.Acquire("row1", 2, LockModeUpdate)
		blocked.Store(false)
		close(ch)
	}()

	time.Sleep(50 * time.Millisecond)
	if !blocked.Load() {
		t.Fatal("Tx 2 should be blocked")
	}

	lm.ReleaseAll(1)

	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for update lock after release")
	}
}
