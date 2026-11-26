package config

import (
	"strings"
	"testing"
	"time"
)

// setRequired sets the values that have no safe default.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://******@db:5432/pamoja?sslmode=disable")
	t.Setenv("AUTH_TOKEN_SECRET", "test-token-secret-0123456789abcdef")
	t.Setenv("PAMOJA_LOOKUP_KEY", "test-lookup-key-0123456789abcdef")
	t.Setenv("PAMOJA_MASTER_KEY", "test-master-key-0123456789abcdef")
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	setRequired(t)
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("expected missing DATABASE_URL error, got %v", err)
	}
}

func TestLoadRequiresKeyMaterial(t *testing.T) {
	for _, key := range []string{"AUTH_TOKEN_SECRET", "PAMOJA_LOOKUP_KEY", "PAMOJA_MASTER_KEY"} {
		t.Run(key, func(t *testing.T) {
			setRequired(t)
			t.Setenv(key, "")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), key+" is required") {
				t.Fatalf("expected %s required error, got %v", key, err)
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
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
	if cfg.Auth.AccessTTL != 15*time.Minute {
		t.Errorf("AccessTTL = %v, want 15m", cfg.Auth.AccessTTL)
	}
	if cfg.Auth.RefreshTTL != 14*24*time.Hour {
		t.Errorf("RefreshTTL = %v, want 336h", cfg.Auth.RefreshTTL)
	}
	if cfg.Auth.LockoutFailures != 5 || cfg.Auth.MaxDevices != 5 {
		t.Errorf("lockout/device defaults = %d/%d, want 5/5", cfg.Auth.LockoutFailures, cfg.Auth.MaxDevices)
	}
	if cfg.Auth.Issuer != "pamoja-api" || cfg.Auth.Audience != "pamoja-client" {
		t.Errorf("issuer/audience = %q/%q", cfg.Auth.Issuer, cfg.Auth.Audience)
	}
}

func TestLoadRejectsBadPortAndEnv(t *testing.T) {
	setRequired(t)

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

func TestLoadRejectsBadAuthValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"access ttl over 15m", "AUTH_ACCESS_TTL", "16m", "AUTH_ACCESS_TTL"},
		{"access ttl zero", "AUTH_ACCESS_TTL", "0s", "AUTH_ACCESS_TTL"},
		{"refresh shorter than access", "AUTH_REFRESH_TTL", "5m", "AUTH_REFRESH_TTL"},
		{"bad duration", "AUTH_IDLE_STAFF", "soon", "AUTH_IDLE_STAFF"},
		{"bad int", "AUTH_MAX_DEVICES", "many", "AUTH_MAX_DEVICES"},
		{"zero devices", "AUTH_MAX_DEVICES", "0", "AUTH_MAX_DEVICES"},
		{"zero lockout failures", "AUTH_LOCKOUT_FAILURES", "0", "AUTH_LOCKOUT_FAILURES"},
		{"lockout max below base", "AUTH_LOCKOUT_MAX", "30s", "AUTH_LOCKOUT_MAX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s error, got %v", tc.want, err)
			}
		})
	}
}
