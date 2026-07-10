//go:build windows

package crypto

import (
	"context"
	"fmt"
)

// DPAPISource is a stub on Windows — full DPAPI integration requires
// careful unsafe.Pointer work with DataBlob. For now, returns an error.
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
	return nil, fmt.Errorf("DPAPI not yet fully implemented on Windows")
}
