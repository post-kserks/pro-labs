package benchmarks

import (
	"io"
	"log"
	"log/slog"
	"os"
	"testing"
)

// TestMain silences engine logging. `go test` merges the test binary's stderr
// into its stdout, so log lines would land in the middle of benchmark result
// lines and break benchstat parsing in CI.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}
