package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadJSON(t *testing.T) {
	app := newTestApplication(t)

	type input struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"valid", `{"name": "x", "count": 2}`, ""},
		{"empty", ``, "body must not be empty"},
		{"badly formed", `{"name": "x",}`, "badly-formed JSON"},
		{"truncated", `{"name": "x"`, "badly-formed JSON"},
		{"wrong type", `{"count": "two"}`, `incorrect JSON type for field "count"`},
		{"not an object", `["x"]`, "incorrect JSON type"},
		{"unknown field", `{"name": "x", "admin": true}`, `unknown key "admin"`},
		{"two values", `{"name": "x"}{"name": "y"}`, "single JSON value"},
		{"too large", `{"name": "` + strings.Repeat("x", maxRequestBodyBytes) + `"}`, "must not be larger than"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			var dst input
			err := app.readJSON(w, r, &dst)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if dst.Name != "x" || dst.Count != 2 {
					t.Errorf("decoded %+v", dst)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v; want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestReadIDParam(t *testing.T) {
	app := newTestApplication(t)

	tests := map[string]int64{
		"1":                    1,
		"42":                   42,
		"0":                    0,
		"-1":                   0,
		"abc":                  0,
		"1.5":                  0,
		"":                     0,
		"99999999999999999999": 0,
	}

	for value, want := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetPathValue("id", value)

		got, err := app.readIDParam(r, "id")
		if want == 0 {
			if err == nil {
				t.Errorf("readIDParam(%q) = %d; want an error", value, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("readIDParam(%q) = %d, %v; want %d", value, got, err, want)
		}
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header    string
		wantToken string
		wantOK    bool
		wantErr   bool
	}{
		{"", "", false, false},
		{"Bearer abc", "abc", true, false},
		{"bearer abc", "abc", true, false},
		{"Basic abc", "", true, true},
		{"Bearer", "", true, true},
		{"Bearer ", "", true, true},
	}

	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.header != "" {
			r.Header.Set("Authorization", tt.header)
		}

		token, ok, err := bearerToken(r)
		if token != tt.wantToken || ok != tt.wantOK || (err != nil) != tt.wantErr {
			t.Errorf("bearerToken(%q) = %q, %t, %v; want %q, %t, err=%t",
				tt.header, token, ok, err, tt.wantToken, tt.wantOK, tt.wantErr)
		}
	}
}
