//go:build !windows

package heap

import (
	"golang.org/x/sys/unix"
	"os"
)

type mmapFile struct {
	file     *os.File
	mmapData []byte
}

func openMmapFile(path string, flag int, perm os.FileMode) (*mmapFile, error) {
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := info.Size()
	mf := &mmapFile{file: f}
	if size > 0 {
		data, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err != nil {
			f.Close()
			return nil, err
		}
		mf.mmapData = data
	}
	return mf, nil
}

func (mf *mmapFile) ReadAt(b []byte, off int64) (int, error) {
	if int(off)+len(b) > len(mf.mmapData) {
		return mf.file.ReadAt(b, off)
	}
	n := copy(b, mf.mmapData[off:])
	return n, nil
}

func (mf *mmapFile) WriteAt(b []byte, off int64) (int, error) {
	if int(off)+len(b) > len(mf.mmapData) {
		// grow file
		newSize := int(off) + len(b)
		if err := mf.file.Truncate(int64(newSize)); err != nil {
			return 0, err
		}
		if len(mf.mmapData) > 0 {
			if err := unix.Munmap(mf.mmapData); err != nil {
				return 0, err
			}
		}
		data, err := unix.Mmap(int(mf.file.Fd()), 0, newSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err != nil {
			return 0, err
		}
		mf.mmapData = data
	}
	n := copy(mf.mmapData[off:], b)
	return n, nil
}

func (mf *mmapFile) Sync() error {
	if len(mf.mmapData) > 0 {
		if err := unix.Msync(mf.mmapData, unix.MS_SYNC); err != nil {
			return err
		}
	}
	return mf.file.Sync()
}

func (mf *mmapFile) Close() error {
	var err1 error
	if len(mf.mmapData) > 0 {
		err1 = unix.Munmap(mf.mmapData)
		mf.mmapData = nil
	}
	err2 := mf.file.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func (mf *mmapFile) Stat() (os.FileInfo, error) {
	return mf.file.Stat()
}
