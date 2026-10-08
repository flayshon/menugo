package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

func (app *application) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			pv := recover()
			if pv == nil {
				return
			}
			// http.ErrAbortHandler is how handlers deliberately abort a
			// response; let net/http deal with it.
			if pv == http.ErrAbortHandler {
				panic(pv)
			}
			w.Header().Set("Connection", "close")
			app.serverErrorResponse(w, r, fmt.Errorf("panic: %v", pv))
		}()

		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code a handler writes.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(status int) {
	if sr.status == 0 {
		sr.status = status
	}
	sr.ResponseWriter.WriteHeader(status)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	return sr.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// logRequest gives each request an ID (returned in the X-Request-ID header)
// and logs one line per request once it has been handled. Request bodies,
// query strings and headers are never logged: they may contain passwords,
// tokens or customer details.
func (app *application) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		info := &requestInfo{id: rand.Text()}
		r = app.contextSetRequestInfo(r, info)
		w.Header().Set("X-Request-ID", info.id)

		rec := &statusRecorder{ResponseWriter: w}

		defer func() {
			attrs := []any{
				"request_id", info.id,
				"method", r.Method,
				"path", info.logPath(r),
				"status", rec.status,
				"duration", time.Since(start),
			}
			if info.userID != 0 {
				attrs = append(attrs, "user_id", info.userID)
			}
			if info.restaurantID != 0 {
				attrs = append(attrs, "restaurant_id", info.restaurantID)
			}

			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			}
			app.logger.Log(r.Context(), level, "request", attrs...)
		}()

		next.ServeHTTP(rec, r)
	})
}

// secureHeaders sets headers suited to a JSON API that is never rendered as
// a page or embedded in one.
func (app *application) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")

		next.ServeHTTP(w, r)
	})
}

// authenticate puts the user identified by the request's bearer token in the
// request context, or data.AnonymousUser if there is no token. A token that is
// present but invalid or expired is rejected outright.
func (app *application) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Authorization")

		token, ok, err := bearerToken(r)
		if err != nil {
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}
		if !ok {
			r = app.contextSetUser(r, data.AnonymousUser)
			next.ServeHTTP(w, r)
			return
		}

		v := validator.New()
		if data.ValidateTokenPlaintext(v, token); !v.Valid() {
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}

		user, err := app.models.Users.GetForToken(r.Context(), data.ScopeAuthentication, token)
		if err != nil {
			switch {
			case errors.Is(err, data.ErrRecordNotFound):
				app.invalidAuthenticationTokenResponse(w, r)
			default:
				app.serverErrorResponse(w, r, err)
			}
			return
		}

		app.contextGetRequestInfo(r).userID = user.ID
		r = app.contextSetUser(r, user)
		r = app.contextSetAuthHash(r, data.TokenHash(token))

		next.ServeHTTP(w, r)
	})
}

// acceptStreamTicket lets a stream route be opened with ?ticket= instead of
// an Authorization header, for clients that can't send headers (a browser's
// EventSource). The ticket is used up; the request then counts as made with
// the authentication token the ticket was issued from.
func (app *application) acceptStreamTicket(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticket := r.URL.Query().Get("ticket")
		if ticket == "" {
			next(w, r)
			return
		}

		if !app.contextGetUser(r).IsAnonymous() {
			app.badRequestResponse(w, r, errors.New("send either an Authorization header or a ticket, not both"))
			return
		}

		v := validator.New()
		if data.ValidateTokenPlaintext(v, ticket); !v.Valid() {
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}

		parentHash, err := app.models.Tokens.ConsumeStreamTicket(r.Context(), ticket)
		if err != nil {
			switch {
			case errors.Is(err, data.ErrRecordNotFound):
				app.invalidAuthenticationTokenResponse(w, r)
			default:
				app.serverErrorResponse(w, r, err)
			}
			return
		}

		user, err := app.models.Users.GetForTokenHash(r.Context(), data.ScopeAuthentication, parentHash)
		if err != nil {
			switch {
			case errors.Is(err, data.ErrRecordNotFound):
				app.invalidAuthenticationTokenResponse(w, r)
			default:
				app.serverErrorResponse(w, r, err)
			}
			return
		}

		app.contextGetRequestInfo(r).userID = user.ID
		r = app.contextSetUser(r, user)
		r = app.contextSetAuthHash(r, parentHash)

		next(w, r)
	}
}

func (app *application) requireAuthenticatedUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if app.contextGetUser(r).IsAnonymous() {
			app.authenticationRequiredResponse(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// requireRestaurantRole is the tenant boundary. It allows the request only if
// the authenticated user is a member of the restaurant in the {restaurantID}
// path parameter with one of the given roles, and puts that membership in the
// request context.
//
// Non-members get a 404, so they can't discover which restaurant IDs exist.
// Members with the wrong role get a 403.
func (app *application) requireRestaurantRole(roles []data.Role, next http.HandlerFunc) http.HandlerFunc {
	return app.requireAuthenticatedUser(func(w http.ResponseWriter, r *http.Request) {
		restaurantID, err := app.readIDParam(r, "restaurantID")
		if err != nil {
			app.notFoundResponse(w, r)
			return
		}

		user := app.contextGetUser(r)

		ms, err := app.models.Memberships.Get(r.Context(), restaurantID, user.ID)
		if err != nil {
			switch {
			case errors.Is(err, data.ErrRecordNotFound):
				app.notFoundResponse(w, r)
			default:
				app.serverErrorResponse(w, r, err)
			}
			return
		}

		app.contextGetRequestInfo(r).restaurantID = ms.RestaurantID

		if !slices.Contains(roles, ms.Role) {
			app.notPermittedResponse(w, r)
			return
		}

		r = app.contextSetMembership(r, ms)
		next.ServeHTTP(w, r)
	})
}

// matchRoute records which route the request matches, before any other
// middleware can respond, so that the request log never contains a secret
// from the path (see requestInfo.logPath).
func (app *application) matchRoute(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, app.contextGetRequestInfo(r).route = mux.Handler(r)
		next.ServeHTTP(w, r)
	})
}

// discardResponse records what a handler would have sent, without sending it.
type discardResponse struct {
	header http.Header
	status int
}

func (d *discardResponse) Header() http.Header { return d.header }

func (d *discardResponse) Write(b []byte) (int, error) {
	if d.status == 0 {
		d.status = http.StatusOK
	}
	return len(b), nil
}

func (d *discardResponse) WriteHeader(status int) {
	if d.status == 0 {
		d.status = status
	}
}

// jsonUnmatched makes requests that match no route get the same JSON error
// format as everything else. http.ServeMux answers them itself, in plain
// text: 404, 405 with an Allow header, or a redirect to a cleaned-up path.
func (app *application) jsonUnmatched(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}

		resp := &discardResponse{header: make(http.Header)}
		mux.ServeHTTP(resp, r)

		switch resp.status {
		case http.StatusNotFound:
			app.notFoundResponse(w, r)
		case http.StatusMethodNotAllowed:
			w.Header().Set("Allow", resp.header.Get("Allow"))
			app.methodNotAllowedResponse(w, r)
		default:
			maps.Copy(w.Header(), resp.header)
			w.WriteHeader(resp.status)
		}
	})
}

// allowAnyOrigin lets web pages on any origin call a public endpoint. It's
// only for endpoints that use no credentials (no Authorization header, no
// cookies), so there is nothing a malicious page could abuse.
func (app *application) allowAnyOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next(w, r)
	}
}

// publicPreflightHandler answers CORS preflight requests for public
// endpoints. Browsers send one before a cross-origin POST with a JSON body.
func (app *application) publicPreflightHandler(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}
