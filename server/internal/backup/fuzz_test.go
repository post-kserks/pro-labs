package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzBackupRestore(f *testing.F) {
	// Create a valid backup to seed the corpus
	tmpDir := f.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	os.MkdirAll(filepath.Join(dataDir, "pagedb", "testdb"), 0o755)
	os.WriteFile(filepath.Join(dataDir, "pagedb", "testdb", "test.tbl"), []byte("test page data"), 0o644)
	os.MkdirAll(filepath.Join(dataDir, "wal"), 0o755)
	os.WriteFile(filepath.Join(dataDir, "wal", "test.wal"), []byte("test wal data"), 0o644)

	backupPath := filepath.Join(tmpDir, "valid.bak")
	err := Backup(dataDir, backupPath)
	if err == nil {
		validBackup, _ := os.ReadFile(backupPath)
		f.Add(validBackup)
	}

	// Corrupted variants of valid backup
	if err == nil {
		validBackup, _ := os.ReadFile(backupPath)
		if len(validBackup) > 10 {
			corrupted := make([]byte, len(validBackup))
			copy(corrupted, validBackup)
			corrupted[len(corrupted)/2] ^= 0xFF
			f.Add(corrupted)
		}
		if len(validBackup) > 20 {
			f.Add(validBackup[:len(validBackup)/3])
		}
	}

	// Edge cases
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add([]byte("not a backup"))

	// Constructed valid gzip+tar with various entries
	{
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		tw.WriteHeader(&tar.Header{
			Name:     "pagedb/db1/table.tbl",
			Mode:     0o644,
			Size:     int64(len("page data")),
			Typeflag: tar.TypeReg,
		})
		tw.Write([]byte("page data"))
		tw.WriteHeader(&tar.Header{
			Name:     "wal/wal.log",
			Mode:     0o644,
			Size:     int64(len("wal data")),
			Typeflag: tar.TypeReg,
		})
		tw.Write([]byte("wal data"))
		tw.WriteHeader(&tar.Header{
			Name:     "pagedb/db1/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		})
		tw.Close()
		gw.Close()
		f.Add(buf.Bytes())
	}

	// Constructed gzip+tar with path traversal attempt
	{
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		tw.WriteHeader(&tar.Header{
			Name:     "../../etc/passwd",
			Mode:     0o644,
			Size:     4,
			Typeflag: tar.TypeReg,
		})
		tw.Write([]byte("pwn!"))
		tw.Close()
		gw.Close()
		f.Add(buf.Bytes())
	}

	// Constructed gzip+tar with symlink
	{
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		tw.WriteHeader(&tar.Header{
			Name:     "pagedb/evil",
			Linkname: "/etc/shadow",
			Typeflag: tar.TypeSymlink,
			Mode:     0o777,
		})
		tw.Close()
		gw.Close()
		f.Add(buf.Bytes())
	}

	// Constructed gzip+tar with very long entry name
	{
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		tw.WriteHeader(&tar.Header{
			Name:     "pagedb/" + strings.Repeat("a", 10000) + ".tbl",
			Mode:     0o644,
			Size:     1,
			Typeflag: tar.TypeReg,
		})
		tw.Write([]byte("x"))
		tw.Close()
		gw.Close()
		f.Add(buf.Bytes())
	}

	// Random template-based seeds
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 30; i++ {
		// Random gzip data
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		b := make([]byte, rng.Intn(200))
		rng.Read(b)
		gw.Write(b)
		gw.Close()
		f.Add(buf.Bytes())
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("backup/restore panicked: %v", r)
			}
		}()

		// Write fuzzed data to a temp file
		tmpDir := t.TempDir()
		backupPath := filepath.Join(tmpDir, "fuzz.bak")
		if err := os.WriteFile(backupPath, data, 0o644); err != nil {
			return
		}

		restoreDir := filepath.Join(tmpDir, "restore")
		os.MkdirAll(restoreDir, 0o755)

		// Restore should handle all corruption gracefully
		_ = Restore(backupPath, restoreDir)
	})
}
