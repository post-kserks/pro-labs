package storage

import (
	"sync"
)

type MemoryPool struct {
	pool sync.Pool
}

func NewMemoryPool() *MemoryPool {
	return &MemoryPool{
		pool: sync.Pool{
			New: func() interface{} {
				b := make([]byte, 0)
				return &b
			},
		},
	}
}

func (m *MemoryPool) Get(size int) []byte {
	bPtr := m.pool.Get().(*[]byte)
	b := *bPtr
	if cap(b) < size {
		b = make([]byte, size)
	} else {
		b = b[:size]
	}
	return b
}

func (m *MemoryPool) Put(b []byte) {
	b = b[:0]
	m.pool.Put(&b)
}

type TupleHeader struct {
	Xmin    uint64
	Xmax    uint64
	DataLen uint32
}

type RawTuple struct {
	Header TupleHeader
	Data   []byte
}
