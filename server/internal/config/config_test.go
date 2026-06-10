package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.MaxRequestSizeBytes != DefaultMaxRequestSize {
		t.Fatalf("default max request size = %d", cfg.Server.MaxRequestSizeBytes)
	}
	if cfg.Storage.Engine != "json" {
		t.Fatalf("default engine = %q", cfg.Storage.Engine)
	}
	if cfg.Server.LiveQueries.DropPolicy != "drop" {
		t.Fatalf("default drop policy = %q", cfg.Server.LiveQueries.DropPolicy)
	}
}

func TestLoadFullConfig(t *testing.T) {
	yaml := `
server:
  host: 0.0.0.0
  port: 6000
  http_port: 8081
  monitor_port: 5434
  max_request_size_bytes: 67108864  # 64 МБ
  live_queries:
    buffer_size: 128
    drop_policy: "evict"
    block_timeout_s: 7

storage:
  engine: page
  data_dir: /data

auth:
  enabled: false

ai:
  provider: ollama
  endpoint: http://localhost:11434/api/embeddings
  model: nomic-embed-text
`
	path := filepath.Join(t.TempDir(), "vaultdb.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 6000 {
		t.Fatalf("server section: %+v", cfg.Server)
	}
	if cfg.Server.MaxRequestSizeBytes != 67108864 {
		t.Fatalf("max_request_size_bytes = %d", cfg.Server.MaxRequestSizeBytes)
	}
	if cfg.Server.LiveQueries.BufferSize != 128 ||
		cfg.Server.LiveQueries.DropPolicy != "evict" ||
		cfg.Server.LiveQueries.BlockTimeoutS != 7 {
		t.Fatalf("live_queries section: %+v", cfg.Server.LiveQueries)
	}
	if cfg.Storage.Engine != "page" || cfg.Storage.DataDir != "/data" {
		t.Fatalf("storage section: %+v", cfg.Storage)
	}
	if cfg.Auth.Enabled {
		t.Fatal("auth.enabled should be false")
	}
	if cfg.AI.Provider != "ollama" || cfg.AI.Model != "nomic-embed-text" {
		t.Fatalf("ai section: %+v", cfg.AI)
	}
	if cfg.AI.Endpoint != "http://localhost:11434/api/embeddings" {
		t.Fatalf("ai.endpoint = %q", cfg.AI.Endpoint)
	}
}

func TestInvalidValues(t *testing.T) {
	for name, yaml := range map[string]string{
		"bad port":   "server:\n  port: abc\n",
		"bad policy": "server:\n  live_queries:\n    drop_policy: whatever\n",
		"bad engine": "storage:\n  engine: mongodb\n",
	} {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}
