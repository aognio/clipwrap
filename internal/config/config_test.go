package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8765" {
		t.Errorf("unexpected default listen: %s", cfg.Server.Listen)
	}
	if cfg.History.Capacity != 500 {
		t.Errorf("unexpected default capacity: %d", cfg.History.Capacity)
	}
	if cfg.Auth.Username != "admin" {
		t.Errorf("unexpected default username: %s", cfg.Auth.Username)
	}
}

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `[server]
listen = "127.0.0.1:9999"

[history]
capacity = 100

[auth]
username = "admin"
password_hash = ""
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.Listen != "127.0.0.1:9999" {
		t.Errorf("expected listen 127.0.0.1:9999, got %s", cfg.Server.Listen)
	}
	if cfg.History.Capacity != 100 {
		t.Errorf("expected capacity 100, got %d", cfg.History.Capacity)
	}
	// Defaults should be applied for missing fields
	if cfg.Ingest.MaxItemBytes != 1048576 {
		t.Errorf("expected default max_item_bytes, got %d", cfg.Ingest.MaxItemBytes)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("not [[ valid toml"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestValidateInvalidUsername(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Username = "root"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for non-admin username")
	}
}

func TestValidateMissingListen(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Listen = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing listen address")
	}
}

func TestValidateInvalidLogLevel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Logging.Level = "verbose"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := DefaultConfig()
	cfg.Server.Listen = "0.0.0.0:8080"
	cfg.History.Capacity = 200

	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Server.Listen != "0.0.0.0:8080" {
		t.Errorf("expected listen 0.0.0.0:8080, got %s", loaded.Server.Listen)
	}
	if loaded.History.Capacity != 200 {
		t.Errorf("expected capacity 200, got %d", loaded.History.Capacity)
	}
}

func TestHasPassword(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HasPassword() {
		t.Error("expected no password by default")
	}

	cfg.Auth.PasswordHash = "$argon2id$v=19$m=65536,t=1,p=4$abc$def"
	if !cfg.HasPassword() {
		t.Error("expected password to be set")
	}
}

func TestResolveConfigPath(t *testing.T) {
	// Given path should be returned as-is
	if got := ResolveConfigPath("/custom/path.toml"); got != "/custom/path.toml" {
		t.Errorf("expected /custom/path.toml, got %s", got)
	}

	// Empty path should return default
	got := ResolveConfigPath("")
	if got != "/etc/clipwrapd/clipwrap-server.toml" {
		t.Errorf("expected default path, got %s", got)
	}
}
