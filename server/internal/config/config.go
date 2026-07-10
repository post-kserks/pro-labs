// Package config загружает vaultdb.yaml.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// LiveQueriesConfig управляет поведением Live Queries при медленных клиентах.
type LiveQueriesConfig struct {
	BufferSize    int    `yaml:"buffer_size"`
	DropPolicy    string `yaml:"drop_policy"`
	BlockTimeoutS int    `yaml:"block_timeout_s"`
}

// TLSConfig — параметры TLS.
type TLSConfig struct {
	Enabled      bool   `yaml:"enabled"`
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	MinVersion   string `yaml:"min_version"`   // "1.2" or "1.3"
	Enforce      bool   `yaml:"enforce"`       // reject non-TLS connections
	RedirectHTTP bool   `yaml:"redirect_http"` // auto redirect HTTP to HTTPS
}

// ServerConfig — сетевые параметры сервера.
type ServerConfig struct {
	Host                string            `yaml:"host"`
	Port                int               `yaml:"port"`
	HTTPPort            int               `yaml:"http_port"`
	MonitorPort         int               `yaml:"monitor_port"`
	MaxRequestSizeBytes int               `yaml:"max_request_size_bytes"`
	MaxRows             int               `yaml:"max_rows"`
	AllowedOrigins      []string          `yaml:"allowed_origins"`
	LiveQueries         LiveQueriesConfig `yaml:"live_queries"`
	QueryTimeoutSec     int               `yaml:"query_timeout_sec"`
	MaxConnections      int               `yaml:"max_connections"`
	ShutdownTimeoutSec  int               `yaml:"shutdown_timeout_sec"`
	TCPKeepAliveSec     int               `yaml:"tcp_keepalive_sec"`
	TCPIdleTimeoutSec   int               `yaml:"tcp_idle_timeout_sec"`
	MaxPreparedStmts    int               `yaml:"max_prepared_statements"`
	RateLimitRPS        int               `yaml:"rate_limit_rps"`
	RateLimitBurst      int               `yaml:"rate_limit_burst"`
	TLS                 TLSConfig         `yaml:"tls"`
}

// StorageConfig — параметры хранилища.
type StorageConfig struct {
	Engine           string `yaml:"engine"`
	DataDir          string `yaml:"data_dir"`
	ResultCacheSize  int    `yaml:"result_cache_size"`
	ResultCacheTTL_s int    `yaml:"result_cache_ttl_seconds"`
	BufferPoolPages  int    `yaml:"buffer_pool_pages"`
}

// AuthConfig — параметры аутентификации.
type AuthConfig struct {
	Enabled            bool   `yaml:"enabled"`
	MTLSEnabled        bool   `yaml:"mtls_enabled"`
	MTLScaFile         string `yaml:"mtls_ca_file"`
	RateWindowSec      int    `yaml:"rate_window_seconds"`
	MaxFails           int    `yaml:"max_fails"`
	BlockForSec        int    `yaml:"block_for_seconds"`
	LocalhostBypass    bool   `yaml:"localhost_bypass"`
	RequireTLSForToken bool   `yaml:"require_tls_for_token"`
}

// AIConfig — параметры внешнего embedding-провайдера для SEMANTIC_MATCH/AI_EMBED.
type AIConfig struct {
	Provider     string `yaml:"provider"`
	Endpoint     string `yaml:"endpoint"`
	Model        string `yaml:"model"`
	APIKey       string `yaml:"api_key"`
	CacheEnabled bool   `yaml:"cache_enabled"`
	CacheSize    int    `yaml:"cache_size"`
}

// EncryptionConfig — параметры Transparent Data Encryption (TDE).
type EncryptionConfig struct {
	Enabled        bool   `yaml:"enabled"`
	KeySource      string `yaml:"key_source"`    // passphrase | os_keychain | kms
	DefaultScope   string `yaml:"default_scope"` // all | tables_only | off
	EncryptCatalog bool   `yaml:"encrypt_catalog"`
	EncryptWAL     bool   `yaml:"encrypt_wal"`
}

// AuditConfig — параметры журналирования аудита.
type AuditConfig struct {
	ArchivePath       string `yaml:"archive_path"`
	ArchiveKeepCount  int    `yaml:"archive_keep_count"`
	VerifyIntervalSec int    `yaml:"verify_interval_sec"`
}

// Config — корневая конфигурация vaultdb.yaml.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Storage    StorageConfig    `yaml:"storage"`
	Auth       AuthConfig       `yaml:"auth"`
	AI         AIConfig         `yaml:"ai"`
	Encryption EncryptionConfig `yaml:"encryption"`
	Audit      AuditConfig      `yaml:"audit"`
}

