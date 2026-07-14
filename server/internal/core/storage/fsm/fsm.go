// Package fsm implements the Free Space Map: a max-binary-tree over the
// pages of one table that finds a page with at least N free bytes in
// O(log n) without scanning every page (TZ phase 1).
package fsm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
)

// CategoryGranularity: free space is tracked in units of 32 bytes so each
// leaf fits in a uint8 (8192 / 32 = 256 categories).
const CategoryGranularity = 32

// FSM stores the free-space category of every page in a complete binary
// tree laid out as an array: nodes[1] is the root, nodes[2i] / nodes[2i+1]
// are children, and the leaves (one per page) occupy the second half.
// Each internal node holds the maximum category among its descendants.
type FSM struct {
	mu     sync.RWMutex
	path   string
	nodes  []uint8
	nPages uint32
}

// New creates an FSM tracking nPages pages, all initially with zero free
// space recorded. path is where Save persists the map ("" = memory only).
func New(path string, nPages uint32) *FSM {
	f := &FSM{path: path}
	f.resize(nPages)
	return f
}

// resize rebuilds the tree array for at least nPages leaves.
// Caller must hold mu (or have exclusive access).
func (f *FSM) resize(nPages uint32) {
	capacity := uint32(1)
	for capacity < nPages {
		capacity *= 2
	}
	nodes := make([]uint8, 2*capacity)

	if f.nodes != nil {
		oldLeaf := uint32(len(f.nodes) / 2) //nolint:gosec // FSM tree is always power-of-2 sized
		for p := uint32(0); p < f.nPages; p++ {
			nodes[capacity+p] = f.nodes[oldLeaf+p]
		}
	}
	f.nodes = nodes
	f.nPages = nPages
	f.rebuildInternal()
}

// rebuildInternal recomputes every internal node from the leaves.
func (f *FSM) rebuildInternal() {
	leafStart := len(f.nodes) / 2
	for i := leafStart - 1; i >= 1; i-- {
		f.nodes[i] = max8(f.nodes[2*i], f.nodes[2*i+1])
	}
}

func max8(a, b uint8) uint8 {
	if a > b {
		return a
	}
	return b
}

// category converts a byte count to a storage category, rounding down:
// a page in category c is guaranteed to have at least c*32 free bytes.
func category(freeBytes uint16) uint8 {
	c := freeBytes / CategoryGranularity
	if c > 255 {
		c = 255
	}
	return uint8(c) //nolint:gosec // c is bounded to [0,255]
}

// requestCategory converts a request to a category, rounding up, so any
// page found is guaranteed to satisfy it. Requests within 31 bytes of a
// page's recorded free space may miss — the caller then allocates a new
// page, which is the same conservative trade-off PostgreSQL's FSM makes.
func requestCategory(minFree uint16) uint8 {
	c := (uint32(minFree) + CategoryGranularity - 1) / CategoryGranularity
	if c > 255 {
		c = 255
	}
	return uint8(c) //nolint:gosec // c is bounded to [0,255]
}

// PageCount returns the number of tracked pages.
func (f *FSM) PageCount() uint32 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.nPages
}

// Grow extends the map to track nPages pages (no-op if already larger).
func (f *FSM) Grow(nPages uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if nPages <= f.nPages {
		return
	}
	if nPages <= uint32(len(f.nodes)/2) { //nolint:gosec // FSM tree is always power-of-2 sized
		f.nPages = nPages
		return
	}
	f.resize(nPages)
}

// Search finds a page with at least minFree free bytes by walking the tree
// top-down. Returns (pageNo, true) or (0, false) if no page qualifies.
func (f *FSM) Search(minFree uint16) (uint32, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.nPages == 0 {
		return 0, false
	}

	want := requestCategory(minFree)
	leafStart := uint32(len(f.nodes) / 2)

	node := uint32(1)
	if f.nodes[node] < want {
		return 0, false
	}
	for node < leafStart {
		left, right := 2*node, 2*node+1
		if f.nodes[left] >= want {
			node = left
		} else {
			node = right
		}
	}

	pageNo := node - leafStart
	if pageNo >= f.nPages {
		return 0, false
	}
	return pageNo, true
}

// Update records the free space of a page and fixes up ancestors.
// Pages beyond the current size are tracked automatically (Grow).
func (f *FSM) Update(pageNo uint32, freeBytes uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if pageNo >= f.nPages {
		if pageNo >= uint32(len(f.nodes)/2) {
			f.resize(pageNo + 1)
		} else {
			f.nPages = pageNo + 1
		}
	}

	leafStart := uint32(len(f.nodes) / 2)
	leafIdx := leafStart + pageNo
	f.nodes[leafIdx] = category(freeBytes)

	for node := leafIdx / 2; node >= 1; node /= 2 {
		newVal := max8(f.nodes[2*node], f.nodes[2*node+1])
		if f.nodes[node] == newVal {
			break
		}
		f.nodes[node] = newVal
	}
}

// Save persists the map to its path: "VFSM" magic, page count, then one
// category byte per page.
func (f *FSM) Save() error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.path == "" {
		return errors.New("fsm has no backing path")
	}

	leafStart := uint32(len(f.nodes) / 2)
	buf := make([]byte, 8+f.nPages)
	copy(buf, "VFSM")
	binary.LittleEndian.PutUint32(buf[4:], f.nPages)
	copy(buf[8:], f.nodes[leafStart:leafStart+f.nPages])

	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// Load reads a previously saved map from path.
func Load(path string) (*FSM, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(buf) < 8 || string(buf[:4]) != "VFSM" {
		return nil, fmt.Errorf("fsm %s: bad file header", path)
	}
	nPages := binary.LittleEndian.Uint32(buf[4:])
	if uint32(len(buf)-8) < nPages {
		return nil, fmt.Errorf("fsm %s: truncated (want %d leaves, have %d)", path, nPages, len(buf)-8)
	}

	f := New(path, nPages)
	leafStart := uint32(len(f.nodes) / 2)
	copy(f.nodes[leafStart:], buf[8:8+nPages])
	f.rebuildInternal()
	return f, nil
}
