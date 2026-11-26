// Package domain holds the small cross-cutting pieces every domain service
// shares: typed errors, money validation, idempotency and request hashing.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Errors returned by domain services. The HTTP layer maps each to one generic
// response; internal detail never reaches the caller.
var (
	ErrNotFound            = errors.New("domain: not found")
	ErrConflict            = errors.New("domain: conflict")
	ErrValidation          = errors.New("domain: validation failed")
	ErrForbidden           = errors.New("domain: forbidden")
	ErrStepUpRequired      = errors.New("domain: fresh step-up required")
	ErrIdempotencyConflict = errors.New("domain: idempotency key reused with a different payload")
	ErrMissingIdempotency  = errors.New("domain: Idempotency-Key is required")
	ErrPostingFailed       = errors.New("domain: ledger posting failed")
	ErrInvalidTransition   = errors.New("domain: invalid state transition")
	ErrSeparationOfDuties  = errors.New("domain: separation of duties violated")
	ErrMaterialChange      = errors.New("domain: material change requires re-approval")
)

// ValidationError names the field that failed without echoing its value.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("domain: %s: %s", e.Field, e.Message)
}

// Invalid builds a validation error.
func Invalid(field, message string) error {
	return ValidationError{Field: field, Message: message}
}

// IsValidation reports whether err is a validation error.
func IsValidation(err error) bool {
	var v ValidationError
	return errors.As(err, &v)
}

// PositiveMoney rejects zero, negative and unreasonably large amounts. Money is
// whole UGX in an int64 (ADR-0009).
func PositiveMoney(amount int64) error {
	if amount <= 0 {
		return Invalid("amount_ugx", "must be greater than zero")
	}
	if amount > 1_000_000_000_000 {
		return Invalid("amount_ugx", "is outside the supported range")
	}
	return nil
}

// Required rejects blank strings.
func Required(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return Invalid(field, "is required")
	}
	return nil
}

// HashRequest returns the canonical SHA-256 of method, route and body. The
// idempotency store compares this hash: same key + same hash replays, same key
// + different hash is a 409.
func HashRequest(method, route string, body []byte) string {
	sum := sha256.New()
	sum.Write([]byte(strings.ToUpper(method)))
	sum.Write([]byte{0})
	sum.Write([]byte(route))
	sum.Write([]byte{0})
	sum.Write(body)
	return hex.EncodeToString(sum.Sum(nil))
}
