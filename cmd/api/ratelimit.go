package main

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"menugo.flayshon.com/internal/ratelimit"
)

// Limits for sensitive endpoints, on top of the general per-IP limit. They
// are deliberately not configurable: they only need to stop abuse, and are
// far above what a real person does.
var (
	// Registering and logging in, per client IP.
	authLimit, authBurst = ratelimit.Per(10, time.Minute), 10
	// Login attempts per email address, from any IP, so one account can't
	// be brute-forced from many addresses.
	loginEmailLimit, loginEmailBurst = ratelimit.Per(10, 15*time.Minute), 10
	// Placing and cancelling orders on the public API, per client IP.
	publicWriteLimit, publicWriteBurst = ratelimit.Per(20, time.Minute), 20
)

// limiters are nil when rate limiting is disabled.
type limiters struct {
	global      *ratelimit.Limiter
	auth        *ratelimit.Limiter
	loginEmail  *ratelimit.Limiter
	publicWrite *ratelimit.Limiter
}

func newLimiters(cfg config) limiters {
	if !cfg.limiter.enabled {
		return limiters{}
	}
	return limiters{
		global:      ratelimit.New(rate.Limit(cfg.limiter.rps), cfg.limiter.burst),
		auth:        ratelimit.New(authLimit, authBurst),
		loginEmail:  ratelimit.New(loginEmailLimit, loginEmailBurst),
		publicWrite: ratelimit.New(publicWriteLimit, publicWriteBurst),
	}
}

// cleanupLimiters periodically drops limiter state that no longer matters,
// so memory doesn't grow with every client ever seen.
func (app *application) cleanupLimiters(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, l := range []*ratelimit.Limiter{app.limiters.global, app.limiters.auth, app.limiters.loginEmail, app.limiters.publicWrite} {
				if l != nil {
					l.Cleanup()
				}
			}
		}
	}
}

// clientIP returns the address the request came from. With
// trustProxyHeaders, that's the last entry of X-Forwarded-For: the one our
// own reverse proxy added. Earlier entries come from the client and can't be
// trusted.
func (app *application) clientIP(r *http.Request) string {
	if app.config.trustProxyHeaders {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			entries := strings.Split(xff[len(xff)-1], ",")
			if ip := net.ParseIP(strings.TrimSpace(entries[len(entries)-1])); ip != nil {
				return ip.String()
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimitExceededResponse sends a 429 telling the client when to retry.
func (app *application) rateLimitExceededResponse(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(seconds, 1)))
	app.errorResponse(w, r, http.StatusTooManyRequests, "rate limit exceeded")
}

// allow reports whether key is within l's limit, writing a 429 if not. A nil
// limiter allows everything.
func (app *application) allow(w http.ResponseWriter, r *http.Request, l *ratelimit.Limiter, key string) bool {
	if l == nil {
		return true
	}
	if ok, retryAfter := l.Allow(key); !ok {
		app.rateLimitExceededResponse(w, r, retryAfter)
		return false
	}
	return true
}

// rateLimit applies the general per-IP limit to every request.
func (app *application) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !app.allow(w, r, app.limiters.global, app.clientIP(r)) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitByIP applies l to a single route, per client IP.
func (app *application) limitByIP(l *ratelimit.Limiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !app.allow(w, r, l, app.clientIP(r)) {
			return
		}
		next(w, r)
	}
}
