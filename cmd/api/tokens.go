package main

import (
	"errors"
	"net/http"
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
