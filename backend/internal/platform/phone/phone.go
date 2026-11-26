// Package phone normalises phone numbers before they are hashed for lookup.
// The number itself is never stored in this form; see keyring.LookupHash.
package phone

import "strings"

// Normalize strips separators so equivalent inputs hash equally. It never
// stores or logs the number itself.
func Normalize(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9', r == '+':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '(', r == ')', r == '.':
			// Separator: ignored.
		}
	}
	return b.String()
}
