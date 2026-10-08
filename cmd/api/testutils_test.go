package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/events"
	"menugo.flayshon.com/internal/testdb"
)

// newTestApplication returns an application without a database, for tests of
// code that doesn't touch it. Logs go to the test output.
func newTestApplication(t *testing.T) *application {
	t.Helper()

	var cfg config
	cfg.env = "development"
	cfg.auth.tokenTTL = time.Hour
	cfg.shutdownTimeout = 5 * time.Second

	return &application{
		config:        cfg,
		logger:        slog.New(slog.NewTextHandler(t.Output(), nil)),
		broker:        events.NewBroker(),
		streams:       defaultStreamSettings,
		streamsClosed: make(chan struct{}),
	}
}

// newTestApplicationWithDB returns an application backed by a fresh test
// database. The test is skipped if TEST_DB_DSN isn't set.
func newTestApplicationWithDB(t *testing.T) *application {
	t.Helper()

	db := testdb.New(t)
	app := newTestApplication(t)
	app.db = db
	app.models = data.NewModels(db)
	return app
}

type testServer struct {
	*httptest.Server
}

func newTestServer(t *testing.T, h http.Handler) *testServer {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &testServer{ts}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// do sends a request. body may be nil, a string (sent as is) or a value to
// encode as JSON. token, if not empty, is sent as a bearer token.
func (ts *testServer) do(t *testing.T, method, path, token string, body any) response {
	t.Helper()
	header := make(http.Header)
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	return ts.doWithHeader(t, method, path, body, header)
}

// doWithHeader is like do, with arbitrary request headers.
func (ts *testServer) doWithHeader(t *testing.T, method, path string, body any, header http.Header) response {
	t.Helper()

	var reqBody io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reqBody = bytes.NewBufferString(b)
	default:
		js, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		reqBody = bytes.NewBuffer(js)
	}

	req, err := http.NewRequest(method, ts.URL+path, reqBody)
	if err != nil {
		t.Fatal(err)
	}
	maps.Copy(req.Header, header)

	// Don't follow redirects, so tests see exactly what the API sent.
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	return response{status: res.StatusCode, header: res.Header, body: resBody}
}

// decode unmarshals a JSON response body into a T.
func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
	return v
}

// errorMessage returns the "error" value of a response as a string.
func errorMessage(t *testing.T, body []byte) string {
	t.Helper()
	return decode[struct {
		Error string `json:"error"`
	}](t, body).Error
}

// validationErrors returns the field errors of a 422 response.
func validationErrors(t *testing.T, body []byte) map[string]string {
	t.Helper()
	return decode[struct {
		Error map[string]string `json:"error"`
	}](t, body).Error
}

func assertStatus(t *testing.T, res response, want int) {
	t.Helper()
	if res.status != want {
		t.Fatalf("status = %d; want %d; body: %s", res.status, want, res.body)
	}
}

const testPassword = "pa55word-for-tests"

// signUp registers a user and logs them in, returning their ID and token.
func (ts *testServer) signUp(t *testing.T, email string) (int64, string) {
	t.Helper()

	res := ts.do(t, http.MethodPost, "/v1/users", "", map[string]string{
		"name": "Test User", "email": email, "password": testPassword,
	})
	assertStatus(t, res, http.StatusCreated)
	user := decode[struct {
		User userResponse `json:"user"`
	}](t, res.body).User

	return user.ID, ts.login(t, email, testPassword)
}

func (ts *testServer) login(t *testing.T, email, password string) string {
	t.Helper()

	res := ts.do(t, http.MethodPost, "/v1/tokens/authentication", "", map[string]string{
		"email": email, "password": password,
	})
	assertStatus(t, res, http.StatusCreated)

	return decode[struct {
		Token struct {
			Token string `json:"token"`
		} `json:"authentication_token"`
	}](t, res.body).Token.Token
}

func (ts *testServer) createRestaurant(t *testing.T, token, slug string) restaurantResponse {
	t.Helper()

	res := ts.do(t, http.MethodPost, "/v1/restaurants", token, map[string]string{
		"name": "Restaurant " + slug, "slug": slug, "currency": "BRL",
	})
	assertStatus(t, res, http.StatusCreated)

	return decode[struct {
		Restaurant restaurantResponse `json:"restaurant"`
	}](t, res.body).Restaurant
}

func (ts *testServer) addMember(t *testing.T, token string, restaurantID int64, email string, role data.Role) response {
	t.Helper()
	return ts.do(t, http.MethodPost, restaurantPath(restaurantID)+"/members", token, map[string]string{
		"email": email, "role": string(role),
	})
}
