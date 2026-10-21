package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func Restore(backupPath, dataDir string) error {
	f, err := os.Open(backupPath)
	if err != nil {
		return fmt.Errorf("open backup file: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		// Sanitize and validate path to prevent Zip Slip (Path Traversal)
		relPath := filepath.FromSlash(header.Name)
		target := filepath.Join(dataDir, relPath)
		cleanTarget := filepath.Clean(target)
		cleanDataDir := filepath.Clean(dataDir)
		if cleanTarget != cleanDataDir && !strings.HasPrefix(cleanTarget, cleanDataDir+string(filepath.Separator)) {
			return fmt.Errorf("illegal path traversal in backup archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleanTarget, 0o750); err != nil {
				return err
			}
			continue
		case tar.TypeReg:
			// Regular files allowed
		default:
			// Reject symlinks, hardlinks, devices, etc.
			return fmt.Errorf("unsupported entry type %v for %s", header.Typeflag, header.Name)
		}

		// Check for pre-existing symlink target
		if fi, err := os.Lstat(cleanTarget); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to overwrite symlink target: %s", cleanTarget)
			}
		}

		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o750); err != nil {
			return err
		}

		safeMode := os.FileMode(header.Mode) & 0o600
		if safeMode == 0 {
			safeMode = 0o600
		}

		out, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, safeMode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, 1<<30)); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}

	return nil
}
