package main

import (
	"context"
	"net/http"
	"strings"

	"menugo.flayshon.com/internal/data"
)

type contextKey string

const (
	requestInfoContextKey = contextKey("requestInfo")
	userContextKey        = contextKey("user")
	membershipContextKey  = contextKey("membership")
)

// requestInfo collects facts about a request for the request log. The logging
// middleware creates it; middleware further in fills in the user and
// restaurant once they are known.
type requestInfo struct {
	id           string
	route        string // the matched ServeMux pattern, e.g. "GET /v1/menus/{slug}"
	userID       int64
	restaurantID int64
}

// logPath returns the request path to log. Paths of routes with a {token}
// parameter carry a secret, so for those the route pattern is logged instead.
func (info *requestInfo) logPath(r *http.Request) string {
	if strings.Contains(info.route, "{token}") {
		_, path, _ := strings.Cut(info.route, " ")
		return path
	}
	return r.URL.Path
}

func (app *application) contextSetRequestInfo(r *http.Request, info *requestInfo) *http.Request {
	ctx := context.WithValue(r.Context(), requestInfoContextKey, info)
	return r.WithContext(ctx)
}

// contextGetRequestInfo returns the request's info, or an empty one if the
// logging middleware didn't run (as in some unit tests).
func (app *application) contextGetRequestInfo(r *http.Request) *requestInfo {
	info, ok := r.Context().Value(requestInfoContextKey).(*requestInfo)
	if !ok {
		return &requestInfo{}
	}
	return info
}

func (app *application) contextSetUser(r *http.Request, user *data.User) *http.Request {
	ctx := context.WithValue(r.Context(), userContextKey, user)
	return r.WithContext(ctx)
}

// contextGetUser returns the authenticated user, or data.AnonymousUser. It
// panics if the authenticate middleware didn't run, which is a programming
// error.
func (app *application) contextGetUser(r *http.Request) *data.User {
	user, ok := r.Context().Value(userContextKey).(*data.User)
	if !ok {
		panic("missing user value in request context")
	}
	return user
}

func (app *application) contextSetMembership(r *http.Request, ms *data.Membership) *http.Request {
	ctx := context.WithValue(r.Context(), membershipContextKey, ms)
	return r.WithContext(ctx)
}

// contextGetMembership returns the authenticated user's membership of the
// restaurant in the URL. Handlers behind requireRestaurantRole use its
// RestaurantID, never an ID from the request, to scope their queries.
func (app *application) contextGetMembership(r *http.Request) *data.Membership {
	ms, ok := r.Context().Value(membershipContextKey).(*data.Membership)
	if !ok {
		panic("missing membership value in request context")
	}
	return ms
}
