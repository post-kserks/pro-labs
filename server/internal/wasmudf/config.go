package wasmudf

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// WASMConfig holds configurable WASM UDF settings.
type WASMConfig struct {
	AllowedExports []string `json:"allowed_exports"` // if set, overrides default export whitelist
}

// DefaultAllowedExports is the default export whitelist when WASMConfig.AllowedExports is empty.
var DefaultAllowedExports = []string{
	"alloc",
	"execute",
	"execute_args",
	"result_len",
	"result_copy",
}

// GetAllowedExports returns the configured export whitelist, falling back to DefaultAllowedExports.
func (c *WASMConfig) GetAllowedExports() []string {
	if len(c.AllowedExports) > 0 {
		return c.AllowedExports
	}
	return DefaultAllowedExports
}

// ParseOptions parses WASM function options from the WITH clause.
func ParseOptions(opts map[string]string) (memoryLimit uint32, timeout time.Duration, err error) {
	for k, v := range opts {
		switch strings.ToLower(k) {
		case "memory_limit":
			limit, parseErr := ParseMemoryLimit(v)
			if parseErr != nil {
				return 0, 0, fmt.Errorf("invalid memory_limit: %w", parseErr)
			}
			memoryLimit = limit
		case "timeout":
			d, parseErr := time.ParseDuration(v)
			if parseErr != nil {
				return 0, 0, fmt.Errorf("invalid timeout: %w", parseErr)
			}
			timeout = d
		}
	}
	return
}

// ParseMemoryLimit parses a human-readable memory limit string (e.g., "256MB", "1GB", "4096").
func ParseMemoryLimit(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	s = strings.ToUpper(s)

	if strings.HasSuffix(s, "MB") {
		n, err := strconv.ParseUint(s[:len(s)-2], 10, 32)
		if err != nil {
			return 0, err
		}
		return uint32(n * 1024 * 1024), nil
	}
	if strings.HasSuffix(s, "KB") {
		n, err := strconv.ParseUint(s[:len(s)-2], 10, 32)
		if err != nil {
			return 0, err
		}
		return uint32(n * 1024), nil
	}
	if strings.HasSuffix(s, "GB") {
		n, err := strconv.ParseUint(s[:len(s)-2], 10, 32)
		if err != nil {
			return 0, err
		}
		return uint32(n * 1024 * 1024 * 1024), nil
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(n), nil
}