const (
	DefaultMaxRequestSize         = 64 * 1024 * 1024 // 64 МБ
	DefaultLiveQueryBuffer        = 256
	DefaultLiveQueryPolicy        = "drop"
	DefaultLiveQueryBlockTimeout  = 5
	DefaultQueryTimeoutSec        = 30
	DefaultMaxConnections         = 1000
	DefaultShutdownTimeoutSec     = 30
	DefaultMaxRows                = 1000000
	DefaultTCPKeepAliveSec        = 30
	DefaultTCPIdleTimeoutSec      = 300
	DefaultMaxPreparedStmts       = 1000
	DefaultResultCacheSize        = 256
	DefaultResultCacheTTL         = 30
	DefaultRateLimitRPS           = 100
	DefaultRateLimitBurst         = 200
	DefaultBufferPoolPages        = 16384 // 128MB with 8KB pages
	DefaultAuthRateWindowSec      = 60
	DefaultAuthMaxFails           = 10
	DefaultAuthBlockForSec        = 300
	DefaultAuditVerifyIntervalSec = 300 // 5 minutes
)

// Default возвращает конфигурацию со значениями по умолчанию.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Host:                "127.0.0.1",
			Port:                5432,
			HTTPPort:            8080,
			MonitorPort:         5433,
			MaxRequestSizeBytes: DefaultMaxRequestSize,
			MaxRows:             DefaultMaxRows,
			QueryTimeoutSec:     DefaultQueryTimeoutSec,
			MaxConnections:      DefaultMaxConnections,
			ShutdownTimeoutSec:  DefaultShutdownTimeoutSec,
			TCPKeepAliveSec:     DefaultTCPKeepAliveSec,
			TCPIdleTimeoutSec:   DefaultTCPIdleTimeoutSec,
			MaxPreparedStmts:    DefaultMaxPreparedStmts,
			RateLimitRPS:        DefaultRateLimitRPS,
			RateLimitBurst:      DefaultRateLimitBurst,
			LiveQueries: LiveQueriesConfig{
				BufferSize:    DefaultLiveQueryBuffer,
				DropPolicy:    DefaultLiveQueryPolicy,
				BlockTimeoutS: DefaultLiveQueryBlockTimeout,
			},
		},
		Storage: StorageConfig{
			Engine:           "page",
			DataDir:          "./data",
			ResultCacheSize:  DefaultResultCacheSize,
			ResultCacheTTL_s: DefaultResultCacheTTL,
			BufferPoolPages:  DefaultBufferPoolPages,
		},
		Auth: AuthConfig{
			Enabled:         true,
			RateWindowSec:   DefaultAuthRateWindowSec,
			MaxFails:        DefaultAuthMaxFails,
			BlockForSec:     DefaultAuthBlockForSec,
			LocalhostBypass: true,
		},
		Audit: AuditConfig{
			VerifyIntervalSec: DefaultAuditVerifyIntervalSec,
		},
	}
}

// Load читает конфигурацию из файла. Отсутствующие ключи получают значения
// по умолчанию. Если path пустой — возвращаются значения по умолчанию.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("validate config %s: %w", path, err)
	}

	return cfg, nil
}

