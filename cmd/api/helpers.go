package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"menugo.flayshon.com/internal/validator"
)

// maxRequestBodyBytes caps the size of JSON request bodies.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// envelope wraps every JSON response in a named top-level object, e.g.
// {"restaurant": {...}}.
type envelope map[string]any

func (app *application) writeJSON(w http.ResponseWriter, status int, data envelope, headers http.Header) error {
	js, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return fmt.Errorf("encoding JSON response: %w", err)
	}
	js = append(js, '\n')

	maps.Copy(w.Header(), headers)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(js)

	return nil
}

// readJSON decodes a single JSON object from the request body into dst. It
// rejects unknown fields, trailing data and bodies over maxRequestBodyBytes,
// and turns decoding errors into messages that are safe to show the client.
func (app *application) readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError
		var invalidUnmarshalError *json.InvalidUnmarshalError
		var maxBytesError *http.MaxBytesError

		switch {
		case errors.As(err, &syntaxError):
			return fmt.Errorf("body contains badly-formed JSON (at character %d)", syntaxError.Offset)

		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("body contains badly-formed JSON")

		case errors.As(err, &unmarshalTypeError):
			if unmarshalTypeError.Field != "" {
				return fmt.Errorf("body contains incorrect JSON type for field %q", unmarshalTypeError.Field)
			}
			return fmt.Errorf("body contains incorrect JSON type (at character %d)", unmarshalTypeError.Offset)

		case errors.Is(err, io.EOF):
			return errors.New("body must not be empty")

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			// encoding/json has no typed error for this case.
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return fmt.Errorf("body contains unknown key %s", fieldName)

		case errors.As(err, &maxBytesError):
			return fmt.Errorf("body must not be larger than %d bytes", maxBytesError.Limit)

		case errors.As(err, &invalidUnmarshalError):
			// We passed something that isn't a non-nil pointer: our bug.
			panic(err)

		default:
			return err
		}
	}

	err = dec.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		return errors.New("body must only contain a single JSON value")
	}

	return nil
}

// readIDParam parses a positive integer ID from the named path parameter.
func (app *application) readIDParam(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid %s parameter", name)
	}
	return id, nil
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. ok is false if the header is absent; err is set if it is malformed.
func bearerToken(r *http.Request) (token string, ok bool, err error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false, nil
	}

	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", true, errors.New("malformed Authorization header")
	}
	return token, true, nil
}

// readString returns the query string value for key, or defaultValue.
func (app *application) readString(qs url.Values, key, defaultValue string) string {
	if s := qs.Get(key); s != "" {
		return s
	}
	return defaultValue
}

// readCSV splits a comma-separated query string value, e.g.
// ?status=pending,confirmed. It returns nil if the key is absent.
func (app *application) readCSV(qs url.Values, key string) []string {
	if s := qs.Get(key); s != "" {
		return strings.Split(s, ",")
	}
	return nil
}

// readInt parses an integer query string value, recording a validation
// error if it isn't one.
func (app *application) readInt(qs url.Values, key string, defaultValue int, v *validator.Validator) int {
	s := qs.Get(key)
	if s == "" {
		return defaultValue
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		v.AddError(key, "must be an integer value")
		return defaultValue
	}
	return i
}

// readTime parses an RFC 3339 query string value, recording a validation
// error if it isn't one. It returns the zero time if the key is absent.
func (app *application) readTime(qs url.Values, key string, v *validator.Validator) time.Time {
	s := qs.Get(key)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		v.AddError(key, "must be an RFC 3339 time, e.g. 2026-10-08T00:00:00Z")
		return time.Time{}
	}
	return t
}
