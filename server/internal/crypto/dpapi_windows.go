//go:build windows

package crypto

import (
	"context"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DPAPISource uses Windows Data Protection API to protect and retrieve
// a Key Encryption Key (KEK). The protected blob is persisted to disk
// at blobPath so the key survives process restarts.
type DPAPISource struct {
	blobPath string
}

func NewDPAPISource(blobPath string) *DPAPISource {
	return &DPAPISource{blobPath: blobPath}
}

func (s *DPAPISource) Name() string {
	return "dpapi"
}

func (s *DPAPISource) GetKEK(_ context.Context) ([]byte, error) {
	data, err := os.ReadFile(s.blobPath)
	if err == nil {
		return s.unprotect(data)
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read dpapi blob: %w", err)
	}
	return s.generateAndProtect()
}

func (s *DPAPISource) generateAndProtect() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := windows.RtlGenRandom((*byte)(unsafe.Pointer(&key[0])), uint32(len(key))); err != nil {
		return nil, fmt.Errorf("generate random key: %w", err)
	}
	blob, err := s.protect(key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.blobPath, blob, 0600); err != nil {
		return nil, fmt.Errorf("write dpapi blob: %w", err)
	}
	return key, nil
}

func (s *DPAPISource) protect(data []byte) ([]byte, error) {
	in := windows.DataBlob{
		Len: uint32(len(data)),
		PbData: (*byte)(unsafe.Pointer(&data[0])),
	}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, nil, nil, 0, &out); err != nil {
		return nil, fmt.Errorf("dpapi protect: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.PbData)))
	blob := make([]byte, out.Len)
	copy(blob, unsafe.Slice(out.PbData, out.Len))
	return blob, nil
}

func (s *DPAPISource) unprotect(blob []byte) ([]byte, error) {
	in := windows.DataBlob{
		Len: uint32(len(blob)),
		PbData: (*byte)(unsafe.Pointer(&blob[0])),
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, nil, nil, 0, &out); err != nil {
		return nil, fmt.Errorf("dpapi unprotect: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.PbData)))
	result := make([]byte, out.Len)
	copy(result, unsafe.Slice(out.PbData, out.Len))
	return result, nil
}
