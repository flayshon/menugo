package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"menugo.flayshon.com/internal/testdb"
)

func TestStreamTickets(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	u := insertUser(t, m, "alice@example.com")
	auth, err := m.Tokens.New(ctx, u.ID, time.Hour, ScopeAuthentication)
	if err != nil {
		t.Fatal(err)
	}

	ticket, err := m.Tokens.NewStreamTicket(ctx, auth.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.UserID != u.ID || time.Until(ticket.Expiry) > StreamTicketTTL || len(ticket.Plaintext) != 26 {
		t.Errorf("ticket = %+v", ticket)
	}

	if _, err := m.Users.GetForToken(ctx, ScopeAuthentication, ticket.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("a ticket must not work as an authentication token: %v", err)
	}

	parent, err := m.Tokens.ConsumeStreamTicket(ctx, ticket.Plaintext)
	if err != nil || string(parent) != string(auth.Hash) {
		t.Fatalf("consume = %x, %v; want the parent's hash", parent, err)
	}
	if _, err := m.Tokens.ConsumeStreamTicket(ctx, ticket.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("second use: err = %v; want ErrRecordNotFound", err)
	}

	t.Run("expires no later than its parent", func(t *testing.T) {
		short, err := m.Tokens.New(ctx, u.ID, 10*time.Second, ScopeAuthentication)
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := m.Tokens.NewStreamTicket(ctx, short.Hash)
		if err != nil {
			t.Fatal(err)
		}
		if !ticket.Expiry.Equal(short.Expiry) {
			t.Errorf("expiry = %v; want the parent's %v", ticket.Expiry, short.Expiry)
		}
	})

	t.Run("logging out deletes unused tickets", func(t *testing.T) {
		ticket, err := m.Tokens.NewStreamTicket(ctx, auth.Hash)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Tokens.Delete(ctx, ScopeAuthentication, auth.Plaintext); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Tokens.ConsumeStreamTicket(ctx, ticket.Plaintext); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("err = %v; want ErrRecordNotFound", err)
		}
		if _, err := m.Tokens.NewStreamTicket(ctx, auth.Hash); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("issuing from a revoked token: err = %v", err)
		}
	})

	t.Run("expired tickets don't work", func(t *testing.T) {
		auth, _ := m.Tokens.New(ctx, u.ID, time.Hour, ScopeAuthentication)
		ticket, err := m.Tokens.NewStreamTicket(ctx, auth.Hash)
		if err != nil {
			t.Fatal(err)
		}
		m.Tokens.DB.Exec("UPDATE tokens SET expiry = ? WHERE hash = ?", now().Add(-time.Second), ticket.Hash)
		if _, err := m.Tokens.ConsumeStreamTicket(ctx, ticket.Plaintext); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("err = %v; want ErrRecordNotFound", err)
		}
	})
}
