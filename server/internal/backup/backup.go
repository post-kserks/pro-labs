package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func Backup(dataDir, backupPath string) error {
	f, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, dir := range []string{"pagedb", "wal"} {
		root := filepath.Join(dataDir, dir)
		if err := addDirToTar(tw, root, dir); err != nil {
			return fmt.Errorf("add %s to tar: %w", dir, err)
		}
	}

	return nil
}

func addDirToTar(tw *tar.Writer, rootDir, prefix string) error {
	return filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		name := filepath.Join(prefix, rel)

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		header.Size = info.Size()

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}
