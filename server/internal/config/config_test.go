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
	if cfg.Storage.Engine != "page" {
		t.Fatalf("default engine = %q", cfg.Storage.Engine)
	}
	if cfg.Server.LiveQueries.DropPolicy != "drop" {
		t.Fatalf("default drop policy = %q", cfg.Server.LiveQueries.DropPolicy)
	}
	if cfg.Storage.BufferPoolPages != DefaultBufferPoolPages {
		t.Fatalf("default buffer_pool_pages = %d, want %d", cfg.Storage.BufferPoolPages, DefaultBufferPoolPages)
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
  buffer_pool_pages: 8192

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
	if cfg.Storage.BufferPoolPages != 8192 {
		t.Fatalf("storage.buffer_pool_pages = %d, want 8192", cfg.Storage.BufferPoolPages)
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

// --- ApplyEnvOverrides tests ---

func TestApplyEnvOverrides_Host(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_HOST", "10.0.0.1")
	ApplyEnvOverrides(cfg)
	if cfg.Server.Host != "10.0.0.1" {
		t.Fatalf("host = %q, want 10.0.0.1", cfg.Server.Host)
	}
}

func TestApplyEnvOverrides_Port(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_PORT", "9999")
	ApplyEnvOverrides(cfg)
	if cfg.Server.Port != 9999 {
		t.Fatalf("port = %d, want 9999", cfg.Server.Port)
	}
}

func TestApplyEnvOverrides_PortInvalid(t *testing.T) {
	cfg := Default()
	cfg.Server.Port = 5432
	t.Setenv("VAULTDB_PORT", "not-a-number")
	ApplyEnvOverrides(cfg)
	if cfg.Server.Port != 5432 {
		t.Fatalf("port should remain 5432 on invalid env, got %d", cfg.Server.Port)
	}
}

func TestApplyEnvOverrides_PortOutOfRange(t *testing.T) {
	cfg := Default()
	cfg.Server.Port = 5432
	t.Setenv("VAULTDB_PORT", "70000")
	ApplyEnvOverrides(cfg)
	if cfg.Server.Port != 5432 {
		t.Fatalf("port should remain 5432 on out-of-range env, got %d", cfg.Server.Port)
	}
}

func TestApplyEnvOverrides_HTTPPort(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_HTTP_PORT", "9090")
	ApplyEnvOverrides(cfg)
	if cfg.Server.HTTPPort != 9090 {
		t.Fatalf("http_port = %d, want 9090", cfg.Server.HTTPPort)
	}
}

func TestApplyEnvOverrides_HTTPPortInvalid(t *testing.T) {
	cfg := Default()
	cfg.Server.HTTPPort = 8080
	t.Setenv("VAULTDB_HTTP_PORT", "xyz")
	ApplyEnvOverrides(cfg)
	if cfg.Server.HTTPPort != 8080 {
		t.Fatalf("http_port should remain 8080, got %d", cfg.Server.HTTPPort)
	}
}

func TestApplyEnvOverrides_HTTPPortOutOfRange(t *testing.T) {
	cfg := Default()
	cfg.Server.HTTPPort = 8080
	t.Setenv("VAULTDB_HTTP_PORT", "0")
	ApplyEnvOverrides(cfg)
	if cfg.Server.HTTPPort != 8080 {
		t.Fatalf("http_port should remain 8080, got %d", cfg.Server.HTTPPort)
	}
}

func TestApplyEnvOverrides_MonitorPort(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_MONITOR_PORT", "5435")
	ApplyEnvOverrides(cfg)
	if cfg.Server.MonitorPort != 5435 {
		t.Fatalf("monitor_port = %d, want 5435", cfg.Server.MonitorPort)
	}
}

func TestApplyEnvOverrides_MonitorPortInvalid(t *testing.T) {
	cfg := Default()
	cfg.Server.MonitorPort = 5433
	t.Setenv("VAULTDB_MONITOR_PORT", "abc")
	ApplyEnvOverrides(cfg)
	if cfg.Server.MonitorPort != 5433 {
		t.Fatalf("monitor_port should remain 5433, got %d", cfg.Server.MonitorPort)
	}
}

func TestApplyEnvOverrides_MonitorPortOutOfRange(t *testing.T) {
	cfg := Default()
	cfg.Server.MonitorPort = 5433
	t.Setenv("VAULTDB_MONITOR_PORT", "99999")
	ApplyEnvOverrides(cfg)
	if cfg.Server.MonitorPort != 5433 {
		t.Fatalf("monitor_port should remain 5433, got %d", cfg.Server.MonitorPort)
	}
}

func TestApplyEnvOverrides_DataDir(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_DATA_DIR", "/custom/data")
	ApplyEnvOverrides(cfg)
	if cfg.Storage.DataDir != "/custom/data" {
		t.Fatalf("data_dir = %q, want /custom/data", cfg.Storage.DataDir)
	}
}

func TestApplyEnvOverrides_MTLSEnabled(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_MTLS_ENABLED", "true")
	ApplyEnvOverrides(cfg)
	if !cfg.Auth.MTLSEnabled {
		t.Fatal("mtls_enabled should be true")
	}
}

func TestApplyEnvOverrides_MTLSCaFile(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_MTLS_CA_FILE", "/path/to/ca.pem")
	ApplyEnvOverrides(cfg)
	if cfg.Auth.MTLScaFile != "/path/to/ca.pem" {
		t.Fatalf("mtls_ca_file = %q", cfg.Auth.MTLScaFile)
	}
}

func TestApplyEnvOverrides_AIKey(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_AI_API_KEY", "secret-key-123")
	ApplyEnvOverrides(cfg)
	if cfg.AI.APIKey != "secret-key-123" {
		t.Fatalf("ai_api_key = %q", cfg.AI.APIKey)
	}
}

func TestApplyEnvOverrides_BufferPoolPages(t *testing.T) {
	cfg := Default()
	t.Setenv("VAULTDB_BUFFER_POOL_PAGES", "32768")
	ApplyEnvOverrides(cfg)
	if cfg.Storage.BufferPoolPages != 32768 {
		t.Fatalf("buffer_pool_pages = %d, want 32768", cfg.Storage.BufferPoolPages)
	}
}

func TestApplyEnvOverrides_BufferPoolPagesInvalid(t *testing.T) {
	cfg := Default()
	cfg.Storage.BufferPoolPages = 16384
	t.Setenv("VAULTDB_BUFFER_POOL_PAGES", "not-a-number")
	ApplyEnvOverrides(cfg)
	if cfg.Storage.BufferPoolPages != 16384 {
		t.Fatalf("buffer_pool_pages should remain 16384 on invalid env, got %d", cfg.Storage.BufferPoolPages)
	}
}

func TestApplyEnvOverrides_BufferPoolPagesTooSmall(t *testing.T) {
	cfg := Default()
	cfg.Storage.BufferPoolPages = 16384
	t.Setenv("VAULTDB_BUFFER_POOL_PAGES", "10")
	ApplyEnvOverrides(cfg)
	if cfg.Storage.BufferPoolPages != 16384 {
		t.Fatalf("buffer_pool_pages should remain 16384 when too small, got %d", cfg.Storage.BufferPoolPages)
	}
}

func TestApplyEnvOverrides_EmptyEnvIgnored(t *testing.T) {
	cfg := Default()
	cfg.Server.Host = "original"
	t.Setenv("VAULTDB_HOST", "")
	ApplyEnvOverrides(cfg)
	if cfg.Server.Host != "original" {
		t.Fatalf("empty env should be ignored, host = %q", cfg.Server.Host)
	}
}

// --- envBoolValue tests ---

func TestEnvBoolValue(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"Yes", true},
		{"YES", true},
		{"on", true},
		{"On", true},
		{"ON", true},
		{" true ", true},
		{"  YES  ", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"off", false},
		{"", false},
		{"maybe", false},
		{"2", false},
		{"-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := envBoolValue(tt.input)
			if got != tt.want {
				t.Errorf("envBoolValue(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- Reload tests ---

func TestReload(t *testing.T) {
	yaml1 := `
server:
  port: 6001
  http_port: 8082
  monitor_port: 5436
storage:
  engine: page
  data_dir: /data1
`
	yaml2 := `
server:
  port: 6002
  http_port: 8083
  monitor_port: 5437
storage:
  engine: json
  data_dir: /data2
`
	dir := t.TempDir()
	path := filepath.Join(dir, "vaultdb.yaml")

	if err := os.WriteFile(path, []byte(yaml1), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg1, err := Reload(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg1.Server.Port != 6001 {
		t.Fatalf("first reload port = %d", cfg1.Server.Port)
	}

	if err := os.WriteFile(path, []byte(yaml2), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Reload(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Server.Port != 6002 {
		t.Fatalf("second reload port = %d", cfg2.Server.Port)
	}
	if cfg2.Storage.Engine != "json" {
		t.Fatalf("second reload engine = %q", cfg2.Storage.Engine)
	}
}

func TestReload_EmptyPath(t *testing.T) {
	cfg, err := Reload("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 5432 {
		t.Fatalf("reload with empty path should return defaults, port = %d", cfg.Server.Port)
	}
}

func TestReload_InvalidFile(t *testing.T) {
	_, err := Reload("/nonexistent/vaultdb.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReload_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: [invalid yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Reload(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestReload_ValidationError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	yaml := `
storage:
  engine: json
  data_dir: ""
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Reload(path)
	if err == nil {
		t.Fatal("expected error for empty data_dir")
	}
}

// --- Validation tests ---

func TestValidation_PortRange(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"port negative", "server:\n  port: -1\n  http_port: 8081\n  monitor_port: 5433\nstorage:\n  data_dir: /d\n"},
		{"port too large", "server:\n  port: 65536\n  http_port: 8081\n  monitor_port: 5433\nstorage:\n  data_dir: /d\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "val.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidation_PortConflict(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			"port == http_port",
			"server:\n  port: 8080\n  http_port: 8080\n  monitor_port: 5433\nstorage:\n  data_dir: /d\n",
		},
		{
			"port == monitor_port",
			"server:\n  port: 5433\n  http_port: 8081\n  monitor_port: 5433\nstorage:\n  data_dir: /d\n",
		},
		{
			"http_port == monitor_port",
			"server:\n  port: 5432\n  http_port: 8080\n  monitor_port: 8080\nstorage:\n  data_dir: /d\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conflict.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected port conflict error")
			}
		})
	}
}

func TestValidation_MissingDataDir(t *testing.T) {
	yaml := `
storage:
  engine: page
  data_dir: ""
`
	path := filepath.Join(t.TempDir(), "nodd.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty data_dir")
	}
}

func TestValidation_NegativeQueryTimeout(t *testing.T) {
	yaml := `
server:
  query_timeout_sec: -1
storage:
  data_dir: /d
`
	path := filepath.Join(t.TempDir(), "neg.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for negative query_timeout_sec")
	}
}

func TestValidation_BadDropPolicy(t *testing.T) {
	yaml := `
server:
  live_queries:
    drop_policy: invalid
storage:
  data_dir: /d
`
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid drop_policy")
	}
}

func TestValidation_BadEngine(t *testing.T) {
	yaml := `
storage:
  engine: redis
  data_dir: /d
`
	path := filepath.Join(t.TempDir(), "engine.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid engine")
	}
}

func TestValidation_ValidPolicies(t *testing.T) {
	for _, policy := range []string{"drop", "block", "evict"} {
		t.Run(policy, func(t *testing.T) {
			yaml := `
server:
  live_queries:
    drop_policy: ` + policy + `
storage:
  data_dir: /d
`
			path := filepath.Join(t.TempDir(), "ok.yaml")
			if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err != nil {
				t.Fatalf("policy %q should be valid: %v", policy, err)
			}
		})
	}
}

func TestValidation_ValidEngines(t *testing.T) {
	for _, engine := range []string{"page", "json"} {
		t.Run(engine, func(t *testing.T) {
			yaml := `
storage:
  engine: ` + engine + `
  data_dir: /d
`
			path := filepath.Join(t.TempDir(), "eng.yaml")
			if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err != nil {
				t.Fatalf("engine %q should be valid: %v", engine, err)
			}
		})
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoad_DefaultsWithEmptyPath(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Fatalf("default host = %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 5432 {
		t.Fatalf("default port = %d", cfg.Server.Port)
	}
	if cfg.Server.HTTPPort != 8080 {
		t.Fatalf("default http_port = %d", cfg.Server.HTTPPort)
	}
	if cfg.Server.MonitorPort != 5433 {
		t.Fatalf("default monitor_port = %d", cfg.Server.MonitorPort)
	}
	if cfg.Server.MaxRows != DefaultMaxRows {
		t.Fatalf("default max_rows = %d", cfg.Server.MaxRows)
	}
	if cfg.Server.QueryTimeoutSec != DefaultQueryTimeoutSec {
		t.Fatalf("default query_timeout_sec = %d", cfg.Server.QueryTimeoutSec)
	}
	if cfg.Server.MaxConnections != DefaultMaxConnections {
		t.Fatalf("default max_connections = %d", cfg.Server.MaxConnections)
	}
	if cfg.Server.ShutdownTimeoutSec != DefaultShutdownTimeoutSec {
		t.Fatalf("default shutdown_timeout_sec = %d", cfg.Server.ShutdownTimeoutSec)
	}
	if cfg.Server.TCPKeepAliveSec != DefaultTCPKeepAliveSec {
		t.Fatalf("default tcp_keepalive_sec = %d", cfg.Server.TCPKeepAliveSec)
	}
	if cfg.Server.TCPIdleTimeoutSec != DefaultTCPIdleTimeoutSec {
		t.Fatalf("default tcp_idle_timeout_sec = %d", cfg.Server.TCPIdleTimeoutSec)
	}
	if cfg.Server.MaxPreparedStmts != DefaultMaxPreparedStmts {
		t.Fatalf("default max_prepared_statements = %d", cfg.Server.MaxPreparedStmts)
	}
	if cfg.Server.RateLimitRPS != DefaultRateLimitRPS {
		t.Fatalf("default rate_limit_rps = %d", cfg.Server.RateLimitRPS)
	}
	if cfg.Server.RateLimitBurst != DefaultRateLimitBurst {
		t.Fatalf("default rate_limit_burst = %d", cfg.Server.RateLimitBurst)
	}
	if cfg.Server.LiveQueries.BufferSize != DefaultLiveQueryBuffer {
		t.Fatalf("default buffer_size = %d", cfg.Server.LiveQueries.BufferSize)
	}
	if cfg.Server.LiveQueries.BlockTimeoutS != DefaultLiveQueryBlockTimeout {
		t.Fatalf("default block_timeout_s = %d", cfg.Server.LiveQueries.BlockTimeoutS)
	}
	if cfg.Storage.ResultCacheSize != DefaultResultCacheSize {
		t.Fatalf("default result_cache_size = %d", cfg.Storage.ResultCacheSize)
	}
	if cfg.Storage.ResultCacheTTL_s != DefaultResultCacheTTL {
		t.Fatalf("default result_cache_ttl = %d", cfg.Storage.ResultCacheTTL_s)
	}
	if cfg.Auth.RateWindowSec != DefaultAuthRateWindowSec {
		t.Fatalf("default rate_window_seconds = %d", cfg.Auth.RateWindowSec)
	}
	if cfg.Auth.MaxFails != DefaultAuthMaxFails {
		t.Fatalf("default max_fails = %d", cfg.Auth.MaxFails)
	}
	if cfg.Auth.BlockForSec != DefaultAuthBlockForSec {
		t.Fatalf("default block_for_seconds = %d", cfg.Auth.BlockForSec)
	}
}
