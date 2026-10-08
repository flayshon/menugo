package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"menugo.flayshon.com/internal/data"
)

func (ts *testServer) streamTicket(t *testing.T, token string) string {
	t.Helper()
	res := ts.do(t, http.MethodPost, "/v1/tokens/stream", token, nil)
	assertStatus(t, res, http.StatusCreated)
	return decode[struct {
		Ticket struct {
			Ticket string    `json:"ticket"`
			Expiry time.Time `json:"expiry"`
		} `json:"stream_ticket"`
	}](t, res.body).Ticket.Ticket
}

func TestStreamTicketsAPI(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	s.app.streams.recheckInterval = 50 * time.Millisecond
	startPolling(t, s.app)

	assertStatus(t, s.ts.do(t, http.MethodPost, "/v1/tokens/stream", "", nil), http.StatusUnauthorized)

	ticket := s.ts.streamTicket(t, s.owner)
	stream := s.ts.openStream(t, s.path("/events?ticket="+ticket), "", nil)

	s.placeOrder(t, s.deliveryOrder())
	placed := stream.next(t)
	if placed.name != data.EventOrderPlaced {
		t.Errorf("event = %+v", placed)
	}

	t.Run("single use", func(t *testing.T) {
		assertStatus(t, s.ts.do(t, http.MethodGet, s.path("/events?ticket="+ticket), "", nil), http.StatusUnauthorized)
	})

	t.Run("only for streams", func(t *testing.T) {
		fresh := s.ts.streamTicket(t, s.owner)
		assertStatus(t, s.ts.do(t, http.MethodGet, s.path("?ticket="+fresh), "", nil), http.StatusUnauthorized)
		assertStatus(t, s.ts.do(t, http.MethodGet, s.path(""), fresh, nil), http.StatusUnauthorized)
	})

	t.Run("not both a header and a ticket", func(t *testing.T) {
		fresh := s.ts.streamTicket(t, s.owner)
		assertStatus(t, s.ts.do(t, http.MethodGet, s.path("/events?ticket="+fresh), s.owner, nil), http.StatusBadRequest)
	})

	t.Run("catching up with last_event_id", func(t *testing.T) {
		id := s.newestOrderID(t)
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "confirmed"}), http.StatusOK)
		stream.next(t) // the confirmation, live

		replay := s.ts.openStream(t, s.path("/events?last_event_id="+placed.id+"&ticket="+s.ts.streamTicket(t, s.owner)), "", nil)
		if e := replay.next(t); e.name != data.EventOrderStatusChanged {
			t.Errorf("replayed %+v", e)
		}
	})

	t.Run("driver stream", func(t *testing.T) {
		_, token := s.newDriver(t, "joao@example.com", "João")
		s.ts.openStream(t, "/v1/me/events?ticket="+s.ts.streamTicket(t, token), "", nil)
	})

	t.Run("logging out ends ticket streams", func(t *testing.T) {
		_, token := s.ts.signUp(t, "bob@example.com")
		stream := s.ts.openStream(t, "/v1/me/events?ticket="+s.ts.streamTicket(t, token), "", nil)
		assertStatus(t, s.ts.do(t, http.MethodDelete, "/v1/tokens/authentication", token, nil), http.StatusNoContent)
		if e := stream.next(t); e.name != "end" || !strings.Contains(e.data, "revoked") {
			t.Errorf("event = %+v; want end", e)
		}
	})
}
