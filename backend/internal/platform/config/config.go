// Package config loads and validates the API's runtime configuration from the
// process environment. Every value has a safe default except the database URL,
// which has no safe default and must always be supplied.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env is the deployment environment the process believes it runs in.
type Env string

// Deployment environments. APP_ENV must be exactly one of these.
const (
	EnvDevelopment Env = "development"
	EnvStaging     Env = "staging"
	EnvProduction  Env = "production"
)

// Config is the validated runtime configuration.
type Config struct {
	AppEnv          Env
	Port            int
	DatabaseURL     string
	LogLevel        string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	MaxBodyBytes    int64
}

// Load reads the environment and returns a validated Config.
func Load() (Config, error) {
	cfg := Config{
		AppEnv:          Env(getenv("APP_ENV", string(EnvDevelopment))),
		LogLevel:        strings.ToLower(getenv("LOG_LEVEL", "info")),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    30 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 20 * time.Second,
		MaxBodyBytes:    1 << 20, // 1 MiB; per-route limits may be lower.
	}

	port, err := strconv.Atoi(getenv("PORT", "3000"))
	if err != nil {
		return Config{}, fmt.Errorf("config: PORT must be an integer: %w", err)
	}
	cfg.Port = port

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.AppEnv {
	case EnvDevelopment, EnvStaging, EnvProduction:
	default:
		return fmt.Errorf("config: APP_ENV must be one of development, staging, production (got %q)", c.AppEnv)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("config: PORT must be between 1 and 65535 (got %d)", c.Port)
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: LOG_LEVEL must be one of debug, info, warn, error (got %q)", c.LogLevel)
	}
	if c.MaxBodyBytes < 1024 {
		return fmt.Errorf("config: MAX_BODY_BYTES must be at least 1024")
	}
	return nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
