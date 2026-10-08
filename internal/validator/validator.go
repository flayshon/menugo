// Package validator provides a small helper for collecting request validation
// errors, in the style of Let's Go Further.
package validator

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	// EmailRX is the pattern recommended by the WHATWG for email inputs.
	EmailRX = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+\\/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

	// SlugRX matches lowercase, hyphen-separated slugs like "pizza-place-2".
	SlugRX = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// Validator holds a map of field names to validation error messages.
type Validator struct {
	Errors map[string]string
}

// New returns a Validator with no errors.
func New() *Validator {
	return &Validator{Errors: make(map[string]string)}
}

// Valid reports whether no errors have been recorded.
func (v *Validator) Valid() bool {
	return len(v.Errors) == 0
}

// AddError records an error for key, unless one is already recorded for it.
func (v *Validator) AddError(key, message string) {
	if _, exists := v.Errors[key]; !exists {
		v.Errors[key] = message
	}
}

// Check records an error for key if ok is false.
func (v *Validator) Check(ok bool, key, message string) {
	if !ok {
		v.AddError(key, message)
	}
}

// NotBlank reports whether value contains at least one non-whitespace character.
func NotBlank(value string) bool {
	return strings.TrimSpace(value) != ""
}

// MaxChars reports whether value has at most n characters (runes, not bytes).
func MaxChars(value string, n int) bool {
	return utf8.RuneCountInString(value) <= n
}

// MinChars reports whether value has at least n characters (runes, not bytes).
func MinChars(value string, n int) bool {
	return utf8.RuneCountInString(value) >= n
}

// Matches reports whether value matches rx.
func Matches(value string, rx *regexp.Regexp) bool {
	return rx.MatchString(value)
}

// PermittedValue reports whether value is one of permittedValues.
func PermittedValue[T comparable](value T, permittedValues ...T) bool {
	return slices.Contains(permittedValues, value)
}

// IsHTTPURL reports whether value is an absolute http or https URL.
func IsHTTPURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
