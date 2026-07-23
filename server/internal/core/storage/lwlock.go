package storage

import (
	"runtime"
	"sync/atomic"
)

// LWLock (LightWeight Lock) is a spinlock implementation optimized for very short critical sections.
// It uses exponential backoff and runtime.Gosched() to prevent CPU starvation.
type LWLock struct {
	state uint32
}

const (
	lwLockFree   = 0
	lwLockLocked = 1
)

func NewLWLock() *LWLock {
	return &LWLock{state: lwLockFree}
}

func (l *LWLock) Lock() {
	spins := 0
	for {
		if atomic.CompareAndSwapUint32(&l.state, lwLockFree, lwLockLocked) {
			return
		}
		
		spins++
		if spins < 10 {
			// Spin briefly
			for i := 0; i < spins*10; i++ {
				// Empty loop to burn a few CPU cycles
			}
		} else {
			// Yield to other goroutines if contention is high
			runtime.Gosched()
		}
	}
}

func (l *LWLock) Unlock() {
	atomic.StoreUint32(&l.state, lwLockFree)
}

// LWRLock is a lightweight Read-Write lock.
type LWRLock struct {
	state int32
}

const (
	lwRLockWrite = -1
	lwRLockFree  = 0
)

func NewLWRLock() *LWRLock {
	return &LWRLock{state: lwRLockFree}
}

func (l *LWRLock) RLock() {
	spins := 0
	for {
		s := atomic.LoadInt32(&l.state)
		if s >= 0 {
			if atomic.CompareAndSwapInt32(&l.state, s, s+1) {
				return
			}
		}
		
		spins++
		if spins > 10 {
			runtime.Gosched()
		}
	}
}

func (l *LWRLock) RUnlock() {
	for {
		s := atomic.LoadInt32(&l.state)
		if s > 0 {
			if atomic.CompareAndSwapInt32(&l.state, s, s-1) {
				return
			}
		} else {
			panic("RUnlock on unlocked or write-locked LWRLock")
		}
	}
}

func (l *LWRLock) Lock() {
	spins := 0
	for {
		if atomic.CompareAndSwapInt32(&l.state, lwRLockFree, lwRLockWrite) {
			return
		}
		
		spins++
		if spins > 10 {
			runtime.Gosched()
		}
	}
}

func (l *LWRLock) Unlock() {
	if !atomic.CompareAndSwapInt32(&l.state, lwRLockWrite, lwRLockFree) {
		panic("Unlock on unlocked or read-locked LWRLock")
	}
}
