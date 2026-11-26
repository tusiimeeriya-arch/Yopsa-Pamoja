// Package logging builds the structured JSON logger used by every Pamoja
// process. Log records must never contain personal data or secrets: the
// handler redacts values whose attribute key names a sensitive field.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

const redacted = "[REDACTED]"

// sensitiveKeys are attribute-name fragments that are always redacted.
var sensitiveKeys = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie",
	"pin", "otp", "totp", "recovery_code", "national_id", "nin",
	"phone", "msisdn", "email", "date_of_birth", "dob",
	"database_url", "dsn", "api_key", "private_key",
}

// New returns a JSON slog.Logger at the given level. Unknown levels are
// rejected so a typo cannot silently drop warnings.
func New(level, appEnv string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("logging: unsupported level %q", level)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       lvl,
		ReplaceAttr: redact,
	})
	return slog.New(handler).With("service", "pamoja-api", "env", appEnv), nil
}

func redact(_ []string, a slog.Attr) slog.Attr {
	key := strings.ToLower(a.Key)
	for _, sensitive := range sensitiveKeys {
		if strings.Contains(key, sensitive) {
			return slog.String(a.Key, redacted)
		}
	}
	return a
}
