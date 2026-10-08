package data

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"

	"menugo.flayshon.com/internal/validator"
)

const ScopeAuthentication = "authentication"

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
	hash := sha256.Sum256([]byte(plaintext))

	return &Token{
		Plaintext: plaintext,
		Hash:      hash[:],
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

	hash := sha256.Sum256([]byte(tokenPlaintext))

	_, err := m.DB.ExecContext(ctx, "DELETE FROM tokens WHERE hash = ? AND scope = ?", hash[:], scope)
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
