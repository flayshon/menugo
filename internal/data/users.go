package data

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"menugo.flayshon.com/internal/validator"
)

var ErrDuplicateEmail = errors.New("duplicate email")

const bcryptCost = 12

type User struct {
	ID        int64
	Name      string
	Email     string
	Password  Password
	Version   int32
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AnonymousUser represents a request without an authentication token.
var AnonymousUser = &User{}

func (u *User) IsAnonymous() bool {
	return u == AnonymousUser
}

// Password holds a bcrypt hash. Validate the plaintext with
// ValidatePasswordPlaintext before calling Set: bcrypt rejects inputs longer
// than 72 bytes.
type Password struct {
	hash []byte
}

// Set hashes plaintext and stores the hash.
func (p *Password) Set(plaintext string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	p.hash = hash
	return nil
}

// Matches reports whether plaintext matches the stored hash. The comparison
// is constant time.
func (p *Password) Matches(plaintext string) (bool, error) {
	err := bcrypt.CompareHashAndPassword(p.hash, []byte(plaintext))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return false, nil
	default:
		return false, fmt.Errorf("comparing password: %w", err)
	}
}

// dummyHash is compared against when a login names an unknown email, so that
// response times don't reveal which emails are registered.
var dummyHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("not a real password"), bcryptCost)
	if err != nil {
		panic(err)
	}
	return hash
})

// SimulatePasswordCheck spends about as long as Password.Matches does.
func SimulatePasswordCheck(plaintext string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(plaintext))
}

func ValidateEmail(v *validator.Validator, email string) {
	v.Check(validator.NotBlank(email), "email", "must be provided")
	v.Check(validator.MaxChars(email, 254), "email", "must not be more than 254 characters long")
	v.Check(validator.Matches(email, validator.EmailRX), "email", "must be a valid email address")
}

func ValidatePasswordPlaintext(v *validator.Validator, password string) {
	v.Check(password != "", "password", "must be provided")
	v.Check(validator.MinChars(password, 8), "password", "must be at least 8 characters long")
	// bcrypt only uses the first 72 bytes, so longer passwords are rejected
	// rather than silently truncated.
	v.Check(len(password) <= 72, "password", "must not be more than 72 bytes long")
}

func ValidateUser(v *validator.Validator, user *User) {
	v.Check(validator.NotBlank(user.Name), "name", "must be provided")
	v.Check(validator.MaxChars(user.Name, 100), "name", "must not be more than 100 characters long")

	ValidateEmail(v, user.Email)
}

type UserModel struct {
	DB *sql.DB
}

// Insert creates user and fills in its ID, Version and timestamps.
func (m UserModel) Insert(ctx context.Context, user *User) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		INSERT INTO users (name, email, password_hash)
		VALUES (?, ?, ?)
		RETURNING id, version, created_at, updated_at`

	err := m.DB.QueryRowContext(ctx, query, user.Name, user.Email, user.Password.hash).
		Scan(&user.ID, &user.Version, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err, "users_email_uk") {
			return ErrDuplicateEmail
		}
		return fmt.Errorf("inserting user: %w", err)
	}

	return nil
}

func (m UserModel) Get(ctx context.Context, id int64) (*User, error) {
	return m.getBy(ctx, "id = ?", id)
}

// GetByEmail looks up a user by email, ignoring case.
func (m UserModel) GetByEmail(ctx context.Context, email string) (*User, error) {
	return m.getBy(ctx, "email = ?", email)
}

func (m UserModel) getBy(ctx context.Context, where string, arg any) (*User, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// where is always a constant from this file, never user input.
	query := `
		SELECT id, name, email, password_hash, version, created_at, updated_at
		FROM users
		WHERE ` + where

	var user User
	err := m.DB.QueryRowContext(ctx, query, arg).Scan(
		&user.ID, &user.Name, &user.Email, &user.Password.hash,
		&user.Version, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting user: %w", err)
	}

	return &user, nil
}

// GetForToken returns the user owning an unexpired token with the given scope
// and plaintext.
func (m UserModel) GetForToken(ctx context.Context, scope, tokenPlaintext string) (*User, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	hash := sha256.Sum256([]byte(tokenPlaintext))

	query := `
		SELECT u.id, u.name, u.email, u.password_hash, u.version, u.created_at, u.updated_at
		FROM users u
		INNER JOIN tokens t ON t.user_id = u.id
		WHERE t.hash = ? AND t.scope = ? AND t.expiry > ?`

	var user User
	err := m.DB.QueryRowContext(ctx, query, hash[:], scope, now()).Scan(
		&user.ID, &user.Name, &user.Email, &user.Password.hash,
		&user.Version, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting user for token: %w", err)
	}

	return &user, nil
}
