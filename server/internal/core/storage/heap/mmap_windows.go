//go:build windows

package heap

import (
	"os"
)

type mmapFile struct {
	file *os.File
}

func openMmapFile(path string, flag int, perm os.FileMode) (*mmapFile, error) {
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	return &mmapFile{file: f}, nil
}

func (mf *mmapFile) ReadAt(b []byte, off int64) (int, error) {
	return mf.file.ReadAt(b, off)
}

func (mf *mmapFile) WriteAt(b []byte, off int64) (int, error) {
	return mf.file.WriteAt(b, off)
}

func (mf *mmapFile) Sync() error {
	return mf.file.Sync()
}

func (mf *mmapFile) Close() error {
	return mf.file.Close()
}

func (mf *mmapFile) Stat() (os.FileInfo, error) {
	return mf.file.Stat()
}
