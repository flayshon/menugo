package data

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"menugo.flayshon.com/internal/validator"
)

const (
	ScopeAuthentication = "authentication"
	// ScopeStream is for stream tickets: short-lived, single-use tokens for
	// opening an event stream where the client can't send an
	// Authorization header (a browser's EventSource).
	ScopeStream = "stream"
)

// StreamTicketTTL is how long a stream ticket can be used.
const StreamTicketTTL = time.Minute

// TokenHash returns the hash stored for a token.
func TokenHash(tokenPlaintext string) []byte {
	hash := sha256.Sum256([]byte(tokenPlaintext))
	return hash[:]
}

// Token is a bearer token. Plaintext is only known when the token is created;
// the database stores its SHA-256 hash.
type Token struct {
	Plaintext string
	Hash      []byte
	UserID    int64
	Expiry    time.Time
	Scope     string
}

func generateToken(userID int64, ttl time.Duration, scope string) *Token {
	// rand.Text returns 26 base32 characters: 128 bits of randomness.
	plaintext := rand.Text()

	return &Token{
		Plaintext: plaintext,
		Hash:      TokenHash(plaintext),
		UserID:    userID,
		Expiry:    now().Add(ttl),
		Scope:     scope,
	}
}

// ValidateTokenPlaintext checks the shape of a token from the client before
// it is looked up.
func ValidateTokenPlaintext(v *validator.Validator, tokenPlaintext string) {
	v.Check(tokenPlaintext != "", "token", "must be provided")
	v.Check(len(tokenPlaintext) == 26, "token", "must be 26 bytes long")
}

type TokenModel struct {
	DB *sql.DB
}

// New creates and stores a token for userID.
func (m TokenModel) New(ctx context.Context, userID int64, ttl time.Duration, scope string) (*Token, error) {
	token := generateToken(userID, ttl, scope)
	if err := m.Insert(ctx, token); err != nil {
		return nil, err
	}
	return token, nil
}

func (m TokenModel) Insert(ctx context.Context, token *Token) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		INSERT INTO tokens (hash, user_id, expiry, scope)
		VALUES (?, ?, ?, ?)`

	_, err := m.DB.ExecContext(ctx, query, token.Hash, token.UserID, token.Expiry, token.Scope)
	if err != nil {
		return fmt.Errorf("inserting token: %w", err)
	}
	return nil
}

// Delete removes a single token, identified by its plaintext.
func (m TokenModel) Delete(ctx context.Context, scope, tokenPlaintext string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	_, err := m.DB.ExecContext(ctx, "DELETE FROM tokens WHERE hash = ? AND scope = ?", TokenHash(tokenPlaintext), scope)
	if err != nil {
		return fmt.Errorf("deleting token: %w", err)
	}
	return nil
}

// DeleteExpired removes tokens that can no longer be used.
func (m TokenModel) DeleteExpired(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	result, err := m.DB.ExecContext(ctx, "DELETE FROM tokens WHERE expiry <= ?", now())
	if err != nil {
		return 0, fmt.Errorf("deleting expired tokens: %w", err)
	}
	return result.RowsAffected()
}

// NewStreamTicket issues a stream ticket for the user of the authentication
// token with hash parentHash. It expires after StreamTicketTTL, or with its
// parent if that's sooner, and is deleted with its parent.
func (m TokenModel) NewStreamTicket(ctx context.Context, parentHash []byte) (*Token, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	ticket := generateToken(0, StreamTicketTTL, ScopeStream)

	err := m.DB.QueryRowContext(ctx, `
		INSERT INTO tokens (hash, user_id, scope, parent_hash, expiry)
		SELECT ?, user_id, ?, hash, LEAST(?, expiry)
		FROM tokens
		WHERE hash = ? AND scope = ? AND expiry > ?
		RETURNING user_id, expiry`,
		ticket.Hash, ScopeStream, ticket.Expiry, parentHash, ScopeAuthentication, now(),
	).Scan(&ticket.UserID, &ticket.Expiry)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("inserting stream ticket: %w", err)
	}
	return ticket, nil
}

// ConsumeStreamTicket uses up a stream ticket and returns the hash of the
// authentication token it was issued from. A ticket works once: using it
// deletes it. Unknown, used and expired tickets give ErrRecordNotFound.
func (m TokenModel) ConsumeStreamTicket(ctx context.Context, ticketPlaintext string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var parentHash []byte
	err := m.DB.QueryRowContext(ctx, `
		DELETE FROM tokens
		WHERE hash = ? AND scope = ? AND expiry > ?
		RETURNING parent_hash`,
		TokenHash(ticketPlaintext), ScopeStream, now(),
	).Scan(&parentHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("consuming stream ticket: %w", err)
	}
	return parentHash, nil
}
