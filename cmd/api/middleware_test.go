package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecureHeaders(t *testing.T) {
	app := newTestApplication(t)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	w := httptest.NewRecorder()
	app.secureHeaders(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
	}
	for header, value := range want {
		if got := w.Header().Get(header); got != value {
			t.Errorf("%s = %q; want %q", header, got, value)
		}
	}
	if w.Body.String() != "OK" {
		t.Errorf("next handler was not called")
	}
}

func TestRecoverPanic(t *testing.T) {
	app := newTestApplication(t)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something secret went wrong")
	})

	w := httptest.NewRecorder()
	app.recoverPanic(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500", w.Code)
	}
	if w.Header().Get("Connection") != "close" {
		t.Error("expected Connection: close")
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Errorf("panic value leaked to the client: %s", w.Body)
	}
}

func TestLogRequest(t *testing.T) {
	var logs bytes.Buffer
	app := newTestApplication(t)
	app.logger = slog.New(slog.NewTextHandler(&logs, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := app.contextGetRequestInfo(r)
		info.userID = 7
		info.restaurantID = 9
		w.WriteHeader(http.StatusTeapot)
	})

	r := httptest.NewRequest(http.MethodPost, "/v1/things?secret=query", strings.NewReader(`{"password":"hunter22"}`))
	r.Header.Set("Authorization", "Bearer SECRETTOKENSECRETTOKEN1234")
	w := httptest.NewRecorder()

	app.logRequest(next).ServeHTTP(w, r)

	requestID := w.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("missing X-Request-ID header")
	}

	line := logs.String()
	for _, want := range []string{"request_id=" + requestID, "method=POST", "path=/v1/things", "status=418", "user_id=7", "restaurant_id=9", "duration="} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q is missing %q", line, want)
		}
	}
	for _, secret := range []string{"SECRETTOKEN", "hunter22", "secret=query"} {
		if strings.Contains(line, secret) {
			t.Errorf("log line leaks %q: %s", secret, line)
		}
	}
}

// These go through the full middleware chain but need no database: the
// requests are rejected before any query runs.
func TestRoutesWithoutDatabase(t *testing.T) {
	app := newTestApplication(t)
	ts := newTestServer(t, app.routes())

	t.Run("unknown path gives a JSON 404", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/nope", "", nil)
		assertStatus(t, res, http.StatusNotFound)
		if got := res.header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if res.header.Get("X-Request-ID") == "" {
			t.Error("missing X-Request-ID")
		}
		errorMessage(t, res.body)
	})

	t.Run("wrong method gives a JSON 405 with Allow", func(t *testing.T) {
		res := ts.do(t, http.MethodPut, "/v1/healthcheck", "", nil)
		assertStatus(t, res, http.StatusMethodNotAllowed)
		if allow := res.header.Get("Allow"); !strings.Contains(allow, "GET") {
			t.Errorf("Allow = %q; want it to include GET", allow)
		}
		if msg := errorMessage(t, res.body); !strings.Contains(msg, "PUT") {
			t.Errorf("message = %q", msg)
		}
	})

	t.Run("unclean path is redirected", func(t *testing.T) {
		// ServeMux picks the status (301 or 307, depending on the Go
		// version); we only pass it through.
		res := ts.do(t, http.MethodGet, "/v1//healthcheck", "", nil)
		if res.status < 300 || res.status > 399 {
			t.Fatalf("status = %d; want a redirect", res.status)
		}
		if loc := res.header.Get("Location"); loc != "/v1/healthcheck" {
			t.Errorf("Location = %q", loc)
		}
	})

	t.Run("protected route needs a token", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/restaurants", "", nil)
		assertStatus(t, res, http.StatusUnauthorized)
		if res.header.Get("WWW-Authenticate") != "Bearer" {
			t.Error("missing WWW-Authenticate: Bearer")
		}
	})

	t.Run("malformed token is rejected", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/restaurants", "too-short", nil)
		assertStatus(t, res, http.StatusUnauthorized)
	})

	t.Run("malformed Authorization header is rejected", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/restaurants", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d; want 401", res.StatusCode)
		}
	})
}
