package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"menugo.flayshon.com/internal/data"
)

func TestHealthcheck(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	res := ts.do(t, http.MethodGet, "/v1/healthcheck", "", nil)
	assertStatus(t, res, http.StatusOK)

	body := decode[struct {
		Status     string            `json:"status"`
		SystemInfo map[string]string `json:"system_info"`
	}](t, res.body)
	if body.Status != "available" || body.SystemInfo["version"] != version {
		t.Errorf("body = %+v", body)
	}
}

func TestRegisterUser(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	valid := map[string]string{"name": "Alice", "email": "alice@example.com", "password": testPassword}

	res := ts.do(t, http.MethodPost, "/v1/users", "", valid)
	assertStatus(t, res, http.StatusCreated)
	if strings.Contains(string(res.body), "password") {
		t.Errorf("response exposes password data: %s", res.body)
	}
	user := decode[struct {
		User userResponse `json:"user"`
	}](t, res.body).User
	if user.ID == 0 || user.Email != "alice@example.com" || user.Name != "Alice" {
		t.Errorf("user = %+v", user)
	}

	tests := []struct {
		name  string
		body  map[string]string
		field string
	}{
		{"missing name", map[string]string{"email": "b@example.com", "password": testPassword}, "name"},
		{"invalid email", map[string]string{"name": "B", "email": "not-an-email", "password": testPassword}, "email"},
		{"short password", map[string]string{"name": "B", "email": "b@example.com", "password": "short"}, "password"},
		// bcrypt can't hash this; it must be a 422, not a 500.
		{"password over 72 bytes", map[string]string{"name": "B", "email": "b@example.com", "password": strings.Repeat("x", 73)}, "password"},
		{"duplicate email in another case", map[string]string{"name": "A2", "email": "ALICE@example.com", "password": testPassword}, "email"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ts.do(t, http.MethodPost, "/v1/users", "", tt.body)
			assertStatus(t, res, http.StatusUnprocessableEntity)
			if _, ok := validationErrors(t, res.body)[tt.field]; !ok {
				t.Errorf("expected an error for %q: %s", tt.field, res.body)
			}
		})
	}

	t.Run("unknown field", func(t *testing.T) {
		res := ts.do(t, http.MethodPost, "/v1/users", "", `{"name":"B","email":"b@example.com","password":"`+testPassword+`","role":"admin"}`)
		assertStatus(t, res, http.StatusBadRequest)
	})
}

func TestAuthentication(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	userID, token := ts.signUp(t, "alice@example.com")

	t.Run("token identifies the user", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/users/me", token, nil)
		assertStatus(t, res, http.StatusOK)
		me := decode[struct {
			User userResponse `json:"user"`
		}](t, res.body).User
		if me.ID != userID {
			t.Errorf("id = %d; want %d", me.ID, userID)
		}
	})

	t.Run("email is case-insensitive at login", func(t *testing.T) {
		ts.login(t, "ALICE@example.com", testPassword)
	})

	t.Run("bad credentials get the same response", func(t *testing.T) {
		wrongPassword := ts.do(t, http.MethodPost, "/v1/tokens/authentication", "",
			map[string]string{"email": "alice@example.com", "password": "wrong-password"})
		unknownEmail := ts.do(t, http.MethodPost, "/v1/tokens/authentication", "",
			map[string]string{"email": "nobody@example.com", "password": testPassword})

		assertStatus(t, wrongPassword, http.StatusUnauthorized)
		assertStatus(t, unknownEmail, http.StatusUnauthorized)
		if string(wrongPassword.body) != string(unknownEmail.body) {
			t.Errorf("responses differ, revealing which emails exist:\n%s\n%s", wrongPassword.body, unknownEmail.body)
		}
	})

	t.Run("login validates input", func(t *testing.T) {
		res := ts.do(t, http.MethodPost, "/v1/tokens/authentication", "", map[string]string{"email": "alice@example.com"})
		assertStatus(t, res, http.StatusUnprocessableEntity)
	})

	t.Run("unknown token is rejected", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/users/me", strings.Repeat("A", 26), nil)
		assertStatus(t, res, http.StatusUnauthorized)
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		expired, err := app.models.Tokens.New(context.Background(), userID, -time.Minute, data.ScopeAuthentication)
		if err != nil {
			t.Fatal(err)
		}
		res := ts.do(t, http.MethodGet, "/v1/users/me", expired.Plaintext, nil)
		assertStatus(t, res, http.StatusUnauthorized)
	})

	t.Run("logout revokes only the token used", func(t *testing.T) {
		other := ts.login(t, "alice@example.com", testPassword)

		res := ts.do(t, http.MethodDelete, "/v1/tokens/authentication", token, nil)
		assertStatus(t, res, http.StatusNoContent)

		res = ts.do(t, http.MethodGet, "/v1/users/me", token, nil)
		assertStatus(t, res, http.StatusUnauthorized)

		res = ts.do(t, http.MethodGet, "/v1/users/me", other, nil)
		assertStatus(t, res, http.StatusOK)
	})
}
