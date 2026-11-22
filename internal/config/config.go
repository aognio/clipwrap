// Package config loads and validates clipwrapd TOML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds the complete daemon configuration.
type Config struct {
	Server     ServerConfig     `toml:"server"`
	Ingest     IngestConfig     `toml:"ingest"`
	History    HistoryConfig    `toml:"history"`
	Web        WebConfig        `toml:"web"`
	Auth       AuthConfig       `toml:"auth"`
	WebSocket  WebSocketConfig  `toml:"websocket"`
	Logging    LoggingConfig    `toml:"logging"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Listen       string        `toml:"listen"`
	PublicURL    string        `toml:"public_url"`
	ReadTimeout  time.Duration `toml:"read_timeout"`
	WriteTimeout time.Duration `toml:"write_timeout"`
	IdleTimeout  time.Duration `toml:"idle_timeout"`
}

// IngestConfig holds Unix socket ingestion settings.
type IngestConfig struct {
	Socket        string `toml:"socket"`
	MaxItemBytes  int    `toml:"max_item_bytes"`
	MaxConnections int   `toml:"max_connections"`
}

// HistoryConfig holds event store settings.
type HistoryConfig struct {
	Capacity int `toml:"capacity"`
}

// WebConfig holds web dashboard settings.
type WebConfig struct {
	Enabled   bool          `toml:"enabled"`
	SessionTTL time.Duration `toml:"session_ttl"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	Username     string `toml:"username"`
	PasswordHash string `toml:"password_hash"`
	ServiceToken string `toml:"service_token"`
}

// WebSocketConfig holds WebSocket settings.
type WebSocketConfig struct {
	PingInterval time.Duration `toml:"ping_interval"`
	PongTimeout  time.Duration `toml:"pong_timeout"`
	MaxClients   int           `toml:"max_clients"`
	ReplayLimit  int           `toml:"replay_limit"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Listen:       "127.0.0.1:8765",
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		Ingest: IngestConfig{
			Socket:        "/run/clipwrapd/clipwrapd.sock",
			MaxItemBytes:  1048576,
			MaxConnections: 64,
		},
		History: HistoryConfig{
			Capacity: 500,
		},
		Web: WebConfig{
			Enabled:    true,
			SessionTTL: 12 * time.Hour,
		},
		Auth: AuthConfig{
			Username: "admin",
		},
		WebSocket: WebSocketConfig{
			PingInterval: 30 * time.Second,
			PongTimeout:  60 * time.Second,
			MaxClients:   32,
			ReplayLimit:  500,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

// Load reads and validates the configuration from the given file path.
func Load(path string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// Validate checks the configuration for errors.
func (c Config) Validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if c.Ingest.Socket == "" {
		return fmt.Errorf("ingest.socket is required")
	}
	if c.Ingest.MaxItemBytes <= 0 {
		return fmt.Errorf("ingest.max_item_bytes must be positive")
	}
	if c.Ingest.MaxConnections <= 0 {
		return fmt.Errorf("ingest.max_connections must be positive")
	}
	if c.History.Capacity <= 0 {
		return fmt.Errorf("history.capacity must be positive")
	}
	if c.Auth.Username != "admin" {
		return fmt.Errorf("auth.username must be 'admin'")
	}
	if c.Web.SessionTTL <= 0 {
		return fmt.Errorf("web.session_ttl must be positive")
	}
	if c.WebSocket.PingInterval <= 0 {
		return fmt.Errorf("websocket.ping_interval must be positive")
	}
	if c.WebSocket.PongTimeout <= 0 {
		return fmt.Errorf("websocket.pong_timeout must be positive")
	}
	if c.WebSocket.MaxClients <= 0 {
		return fmt.Errorf("websocket.max_clients must be positive")
	}
	if c.Logging.Level != "" && c.Logging.Level != "debug" && c.Logging.Level != "info" && c.Logging.Level != "warn" && c.Logging.Level != "error" {
		return fmt.Errorf("logging.level must be one of: debug, info, warn, error")
	}
	if c.Logging.Format != "" && c.Logging.Format != "text" && c.Logging.Format != "json" {
		return fmt.Errorf("logging.format must be one of: text, json")
	}
	return nil
}

// Save writes the configuration to the given file path atomically.
func Save(path string, cfg Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Write to a temp file and rename for atomicity
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open temp file: %w", err)
	}

	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encode config: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename config: %w", err)
	}

	return nil
}

// HasPassword returns true if a password hash is configured.
func (c Config) HasPassword() bool {
	return c.Auth.PasswordHash != ""
}

// ResolveConfigPath finds the configuration file.
// It checks the given path first, then the default locations.
func ResolveConfigPath(givenPath string) string {
	if givenPath != "" {
		return givenPath
	}
	// Check default locations in order
	paths := []string{
		"./clipwrap-server.toml",
		"/etc/clipwrapd/clipwrap-server.toml",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/etc/clipwrapd/clipwrap-server.toml"
}
