package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

// createAuthenticationTokenHandler exchanges an email and password for a
// bearer token.
func (app *application) createAuthenticationTokenHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	// Only check what's needed to attempt a login, not the password policy:
	// the policy may change after a user has registered.
	v := validator.New()
	data.ValidateEmail(v, input.Email)
	v.Check(input.Password != "", "password", "must be provided")
	v.Check(len(input.Password) <= 72, "password", "must not be more than 72 bytes long")
	if !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// Limit attempts per account, whatever IP they come from.
	if !app.allow(w, r, app.limiters.loginEmail, strings.ToLower(input.Email)) {
		return
	}

	user, err := app.models.Users.GetByEmail(r.Context(), input.Email)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			data.SimulatePasswordCheck(input.Password)
			app.invalidCredentialsResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	match, err := user.Password.Matches(input.Password)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}
	if !match {
		app.invalidCredentialsResponse(w, r)
		return
	}

	token, err := app.models.Tokens.New(r.Context(), user.ID, app.config.auth.tokenTTL, data.ScopeAuthentication)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	resp := struct {
		Token  string    `json:"token"`
		Expiry time.Time `json:"expiry"`
	}{token.Plaintext, token.Expiry}

	err = app.writeJSON(w, http.StatusCreated, envelope{"authentication_token": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// deleteAuthenticationTokenHandler logs out by revoking the token used to
// make the request.
func (app *application) deleteAuthenticationTokenHandler(w http.ResponseWriter, r *http.Request) {
	// authenticate has already checked the header.
	token, _, _ := bearerToken(r)

	if err := app.models.Tokens.Delete(r.Context(), data.ScopeAuthentication, token); err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// createStreamTicketHandler issues a stream ticket: a token that opens one
// event stream with ?ticket=, for clients that can't send an Authorization
// header. It is valid for a minute, works once, and lasts no longer than the
// authentication token used to get it.
func (app *application) createStreamTicketHandler(w http.ResponseWriter, r *http.Request) {
	ticket, err := app.models.Tokens.NewStreamTicket(r.Context(), app.contextGetAuthHash(r))
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.invalidAuthenticationTokenResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	resp := struct {
		Ticket string    `json:"ticket"`
		Expiry time.Time `json:"expiry"`
	}{ticket.Plaintext, ticket.Expiry}

	err = app.writeJSON(w, http.StatusCreated, envelope{"stream_ticket": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
