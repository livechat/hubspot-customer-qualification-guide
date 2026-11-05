package identity

import (
	"net/mail"
	"strings"
	"unicode"
)

// Normalize accepts plain email addresses, never display-name or address-list syntax.
// This example treats casing as insignificant and does not resolve email aliases.
func Normalize(raw string) (string, bool) {
	value := strings.TrimSpace(raw)

	if len(value) == 0 || len(value) > 255 || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", false
	}

	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || parsed.Name != "" || strings.ContainsAny(value, "<>,;\r\n") {
		return "", false
	}

	return strings.ToLower(value), true
}
