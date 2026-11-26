// Package config loads and validates the API's runtime configuration from the
// process environment. Every value has a safe default except the database URL
// and the three key materials, which have no safe default and must always be
// supplied.
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

// Auth is the authentication and session policy. Defaults come from the
// proposed values in ASSUMPTIONS.md (A-008, A-009, A-029, A-030, A-036) and are
// overridden per deployment; none of them is a hard-coded business rule.
type Auth struct {
	AccessTTL              time.Duration
	RefreshTTL             time.Duration
	IdleStaff              time.Duration
	IdleSharedPhone        time.Duration
	StepUpWindow           time.Duration
	LockoutFailures        int
	LockoutBase            time.Duration
	LockoutMax             time.Duration
	MaxDevices             int
	LoginPerIPPerMinute    int
	LoginPerAccountPerHour int
	Issuer                 string
	Audience               string
	// Key material. TokenSecret signs access tokens, LookupKey produces the
	// keyed phone hash, MasterKey wraps the envelope encryption key. Each is
	// stretched with SHA-256 before use, so any high-entropy string works.
	TokenSecret string
	LookupKey   string
	MasterKey   string
}

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
	Auth            Auth
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

	auth, err := loadAuth()
	if err != nil {
		return Config{}, err
	}
	cfg.Auth = auth

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadAuth() (Auth, error) {
	a := Auth{
		AccessTTL:              15 * time.Minute,
		RefreshTTL:             14 * 24 * time.Hour,
		IdleStaff:              30 * time.Minute,
		IdleSharedPhone:        5 * time.Minute,
		StepUpWindow:           5 * time.Minute,
		LockoutFailures:        5,
		LockoutBase:            time.Minute,
		LockoutMax:             15 * time.Minute,
		MaxDevices:             5,
		LoginPerIPPerMinute:    5,
		LoginPerAccountPerHour: 10,
		Issuer:                 getenv("AUTH_ISSUER", "pamoja-api"),
		Audience:               getenv("AUTH_AUDIENCE", "pamoja-client"),
		TokenSecret:            os.Getenv("AUTH_TOKEN_SECRET"),
		LookupKey:              os.Getenv("PAMOJA_LOOKUP_KEY"),
		MasterKey:              os.Getenv("PAMOJA_MASTER_KEY"),
	}

	durations := []struct {
		key    string
		target *time.Duration
	}{
		{"AUTH_ACCESS_TTL", &a.AccessTTL},
		{"AUTH_REFRESH_TTL", &a.RefreshTTL},
		{"AUTH_IDLE_STAFF", &a.IdleStaff},
		{"AUTH_IDLE_SHARED_PHONE", &a.IdleSharedPhone},
		{"AUTH_STEP_UP_WINDOW", &a.StepUpWindow},
		{"AUTH_LOCKOUT_BASE", &a.LockoutBase},
		{"AUTH_LOCKOUT_MAX", &a.LockoutMax},
	}
	for _, d := range durations {
		value, err := durationEnv(d.key, *d.target)
		if err != nil {
			return Auth{}, err
		}
		*d.target = value
	}

	ints := []struct {
		key    string
		target *int
	}{
		{"AUTH_LOCKOUT_FAILURES", &a.LockoutFailures},
		{"AUTH_MAX_DEVICES", &a.MaxDevices},
		{"AUTH_LOGIN_PER_IP_PER_MINUTE", &a.LoginPerIPPerMinute},
		{"AUTH_LOGIN_PER_ACCOUNT_PER_HOUR", &a.LoginPerAccountPerHour},
	}
	for _, i := range ints {
		value, err := intEnv(i.key, *i.target)
		if err != nil {
			return Auth{}, err
		}
		*i.target = value
	}

	return a, nil
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
	return c.Auth.validate()
}

func (a Auth) validate() error {
	switch {
	case a.TokenSecret == "":
		return fmt.Errorf("config: AUTH_TOKEN_SECRET is required")
	case a.LookupKey == "":
		return fmt.Errorf("config: PAMOJA_LOOKUP_KEY is required")
	case a.MasterKey == "":
		return fmt.Errorf("config: PAMOJA_MASTER_KEY is required")
	}
	// T-SEC-05: an access token never lives longer than 15 minutes.
	if a.AccessTTL <= 0 || a.AccessTTL > 15*time.Minute {
		return fmt.Errorf("config: AUTH_ACCESS_TTL must be greater than zero and at most 15m")
	}
	if a.RefreshTTL <= a.AccessTTL {
		return fmt.Errorf("config: AUTH_REFRESH_TTL must be longer than AUTH_ACCESS_TTL")
	}
	if a.IdleStaff <= 0 || a.IdleSharedPhone <= 0 {
		return fmt.Errorf("config: AUTH_IDLE_STAFF and AUTH_IDLE_SHARED_PHONE must be positive")
	}
	if a.StepUpWindow <= 0 {
		return fmt.Errorf("config: AUTH_STEP_UP_WINDOW must be positive")
	}
	if a.LockoutFailures < 1 {
		return fmt.Errorf("config: AUTH_LOCKOUT_FAILURES must be at least 1")
	}
	if a.LockoutBase <= 0 || a.LockoutMax < a.LockoutBase {
		return fmt.Errorf("config: AUTH_LOCKOUT_MAX must be at least AUTH_LOCKOUT_BASE, and both positive")
	}
	if a.MaxDevices < 1 {
		return fmt.Errorf("config: AUTH_MAX_DEVICES must be at least 1")
	}
	if a.LoginPerIPPerMinute < 1 || a.LoginPerAccountPerHour < 1 {
		return fmt.Errorf("config: login rate limits must be at least 1")
	}
	if strings.TrimSpace(a.Issuer) == "" || strings.TrimSpace(a.Audience) == "" {
		return fmt.Errorf("config: AUTH_ISSUER and AUTH_AUDIENCE are required")
	}
	return nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a duration such as 15m: %w", key, err)
	}
	return value, nil
}

func intEnv(key string, fallback int) (int, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return value, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
