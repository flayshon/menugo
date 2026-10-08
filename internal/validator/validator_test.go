package validator

import (
	"strings"
	"testing"
)

func TestValidatorKeepsFirstError(t *testing.T) {
	v := New()
	if !v.Valid() {
		t.Fatal("new validator should be valid")
	}

	v.Check(false, "name", "first")
	v.Check(false, "name", "second")
	v.Check(true, "email", "never recorded")

	if v.Valid() {
		t.Fatal("validator should be invalid")
	}
	if got := v.Errors["name"]; got != "first" {
		t.Errorf("name error = %q; want %q", got, "first")
	}
	if _, ok := v.Errors["email"]; ok {
		t.Error("email error should not be recorded")
	}
}

func TestEmailRX(t *testing.T) {
	tests := map[string]bool{
		"alice@example.com":        true,
		"alice+tag@sub.example.io": true,
		"ALICE@EXAMPLE.COM":        true,
		"":                         false,
		"alice":                    false,
		"alice@":                   false,
		"@example.com":             false,
		"alice@exa mple.com":       false,
		"alice@-example.com":       false,
	}
	for email, want := range tests {
		if got := Matches(email, EmailRX); got != want {
			t.Errorf("Matches(%q, EmailRX) = %t; want %t", email, got, want)
		}
	}
}

func TestSlugRX(t *testing.T) {
	tests := map[string]bool{
		"pizza":         true,
		"pizza-place-2": true,
		"Pizza":         false,
		"pizza--place":  false,
		"-pizza":        false,
		"pizza-":        false,
		"pizza place":   false,
		"pizza_place":   false,
		"":              false,
	}
	for slug, want := range tests {
		if got := Matches(slug, SlugRX); got != want {
			t.Errorf("Matches(%q, SlugRX) = %t; want %t", slug, got, want)
		}
	}
}

func TestCharCounts(t *testing.T) {
	// "ação" is 4 runes but 6 bytes; limits must count runes.
	if !MaxChars("ação", 4) {
		t.Error(`MaxChars("ação", 4) = false; want true`)
	}
	if MaxChars("ação", 3) {
		t.Error(`MaxChars("ação", 3) = true; want false`)
	}
	if !MinChars("ação", 4) {
		t.Error(`MinChars("ação", 4) = false; want true`)
	}
	if NotBlank(" \t\n") {
		t.Error("NotBlank(whitespace) = true; want false")
	}
	if !MaxChars(strings.Repeat("a", 10), 10) {
		t.Error("MaxChars at the limit should be true")
	}
}

func TestPermittedValue(t *testing.T) {
	if !PermittedValue("b", "a", "b") {
		t.Error("expected b to be permitted")
	}
	if PermittedValue("c", "a", "b") {
		t.Error("expected c not to be permitted")
	}
}
