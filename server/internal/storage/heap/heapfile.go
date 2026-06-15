// Package heap implements the heap file manager: a set of 1GB segment files
// per table, addressed page by page (TZ phase 1).
package heap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"vaultdb/internal/storage/page"
)

// ErrChecksumMismatch is returned by ReadPage when a page is corrupted.
var ErrChecksumMismatch = errors.New("page checksum mismatch")

// HeapFile manages the segment files of one table and provides page-level
// read/write/allocate operations. File handles are safe for concurrent
// ReadAt/WriteAt; the mutex only guards the segment list.
type HeapFile struct {
	dir      string
	mu       sync.RWMutex
	segments []*os.File
	closed   bool
}

func segmentName(segNo uint16) string {
	return fmt.Sprintf("%04d.heap", segNo)
}

// CreateHeapFile creates the table directory and its first segment.
// Fails if a segment 0 already exists.
func CreateHeapFile(dir string) (*HeapFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, segmentName(0)), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	return &HeapFile{dir: dir, segments: []*os.File{f}}, nil
}

// OpenHeapFile opens an existing heap file (all consecutive segments).
func OpenHeapFile(dir string) (*HeapFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}

	hf := &HeapFile{dir: dir}
	for segNo := uint16(0); names[segmentName(segNo)]; segNo++ {
		f, err := os.OpenFile(filepath.Join(dir, segmentName(segNo)), os.O_RDWR, 0o644)
		if err != nil {
			hf.Close()
			return nil, err
		}
		hf.segments = append(hf.segments, f)
	}
	if len(hf.segments) == 0 {
		return nil, fmt.Errorf("open heap file %s: no segments found", dir)
	}
	return hf, nil
}

// Close closes all segment file descriptors.
func (hf *HeapFile) Close() error {
	hf.mu.Lock()
	defer hf.mu.Unlock()
	hf.closed = true
	var firstErr error
	for _, seg := range hf.segments {
		if err := seg.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	hf.segments = nil
	return firstErr
}

func (hf *HeapFile) getSegment(segNo uint16) (*os.File, error) {
	hf.mu.RLock()
	defer hf.mu.RUnlock()
	if hf.closed {
		return nil, errors.New("heap file is closed")
	}
	if int(segNo) >= len(hf.segments) {
		return nil, fmt.Errorf("segment %d does not exist", segNo)
	}
	return hf.segments[segNo], nil
}

// ReadPage reads a page from disk into buf and verifies its checksum.
func (hf *HeapFile) ReadPage(pid page.PageID, buf *page.Page) error {
	hf.mu.RLock()
	if hf.closed {
		hf.mu.RUnlock()
		return errors.New("heap file is closed")
	}
	if int(pid.SegmentNo) >= len(hf.segments) {
		hf.mu.RUnlock()
		return fmt.Errorf("segment %d does not exist", pid.SegmentNo)
	}
	seg := hf.segments[pid.SegmentNo]

	if _, err := seg.ReadAt(buf[:], pid.FileOffset()); err != nil {
		hf.mu.RUnlock()
		return fmt.Errorf("readpage %v: %w", pid, err)
	}
	hf.mu.RUnlock()

	if !buf.VerifyChecksum() {
		return fmt.Errorf("page %v: stored=%d computed=%d: %w",
			pid, buf.Header().Checksum, buf.ComputeChecksum(), ErrChecksumMismatch)
	}
	return nil
}

// WritePage computes the page checksum and writes the page to disk.
// It does NOT fsync — durability ordering is the WAL's responsibility.
func (hf *HeapFile) WritePage(pid page.PageID, buf *page.Page) error {
	hf.mu.RLock()
	if hf.closed {
		hf.mu.RUnlock()
		return errors.New("heap file is closed")
	}
	if int(pid.SegmentNo) >= len(hf.segments) {
		hf.mu.RUnlock()
		return fmt.Errorf("segment %d does not exist", pid.SegmentNo)
	}
	seg := hf.segments[pid.SegmentNo]

	buf.SetChecksum()
	_, err := seg.WriteAt(buf[:], pid.FileOffset())
	hf.mu.RUnlock()
	return err
}

// AllocatePage appends a new initialized page to the end of the last
// segment, creating a new segment when the current one reaches 1GB.
func (hf *HeapFile) AllocatePage(pageType uint8) (page.PageID, *page.Page, error) {
	hf.mu.Lock()
	defer hf.mu.Unlock()

	if hf.closed || len(hf.segments) == 0 {
		return page.PageID{}, nil, errors.New("heap file is closed")
	}

	segNo := uint16(len(hf.segments) - 1)
	seg := hf.segments[segNo]

	info, err := seg.Stat()
	if err != nil {
		return page.PageID{}, nil, err
	}
	pageNo := uint32(info.Size() / page.PageSize)

	if pageNo >= page.PagesPerSegment {
		segNo++
		seg, err = os.OpenFile(filepath.Join(hf.dir, segmentName(segNo)), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return page.PageID{}, nil, err
		}
		hf.segments = append(hf.segments, seg)
		pageNo = 0
	}

	pid := page.PageID{SegmentNo: segNo, PageNo: pageNo}
	buf := &page.Page{}
	buf.Init(pageType)
	buf.SetChecksum()

	if _, err := seg.WriteAt(buf[:], pid.FileOffset()); err != nil {
		return page.PageID{}, nil, err
	}
	return pid, buf, nil
}

// PageCount returns the total number of pages across all segments.
func (hf *HeapFile) PageCount() (uint32, error) {
	hf.mu.RLock()
	defer hf.mu.RUnlock()
	if hf.closed {
		return 0, errors.New("heap file is closed")
	}
	var total uint32
	for _, seg := range hf.segments {
		info, err := seg.Stat()
		if err != nil {
			return 0, err
		}
		total += uint32(info.Size() / page.PageSize)
	}
	return total, nil
}

// SegmentCount returns the number of segment files.
func (hf *HeapFile) SegmentCount() int {
	hf.mu.RLock()
	defer hf.mu.RUnlock()
	if hf.closed {
		return 0
	}
	return len(hf.segments)
}

// Sync fsyncs all segments (used by checkpoint / graceful shutdown).
func (hf *HeapFile) Sync() error {
	hf.mu.RLock()
	segs := make([]*os.File, len(hf.segments))
	copy(segs, hf.segments)
	hf.mu.RUnlock()

	for _, seg := range segs {
		if err := seg.Sync(); err != nil {
			return err
		}
	}
	return nil
}
