// Package config загружает vaultdb.yaml.
//
// Используется собственный парсер минимального подмножества YAML
// (вложенные словари через отступы, скалярные значения), чтобы не тянуть
// внешние зависимости: проект сознательно держится на stdlib.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LiveQueriesConfig управляет поведением Live Queries при медленных клиентах.
type LiveQueriesConfig struct {
	BufferSize    int    // размер буфера канала подписки
	DropPolicy    string // "drop" | "block" | "evict"
	BlockTimeoutS int    // таймаут для policy=block, секунды
}

// ServerConfig — сетевые параметры сервера.
type ServerConfig struct {
	Host                string
	Port                int
	HTTPPort            int
	MonitorPort         int
	MaxRequestSizeBytes int
	LiveQueries         LiveQueriesConfig
}

// StorageConfig — параметры хранилища.
type StorageConfig struct {
	Engine  string // "json" (по умолчанию) или "page"
	DataDir string
}

// AuthConfig — параметры аутентификации.
type AuthConfig struct {
	Enabled bool
}

// AIConfig — параметры внешнего embedding-провайдера для SEMANTIC_MATCH/AI_EMBED.
type AIConfig struct {
	Provider string // "openai", "ollama" или пусто (AI отключён)
	Endpoint string
	Model    string
	APIKey   string
}

// Config — корневая конфигурация vaultdb.yaml.
type Config struct {
	Server  ServerConfig
	Storage StorageConfig
	Auth    AuthConfig
	AI      AIConfig
}

const (
	DefaultMaxRequestSize        = 64 * 1024 * 1024 // 64 МБ
	DefaultLiveQueryBuffer       = 256
	DefaultLiveQueryPolicy       = "drop"
	DefaultLiveQueryBlockTimeout = 5
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
	if err := cfg.apply(parseYAML(string(data))); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) apply(values map[string]string) error {
	for key, raw := range values {
		switch key {
		case "server.host":
			c.Server.Host = raw
		case "server.port":
			if err := setInt(&c.Server.Port, key, raw); err != nil {
				return err
			}
		case "server.http_port":
			if err := setInt(&c.Server.HTTPPort, key, raw); err != nil {
				return err
			}
		case "server.monitor_port":
			if err := setInt(&c.Server.MonitorPort, key, raw); err != nil {
				return err
			}
		case "server.max_request_size_bytes":
			if err := setInt(&c.Server.MaxRequestSizeBytes, key, raw); err != nil {
				return err
			}
		case "server.live_queries.buffer_size":
			if err := setInt(&c.Server.LiveQueries.BufferSize, key, raw); err != nil {
				return err
			}
		case "server.live_queries.drop_policy":
			switch raw {
			case "drop", "block", "evict":
				c.Server.LiveQueries.DropPolicy = raw
			default:
				return fmt.Errorf("%s: unknown policy %q (want drop|block|evict)", key, raw)
			}
		case "server.live_queries.block_timeout_s":
			if err := setInt(&c.Server.LiveQueries.BlockTimeoutS, key, raw); err != nil {
				return err
			}
		case "storage.engine":
			switch raw {
			case "json", "page":
				c.Storage.Engine = raw
			default:
				return fmt.Errorf("%s: unknown engine %q (want json|page)", key, raw)
			}
		case "storage.data_dir":
			c.Storage.DataDir = raw
		case "auth.enabled":
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("%s: %q is not a boolean", key, raw)
			}
			c.Auth.Enabled = b
		case "ai.provider":
			c.AI.Provider = raw
		case "ai.endpoint":
			c.AI.Endpoint = raw
		case "ai.model":
			c.AI.Model = raw
		case "ai.api_key":
			c.AI.APIKey = raw
		}
	}
	return nil
}

func setInt(dst *int, key, raw string) error {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("%s: %q is not an integer", key, raw)
	}
	*dst = n
	return nil
}

// parseYAML разбирает плоское подмножество YAML: вложенные словари по отступам
// и скалярные значения. Возвращает map с ключами вида "server.port".
func parseYAML(src string) map[string]string {
	values := make(map[string]string)
	// path[i] — имя секции на глубине i; indents[i] — её отступ
	var path []string
	var indents []int

	for _, line := range strings.Split(src, "\n") {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}

		indent := len(trimmed) - len(strings.TrimLeft(trimmed, " "))
		content := strings.TrimSpace(trimmed)

		key, value, found := strings.Cut(content, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)

		// Откатываем путь до уровня текущего отступа
		for len(indents) > 0 && indent <= indents[len(indents)-1] {
			path = path[:len(path)-1]
			indents = indents[:len(indents)-1]
		}

		if value == "" {
			// Начало вложенной секции
			path = append(path, key)
			indents = append(indents, indent)
			continue
		}

		fullKey := key
		if len(path) > 0 {
			fullKey = strings.Join(path, ".") + "." + key
		}
		values[fullKey] = value
	}
	return values
}