func validateConfig(cfg *Config) error {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 5432
	}
	if cfg.Server.HTTPPort == 0 {
		cfg.Server.HTTPPort = 8080
	}
	if cfg.Server.MonitorPort == 0 {
		cfg.Server.MonitorPort = 5433
	}
	if cfg.Server.MaxRequestSizeBytes == 0 {
		cfg.Server.MaxRequestSizeBytes = DefaultMaxRequestSize
	}
	if cfg.Server.MaxRows == 0 {
		cfg.Server.MaxRows = DefaultMaxRows
	}
	if cfg.Server.QueryTimeoutSec == 0 {
		cfg.Server.QueryTimeoutSec = DefaultQueryTimeoutSec
	}
	if cfg.Server.MaxConnections == 0 {
		cfg.Server.MaxConnections = DefaultMaxConnections
	}
	if cfg.Server.ShutdownTimeoutSec == 0 {
		cfg.Server.ShutdownTimeoutSec = DefaultShutdownTimeoutSec
	}
	if cfg.Server.LiveQueries.BufferSize == 0 {
		cfg.Server.LiveQueries.BufferSize = DefaultLiveQueryBuffer
	}
	if cfg.Server.LiveQueries.DropPolicy == "" {
		cfg.Server.LiveQueries.DropPolicy = DefaultLiveQueryPolicy
	}
	if cfg.Server.LiveQueries.BlockTimeoutS == 0 {
		cfg.Server.LiveQueries.BlockTimeoutS = DefaultLiveQueryBlockTimeout
	}
	if cfg.Storage.Engine == "" {
		cfg.Storage.Engine = "page"
	}
	if cfg.Storage.DataDir == "" {
		return fmt.Errorf("storage.data_dir must not be empty")
	}
	if cfg.Storage.ResultCacheSize == 0 {
		cfg.Storage.ResultCacheSize = DefaultResultCacheSize
	}
	if cfg.Storage.ResultCacheTTL_s == 0 {
		cfg.Storage.ResultCacheTTL_s = DefaultResultCacheTTL
	}
	if cfg.Storage.BufferPoolPages == 0 {
		cfg.Storage.BufferPoolPages = DefaultBufferPoolPages
	}
	if cfg.Server.TCPKeepAliveSec == 0 {
		cfg.Server.TCPKeepAliveSec = DefaultTCPKeepAliveSec
	}
	if cfg.Server.TCPIdleTimeoutSec == 0 {
		cfg.Server.TCPIdleTimeoutSec = DefaultTCPIdleTimeoutSec
	}
	if cfg.Server.MaxPreparedStmts == 0 {
		cfg.Server.MaxPreparedStmts = DefaultMaxPreparedStmts
	}
	if cfg.Server.RateLimitRPS == 0 {
		cfg.Server.RateLimitRPS = DefaultRateLimitRPS
	}
	if cfg.Server.RateLimitBurst == 0 {
		cfg.Server.RateLimitBurst = DefaultRateLimitBurst
	}
	// TLS validation
	if cfg.Server.TLS.Enforce && !cfg.Server.TLS.Enabled {
		return fmt.Errorf("tls.enforce is true but tls.enabled is false: TLS must be enabled to enforce it")
	}
	if cfg.Server.TLS.Enabled {
		if cfg.Server.TLS.CertFile == "" {
			return fmt.Errorf("tls.cert_file must not be empty when tls.enabled is true")
		}
		if cfg.Server.TLS.KeyFile == "" {
			return fmt.Errorf("tls.key_file must not be empty when tls.enabled is true")
		}
	} else {
		// Warning when TLS is disabled
		fmt.Fprintln(os.Stderr, "WARNING: TLS is disabled — server is running on plain HTTP. Enable tls.enabled in production.")
	}
	if cfg.Server.TLS.MinVersion != "" && cfg.Server.TLS.MinVersion != "1.2" && cfg.Server.TLS.MinVersion != "1.3" {
		return fmt.Errorf("unknown tls.min_version %q (want 1.2 or 1.3)", cfg.Server.TLS.MinVersion)
	}
	if cfg.Auth.RateWindowSec == 0 {
		cfg.Auth.RateWindowSec = DefaultAuthRateWindowSec
	}
	if cfg.Auth.MaxFails == 0 {
		cfg.Auth.MaxFails = DefaultAuthMaxFails
	}
	if cfg.Auth.BlockForSec == 0 {
		cfg.Auth.BlockForSec = DefaultAuthBlockForSec
	}
	if cfg.Encryption.KeySource == "" {
		cfg.Encryption.KeySource = "passphrase"
	}
	if cfg.Encryption.DefaultScope == "" {
		cfg.Encryption.DefaultScope = "all"
	}
	if cfg.Encryption.KeySource != "passphrase" &&
		cfg.Encryption.KeySource != "os_keychain" &&
		cfg.Encryption.KeySource != "kms" {
		return fmt.Errorf("unknown encryption.key_source %q (want passphrase|os_keychain|kms)", cfg.Encryption.KeySource)
	}
	if cfg.Encryption.DefaultScope != "all" &&
		cfg.Encryption.DefaultScope != "tables_only" &&
		cfg.Encryption.DefaultScope != "off" {
		return fmt.Errorf("unknown encryption.default_scope %q (want all|tables_only|off)", cfg.Encryption.DefaultScope)
	}
	if cfg.Audit.VerifyIntervalSec == 0 {
		cfg.Audit.VerifyIntervalSec = DefaultAuditVerifyIntervalSec
	}
	if cfg.Audit.VerifyIntervalSec < 10 {
		return fmt.Errorf("audit.verify_interval_sec must be >= 10, got %d", cfg.Audit.VerifyIntervalSec)
	}

	// Warn when localhost auth bypass is enabled (default: true).
	if cfg.Auth.LocalhostBypass && cfg.Auth.Enabled {
		fmt.Fprintln(os.Stderr, "WARNING: localhost auth bypass is enabled — disable in production (auth.localhost_bypass: false)")
	}

	// Validate port ranges
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", cfg.Server.Port)
	}
	if cfg.Server.HTTPPort < 1 || cfg.Server.HTTPPort > 65535 {
		return fmt.Errorf("server.http_port must be between 1 and 65535, got %d", cfg.Server.HTTPPort)
	}
	if cfg.Server.MonitorPort < 1 || cfg.Server.MonitorPort > 65535 {
		return fmt.Errorf("server.monitor_port must be between 1 and 65535, got %d", cfg.Server.MonitorPort)
	}

	// Validate port conflicts
	if cfg.Server.Port == cfg.Server.HTTPPort {
		return fmt.Errorf("server.port and server.http_port must not be the same (%d)", cfg.Server.Port)
	}
	if cfg.Server.Port == cfg.Server.MonitorPort {
		return fmt.Errorf("server.port and server.monitor_port must not be the same (%d)", cfg.Server.Port)
	}
	if cfg.Server.HTTPPort == cfg.Server.MonitorPort {
		return fmt.Errorf("server.http_port and server.monitor_port must not be the same (%d)", cfg.Server.HTTPPort)
	}

	// Validate QueryTimeoutSec
	if cfg.Server.QueryTimeoutSec < 0 {
		return fmt.Errorf("server.query_timeout_sec must not be negative, got %d", cfg.Server.QueryTimeoutSec)
	}

	// Validate known values
	if cfg.Server.LiveQueries.DropPolicy != "drop" &&
		cfg.Server.LiveQueries.DropPolicy != "block" &&
		cfg.Server.LiveQueries.DropPolicy != "evict" {
		return fmt.Errorf("unknown drop_policy %q (want drop|block|evict)", cfg.Server.LiveQueries.DropPolicy)
	}
	if cfg.Storage.Engine != "json" && cfg.Storage.Engine != "page" {
		return fmt.Errorf("unknown engine %q (want json|page)", cfg.Storage.Engine)
	}

	return nil
}

