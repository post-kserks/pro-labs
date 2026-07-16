package wal

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RestoreAsOf replays WAL records from both archiveDir and activeDir
// but stops exactly at the first transaction whose timestamp is >= targetTimestamp.
func (w *WAL) RestoreAsOf(archiveDir string, activeDir string, targetTimestamp time.Time) ([]Entry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var files []string

	// Gather archived segments
	if archiveDir != "" {
		entries, _ := os.ReadDir(archiveDir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), "wal") {
				files = append(files, filepath.Join(archiveDir, e.Name()))
			}
		}
	}

	// Gather active segments
	if activeDir != "" {
		entries, _ := os.ReadDir(activeDir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), "wal") {
				files = append(files, filepath.Join(activeDir, e.Name()))
			}
		}
	}

	sort.Strings(files)

	var entries []Entry
	var maxTxID uint64

	for _, file := range files {
		fEntries, fStop, maxID, err := w.readEntriesUpTo(file, targetTimestamp)
		if err != nil {
			return nil, err
		}
		if maxID > maxTxID {
			maxTxID = maxID
		}
		entries = append(entries, fEntries...)
		if fStop {
			break
		}
	}

	if maxTxID > w.nextTxID.Load() {
		w.nextTxID.Store(maxTxID)
	}

	return entries, nil
}

func (w *WAL) readEntriesUpTo(path string, target time.Time) ([]Entry, bool, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, 0, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gr, err := gzip.NewReader(f)
		if err != nil {
			return nil, false, 0, err
		}
		defer gr.Close()
		r = gr
	}

	var entries []Entry
	var maxTxID uint64
	targetNs := target.UnixNano()

	for {
		entry, _, err := w.readEntryFrom(r)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, false, 0, err
		}

		if entry.TxID > maxTxID {
			maxTxID = entry.TxID
		}

		// Check timestamp if it's a commit
		if entry.OpType == OpCommit {
			decoded, err := DecodeWALPayload(entry.Payload, entry.OpType)
			if err == nil {
				if cp, ok := decoded.(CommitPayload); ok {
					if cp.Timestamp >= targetNs {
						return entries, true, maxTxID, nil
					}
				}
			}
		}

		entries = append(entries, entry)
	}

	return entries, false, maxTxID, nil
}
