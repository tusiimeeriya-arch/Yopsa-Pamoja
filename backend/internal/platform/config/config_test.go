package config

import (
	"strings"
	"testing"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("expected missing DATABASE_URL error, got %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/pamoja?sslmode=disable")
	t.Setenv("APP_ENV", "")
	t.Setenv("PORT", "")
	t.Setenv("LOG_LEVEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppEnv != EnvDevelopment {
		t.Errorf("AppEnv = %q, want development", cfg.AppEnv)
	}
	if cfg.Port != 3000 {
		t.Errorf("Port = %d, want 3000", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
}

func TestLoadRejectsBadPortAndEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/pamoja?sslmode=disable")

	t.Setenv("PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Error("expected error for non-numeric PORT")
	}

	t.Setenv("PORT", "70000")
	if _, err := Load(); err == nil {
		t.Error("expected error for out-of-range PORT")
	}

	t.Setenv("PORT", "3000")
	t.Setenv("APP_ENV", "staging")
	if cfg, err := Load(); err != nil || cfg.AppEnv != EnvStaging {
		t.Fatalf("staging env: cfg=%v err=%v", cfg.AppEnv, err)
	}

	t.Setenv("APP_ENV", "prod-ish")
	if _, err := Load(); err == nil {
		t.Error("expected error for unknown APP_ENV")
	}
}