// ApplyEnvOverrides применяет переменные окружения, перекрывая значения из файла.
func ApplyEnvOverrides(cfg *Config) {
	if v := os.Getenv("VAULTDB_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("VAULTDB_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			slog.Warn("invalid VAULTDB_PORT, ignoring", "value", v, "error", err)
		} else if n < 1 || n > 65535 {
			slog.Warn("VAULTDB_PORT out of range (1-65535), ignoring", "value", v)
		} else {
			cfg.Server.Port = n
		}
	}
	if v := os.Getenv("VAULTDB_HTTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			slog.Warn("invalid VAULTDB_HTTP_PORT, ignoring", "value", v, "error", err)
		} else if n < 1 || n > 65535 {
			slog.Warn("VAULTDB_HTTP_PORT out of range (1-65535), ignoring", "value", v)
		} else {
			cfg.Server.HTTPPort = n
		}
	}
	if v := os.Getenv("VAULTDB_MONITOR_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			slog.Warn("invalid VAULTDB_MONITOR_PORT, ignoring", "value", v, "error", err)
		} else if n < 1 || n > 65535 {
			slog.Warn("VAULTDB_MONITOR_PORT out of range (1-65535), ignoring", "value", v)
		} else {
			cfg.Server.MonitorPort = n
		}
	}
	if v := os.Getenv("VAULTDB_LOG_LEVEL"); v != "" {
		_ = v // Logging config handled separately in main.go
	}
	if v := os.Getenv("VAULTDB_DATA_DIR"); v != "" {
		cfg.Storage.DataDir = v
	}
	if v := os.Getenv("VAULTDB_BUFFER_POOL_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			slog.Warn("invalid VAULTDB_BUFFER_POOL_PAGES, ignoring", "value", v, "error", err)
		} else if n < 64 {
			slog.Warn("VAULTDB_BUFFER_POOL_PAGES too small (min 64), ignoring", "value", v)
		} else {
			cfg.Storage.BufferPoolPages = n
		}
	}
	if v := os.Getenv("VAULTDB_MTLS_ENABLED"); v != "" {
		cfg.Auth.MTLSEnabled = envBoolValue(v)
	}
	if v := os.Getenv("VAULTDB_MTLS_CA_FILE"); v != "" {
		cfg.Auth.MTLScaFile = v
	}
	if v := os.Getenv("VAULTDB_AI_API_KEY"); v != "" {
		cfg.AI.APIKey = v
	}
}

func envBoolValue(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Reload перезагружает конфигурацию из файла.
// Возвращает ошибку если конфиг невалиден.
func Reload(path string) (*Config, error) {
	return Load(path)
}
