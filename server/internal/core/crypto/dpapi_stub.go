//go:build !windows || !cgo

package crypto

import (
	"context"
	"fmt"
)

// DPAPISource is a stub on non-Windows or non-CGO platforms.
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
	return nil, fmt.Errorf("DPAPI not supported on this platform")
}

func protect(data []byte) ([]byte, error) {
	return nil, fmt.Errorf("dpapi: not available on this platform (requires windows + cgo)")
}

func unprotect(blob []byte) ([]byte, error) {
	return nil, fmt.Errorf("dpapi: not available on this platform (requires windows + cgo)")
}
