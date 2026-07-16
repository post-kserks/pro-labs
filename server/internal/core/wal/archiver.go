package wal

import (
	"compress/gzip"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArchiverWorker scans the WAL directory for completed segment files
// and archives them by compressing and moving to the archive directory.
type ArchiverWorker struct {
	walDir     string
	archiveDir string
	ticker     *time.Ticker
	done       chan struct{}
}

func NewArchiverWorker(walDir, archiveDir string, interval time.Duration) *ArchiverWorker {
	return &ArchiverWorker{
		walDir:     walDir,
		archiveDir: archiveDir,
		ticker:     time.NewTicker(interval),
		done:       make(chan struct{}),
	}
}

func (a *ArchiverWorker) Start() {
	go func() {
		for {
			select {
			case <-a.ticker.C:
				a.archiveSegments()
			case <-a.done:
				return
			}
		}
	}()
}

func (a *ArchiverWorker) Stop() {
	if a.ticker != nil {
		a.ticker.Stop()
	}
	close(a.done)
}

func (a *ArchiverWorker) archiveSegments() {
	entries, err := os.ReadDir(a.walDir)
	if err != nil {
		slog.Error("archiver: failed to read wal_dir", "error", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Only process files that start with "wal", are not the active "wal.log",
		// and haven't already been compressed.
		if strings.HasPrefix(name, "wal") && name != "wal.log" && !strings.HasSuffix(name, ".gz") {
			sourcePath := filepath.Join(a.walDir, name)
			destPath := filepath.Join(a.archiveDir, name+".gz")

			if err := a.compressAndMove(sourcePath, destPath); err != nil {
				slog.Error("archiver: failed to archive segment", "file", name, "error", err)
			}
		}
	}
}

func (a *ArchiverWorker) compressAndMove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	if _, err := io.Copy(gw, in); err != nil {
		gw.Close()
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}

	in.Close() // Close before removing
	return os.Remove(src)
}
