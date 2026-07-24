package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

		target := filepath.Join(dataDir, filepath.FromSlash(header.Name))

		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
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
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}

	return nil
}
