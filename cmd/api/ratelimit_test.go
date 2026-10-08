package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"menugo.flayshon.com/internal/ratelimit"
)

func TestClientIP(t *testing.T) {
	app := newTestApplication(t)

	tests := []struct {
		trust  bool
		remote string
		xff    []string
		want   string
	}{
		{false, "203.0.113.7:5000", nil, "203.0.113.7"},
		{false, "203.0.113.7:5000", []string{"198.51.100.1"}, "203.0.113.7"}, // not trusted: ignored
		{true, "10.0.0.2:5000", []string{"198.51.100.1"}, "198.51.100.1"},
		// The client can prepend anything; only the last entry, added by
		// our proxy, counts.
		{true, "10.0.0.2:5000", []string{"1.2.3.4, 198.51.100.1"}, "198.51.100.1"},
		{true, "10.0.0.2:5000", []string{"1.2.3.4", "198.51.100.1"}, "198.51.100.1"},
		{true, "10.0.0.2:5000", []string{"garbage"}, "10.0.0.2"},
		{true, "10.0.0.2:5000", nil, "10.0.0.2"},
		{true, "[2001:db8::1]:5000", []string{"2001:db8::2"}, "2001:db8::2"},
	}

	for _, tt := range tests {
		app.config.trustProxyHeaders = tt.trust
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tt.remote
		for _, v := range tt.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := app.clientIP(r); got != tt.want {
			t.Errorf("clientIP(trust=%t, %s, %v) = %s; want %s", tt.trust, tt.remote, tt.xff, got, tt.want)
		}
	}
}

func (ts *testServer) doFrom(t *testing.T, ip, method, path string, body any) response {
	t.Helper()
	return ts.doWithHeader(t, method, path, body, http.Header{"X-Forwarded-For": {ip}})
}

func TestGlobalRateLimit(t *testing.T) {
	app := newTestApplication(t)
	app.config.trustProxyHeaders = true
	app.limiters.global = ratelimit.New(ratelimit.Per(2, time.Hour), 2)
	ts := newTestServer(t, app.routes())

	for range 2 {
		assertStatus(t, ts.doFrom(t, "198.51.100.1", http.MethodGet, "/v1/nope", nil), http.StatusNotFound)
	}

	res := ts.doFrom(t, "198.51.100.1", http.MethodGet, "/v1/nope", nil)
	assertStatus(t, res, http.StatusTooManyRequests)
	if res.header.Get("Retry-After") != "1800" {
		t.Errorf("Retry-After = %q; want 1800", res.header.Get("Retry-After"))
	}
	if msg := errorMessage(t, res.body); msg != "rate limit exceeded" {
		t.Errorf("message = %q", msg)
	}

	assertStatus(t, ts.doFrom(t, "198.51.100.2", http.MethodGet, "/v1/nope", nil), http.StatusNotFound)
}

func TestSpoofedHeadersDontEscapeLimits(t *testing.T) {
	app := newTestApplication(t) // trustProxyHeaders is off
	app.limiters.global = ratelimit.New(ratelimit.Per(1, time.Hour), 1)
	ts := newTestServer(t, app.routes())

	assertStatus(t, ts.doFrom(t, "198.51.100.1", http.MethodGet, "/v1/nope", nil), http.StatusNotFound)
	assertStatus(t, ts.doFrom(t, "198.51.100.2", http.MethodGet, "/v1/nope", nil), http.StatusTooManyRequests)
}

func TestLoginRateLimits(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	app.config.trustProxyHeaders = true
	ts := newTestServer(t, app.routes())
	ts.signUp(t, "alice@example.com")

	app.limiters.loginEmail = ratelimit.New(ratelimit.Per(2, time.Hour), 2)
	app.limiters.auth = ratelimit.New(ratelimit.Per(3, time.Hour), 3)
	limited := newTestServer(t, app.routes())

	wrong := map[string]string{"email": "alice@example.com", "password": "wrong-password"}

	// Two wrong passwords from different IPs, then the account is locked
	// for everyone, even with the right password.
	assertStatus(t, limited.doFrom(t, "198.51.100.1", http.MethodPost, "/v1/tokens/authentication", wrong), http.StatusUnauthorized)
	assertStatus(t, limited.doFrom(t, "198.51.100.2", http.MethodPost, "/v1/tokens/authentication", wrong), http.StatusUnauthorized)
	right := map[string]string{"email": "ALICE@example.com", "password": testPassword}
	assertStatus(t, limited.doFrom(t, "198.51.100.3", http.MethodPost, "/v1/tokens/authentication", right), http.StatusTooManyRequests)

	// Other accounts are unaffected, until this IP hits its own limit.
	other := map[string]string{"email": "bob@example.com", "password": "whatever-pw"}
	assertStatus(t, limited.doFrom(t, "198.51.100.1", http.MethodPost, "/v1/tokens/authentication", other), http.StatusUnauthorized)
	assertStatus(t, limited.doFrom(t, "198.51.100.1", http.MethodPost, "/v1/users", map[string]string{"name": "x"}), http.StatusUnprocessableEntity)
	assertStatus(t, limited.doFrom(t, "198.51.100.1", http.MethodPost, "/v1/users", map[string]string{"name": "x"}), http.StatusTooManyRequests)
}

func TestPublicOrderRateLimit(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	s.app.config.trustProxyHeaders = true
	s.app.limiters.publicWrite = ratelimit.New(ratelimit.Per(1, time.Hour), 1)
	limited := newTestServer(t, s.app.routes())

	assertStatus(t, limited.doFrom(t, "198.51.100.1", http.MethodPost, "/v1/menus/pizza/orders", s.deliveryOrder()), http.StatusCreated)
	assertStatus(t, limited.doFrom(t, "198.51.100.1", http.MethodPost, "/v1/menus/pizza/orders", s.deliveryOrder()), http.StatusTooManyRequests)
	assertStatus(t, limited.doFrom(t, "198.51.100.2", http.MethodPost, "/v1/menus/pizza/orders", s.deliveryOrder()), http.StatusCreated)

	// Reading the menu isn't limited by this.
	assertStatus(t, limited.doFrom(t, "198.51.100.1", http.MethodGet, "/v1/menus/pizza", nil), http.StatusOK)
}
