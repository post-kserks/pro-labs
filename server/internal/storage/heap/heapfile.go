// Package heap implements the heap file manager: a set of 1GB segment files
// per table, addressed page by page (TZ phase 1).
package heap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"vaultdb/internal/crypto"
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

	// Cached page count — avoids repeated Stat() calls on every segment.
	cachedPageCount uint32
	pageCountValid  bool
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
	return &HeapFile{dir: dir, segments: []*os.File{f}, cachedPageCount: 0, pageCountValid: true}, nil
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

// ReadPageAhead reads `ahead` consecutive pages starting at pid in parallel.
// Pages beyond the heap boundary are silently dropped. Returns the pages that
// were successfully read (length ≤ ahead) and the first error encountered.
func (hf *HeapFile) ReadPageAhead(pid page.PageID, ahead int) ([]*page.Page, error) {
	if ahead <= 0 {
		return nil, nil
	}

	total, err := hf.PageCount()
	if err != nil {
		return nil, err
	}

	// Clamp `ahead` to what actually exists starting from pid.
	if int(pid.PageNo)+ahead > int(total) {
		ahead = int(total) - int(pid.PageNo)
	}
	if ahead <= 0 {
		return nil, nil
	}

	pages := make([]*page.Page, ahead)
	errs := make([]error, ahead)
	var wg sync.WaitGroup

	for i := 0; i < ahead; i++ {
		nextPid := page.PageID{
			TableID:   pid.TableID,
			SegmentNo: pid.SegmentNo,
			PageNo:    pid.PageNo + uint32(i),
		}
		pages[i] = &page.Page{}
		wg.Add(1)
		go func(p *page.Page, idx int) {
			defer wg.Done()
			errs[idx] = hf.ReadPage(nextPid, p)
		}(pages[i], i)
	}
	wg.Wait()

	// Return partial results up to the first error.
	for i, e := range errs {
		if e != nil {
			return pages[:i], e
		}
	}
	return pages, nil
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
	hf.mu.RUnlock()

	if _, err := seg.ReadAt(buf[:], pid.FileOffset()); err != nil {
		return fmt.Errorf("readpage %v: %w", pid, err)
	}

	if !buf.VerifyChecksum() {
		return fmt.Errorf("page %v: stored=%d computed=%d: %w",
			pid, buf.Header().Checksum, buf.ComputeChecksum(), ErrChecksumMismatch)
	}
	return nil
}

// ReadPageEncrypted reads a page from disk into buf, decrypting if necessary.
// If em is nil, the page is read as plaintext (same as ReadPage).
func (hf *HeapFile) ReadPageEncrypted(pid page.PageID, buf *page.Page, em *crypto.EncryptionManager) error {
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
	hf.mu.RUnlock()

	if em == nil {
		if _, err := seg.ReadAt(buf[:], pid.FileOffset()); err != nil {
			return fmt.Errorf("readpage %v: %w", pid, err)
		}
		if !buf.VerifyChecksum() {
			return fmt.Errorf("page %v: stored=%d computed=%d: %w",
				pid, buf.Header().Checksum, buf.ComputeChecksum(), ErrChecksumMismatch)
		}
		return nil
	}

	raw := make([]byte, page.EncryptedOnDiskPageSize)
	if _, err := seg.ReadAt(raw, pid.EncryptedFileOffset()); err != nil {
		return fmt.Errorf("readpage encrypted %v: %w", pid, err)
	}

	if !page.IsEncryptedPage(raw) {
		return fmt.Errorf("page %v: expected encrypted page, got unencrypted or corrupt data", pid)
	}
	hdr, err := page.ParseEncryptedHeader(raw)
	if err != nil {
		return err
	}
	ciphertext := raw[page.EncryptedPageHeaderSize:]

	plaintext, err := em.DecryptPage(hdr.Nonce[:], ciphertext, pid.Bytes(), hdr.KeyVersion)
	if err != nil {
		return fmt.Errorf("decrypt page %v: %w", pid, err)
	}
	copy(buf[:], plaintext)

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
	hf.mu.RUnlock()

	buf.SetChecksum()
	_, err := seg.WriteAt(buf[:], pid.FileOffset())
	return err
}

// WritePageEncrypted encrypts the page and writes it to disk. If em is nil,
// the page is written in plaintext (same as WritePage).
func (hf *HeapFile) WritePageEncrypted(pid page.PageID, buf *page.Page, em *crypto.EncryptionManager) error {
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
	hf.mu.RUnlock()

	if em == nil {
		buf.SetChecksum()
		_, err := seg.WriteAt(buf[:], pid.FileOffset())
		return err
	}

	buf.SetChecksum()
	nonce, ciphertext, err := em.EncryptPage(buf[:], pid.Bytes())
	if err != nil {
		return fmt.Errorf("encrypt page: %w", err)
	}

	out := make([]byte, page.EncryptedOnDiskPageSize)
	copy(out[0:4], []byte(page.EncryptedPageMagic))
	binary.LittleEndian.PutUint32(out[4:8], em.KeyVersion())
	copy(out[8:20], nonce)
	copy(out[20:], ciphertext)

	_, err = seg.WriteAt(out, pid.EncryptedFileOffset())
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

	segCount := len(hf.segments) - 1
	if segCount < 0 || segCount > 0xFFFF {
		return page.PageID{}, nil, fmt.Errorf("segment count out of range: %d", len(hf.segments))
	}
	segNo := uint16(segCount)
	seg := hf.segments[segNo]

	info, err := seg.Stat()
	if err != nil {
		return page.PageID{}, nil, err
	}
	pagesInSeg := info.Size() / page.PageSize
	if pagesInSeg < 0 || pagesInSeg > 0xFFFFFFFF {
		return page.PageID{}, nil, fmt.Errorf("page count out of range for segment %d", segNo)
	}
	pageNo := uint32(pagesInSeg)

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

	// Invalidate cached page count since we just added a page.
	hf.pageCountValid = false

	return pid, buf, nil
}

// PageCount returns the total number of pages across all segments.
func (hf *HeapFile) PageCount() (uint32, error) {
	hf.mu.RLock()
	if hf.pageCountValid {
		count := hf.cachedPageCount
		hf.mu.RUnlock()
		return count, nil
	}
	hf.mu.RUnlock()

	hf.mu.Lock()
	defer hf.mu.Unlock()

	// Double-check after acquiring write lock
	if hf.pageCountValid {
		return hf.cachedPageCount, nil
	}
	if hf.closed {
		return 0, errors.New("heap file is closed")
	}
	var total uint64
	for _, seg := range hf.segments {
		info, err := seg.Stat()
		if err != nil {
			return 0, err
		}
		total += uint64(info.Size() / page.PageSize) //nolint:gosec // single segment page count fits in uint64
	}
	if total > 0xFFFFFFFF {
		return 0, fmt.Errorf("total page count exceeds uint32 range: %d", total)
	}
	hf.cachedPageCount = uint32(total)
	hf.pageCountValid = true
	return hf.cachedPageCount, nil
}

// InvalidatePageCount marks the cached page count as stale so the next
// PageCount() call recomputes from disk. Must be called after AllocatePage
// or any operation that changes the on-disk page count.
func (hf *HeapFile) InvalidatePageCount() {
	hf.mu.Lock()
	defer hf.mu.Unlock()
	hf.pageCountValid = false
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
