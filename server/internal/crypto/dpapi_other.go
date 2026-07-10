//go:build !windows

package crypto

import (
	"context"
	"fmt"
)

// DPAPISource is a stub on non-Windows platforms.
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
