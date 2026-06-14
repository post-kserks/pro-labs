// Package config загружает vaultdb.yaml.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// LiveQueriesConfig управляет поведением Live Queries при медленных клиентах.
type LiveQueriesConfig struct {
	BufferSize    int    `yaml:"buffer_size"`
	DropPolicy    string `yaml:"drop_policy"`
	BlockTimeoutS int    `yaml:"block_timeout_s"`
}

// ServerConfig — сетевые параметры сервера.
type ServerConfig struct {
	Host                string          `yaml:"host"`
	Port                int             `yaml:"port"`
	HTTPPort            int             `yaml:"http_port"`
	MonitorPort         int             `yaml:"monitor_port"`
	MaxRequestSizeBytes int             `yaml:"max_request_size_bytes"`
	AllowedOrigins      []string        `yaml:"allowed_origins"`
	LiveQueries         LiveQueriesConfig `yaml:"live_queries"`
	QueryTimeoutSec     int             `yaml:"query_timeout_sec"`
	MaxConnections      int             `yaml:"max_connections"`
	ShutdownTimeoutSec  int             `yaml:"shutdown_timeout_sec"`
}

// StorageConfig — параметры хранилища.
type StorageConfig struct {
	Engine  string `yaml:"engine"`
	DataDir string `yaml:"data_dir"`
}

// AuthConfig — параметры аутентификации.
type AuthConfig struct {
	Enabled bool `yaml:"enabled"`
}

// AIConfig — параметры внешнего embedding-провайдера для SEMANTIC_MATCH/AI_EMBED.
type AIConfig struct {
	Provider string `yaml:"provider"`
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key"`
}

// Config — корневая конфигурация vaultdb.yaml.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
	Auth    AuthConfig    `yaml:"auth"`
	AI      AIConfig      `yaml:"ai"`
}

const (
	DefaultMaxRequestSize        = 64 * 1024 * 1024 // 64 МБ
	DefaultLiveQueryBuffer       = 256
	DefaultLiveQueryPolicy       = "drop"
	DefaultLiveQueryBlockTimeout = 5
	DefaultQueryTimeoutSec       = 30
	DefaultMaxConnections        = 1000
	DefaultShutdownTimeoutSec    = 30
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
			QueryTimeoutSec:     DefaultQueryTimeoutSec,
			MaxConnections:      DefaultMaxConnections,
			ShutdownTimeoutSec:  DefaultShutdownTimeoutSec,
			LiveQueries: LiveQueriesConfig{
				BufferSize:    DefaultLiveQueryBuffer,
				DropPolicy:    DefaultLiveQueryPolicy,
				BlockTimeoutS: DefaultLiveQueryBlockTimeout,
			},
		},
		Storage: StorageConfig{
			Engine:  "json",
			DataDir: "./data",
		},
		Auth: AuthConfig{Enabled: true},
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
		cfg.Storage.Engine = "json"
	}
	if cfg.Storage.DataDir == "" {
		return fmt.Errorf("storage.data_dir must not be empty")
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
		} else {
			cfg.Server.Port = n
		}
	}
	if v := os.Getenv("VAULTDB_HTTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			slog.Warn("invalid VAULTDB_HTTP_PORT, ignoring", "value", v, "error", err)
		} else {
			cfg.Server.HTTPPort = n
		}
	}
	if v := os.Getenv("VAULTDB_MONITOR_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err != nil {
			slog.Warn("invalid VAULTDB_MONITOR_PORT, ignoring", "value", v, "error", err)
		} else {
			cfg.Server.MonitorPort = n
		}
	}
	if v := os.Getenv("VAULTDB_LOG_LEVEL"); v != "" {
		// Logging config handled separately
	}
	if v := os.Getenv("VAULTDB_DATA_DIR"); v != "" {
		cfg.Storage.DataDir = v
	}
	if v := os.Getenv("VAULTDB_AI_API_KEY"); v != "" {
		cfg.AI.APIKey = v
	}
}

// Reload перезагружает конфигурацию из файла.
// Возвращает ошибку если конфиг невалиден.
func Reload(path string) (*Config, error) {
	return Load(path)
}
