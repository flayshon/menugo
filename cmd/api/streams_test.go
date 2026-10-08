package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"menugo.flayshon.com/internal/data"
)

type sseEvent struct {
	id    string
	name  string
	data  string
	ended bool // the server closed the stream
}

// sseStream reads server-sent events from an open response.
type sseStream struct {
	res      *http.Response
	events   chan sseEvent
	comments chan string
}

// openStream connects to an event stream and starts reading it.
func (ts *testServer) openStream(t *testing.T, path, token string, header http.Header) *sseStream {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })

	if res.StatusCode != http.StatusOK {
		t.Fatalf("stream %s: status %d", path, res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	s := &sseStream{res: res, events: make(chan sseEvent, 100), comments: make(chan string, 100)}
	go s.read()
	return s
}

func (s *sseStream) read() {
	defer close(s.events)

	var e sseEvent
	sc := bufio.NewScanner(s.res.Body)
	for sc.Scan() {
		line := sc.Text()
		field, value, _ := strings.Cut(line, ": ")
		switch {
		case line == "":
			if e.name != "" {
				s.events <- e
			}
			e = sseEvent{}
		case strings.HasPrefix(line, ":"):
			select {
			case s.comments <- strings.TrimPrefix(line, ": "):
			default:
			}
		case field == "id":
			e.id = value
		case field == "event":
			e.name = value
		case field == "data":
			e.data = value
		}
	}
}

// next returns the next event, failing the test if none arrives in time. If
// the server closed the stream, it returns an event with ended set.
func (s *sseStream) next(t *testing.T) sseEvent {
	t.Helper()
	select {
	case e, ok := <-s.events:
		if !ok {
			return sseEvent{ended: true}
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s")
		return sseEvent{}
	}
}

// expectNothing fails if an event arrives within d.
func (s *sseStream) expectNothing(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case e, ok := <-s.events:
		if ok {
			t.Errorf("unexpected event %+v", e)
		}
	case <-time.After(d):
	}
}

func decodeEvent[T any](t *testing.T, e sseEvent) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(e.data), &v); err != nil {
		t.Fatalf("decoding %q: %v", e.data, err)
	}
	return v
}

// startPolling runs the event poller for the test, polling every 20ms. It
// returns once the poller has caught up, so later events are published.
func startPolling(t *testing.T, app *application) {
	t.Helper()
	app.streams.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p := app.newEventPoller()
	p.prime(ctx)
	go p.run(ctx)
}

func TestRestaurantEventStream(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	startPolling(t, s.app)

	_, stranger := s.ts.signUp(t, "stranger@example.com")
	strangers := s.ts.createRestaurant(t, stranger, "strangers")

	res := s.ts.do(t, http.MethodGet, s.path("/events"), stranger, nil)
	assertStatus(t, res, http.StatusNotFound)

	stream := s.ts.openStream(t, s.path("/events"), s.owner, nil)
	otherStream := s.ts.openStream(t, fmt.Sprintf("/v1/restaurants/%d/events", strangers.ID), stranger, nil)

	s.placeOrder(t, s.deliveryOrder())
	placed := stream.next(t)
	got := decodeEvent[restaurantEvent](t, placed)
	if placed.name != data.EventOrderPlaced || got.OrderStatus != data.StatusPending || got.OrderID == 0 || placed.id == "" {
		t.Errorf("event = %+v (%+v)", placed, got)
	}

	assertStatus(t, s.setStatus(t, s.owner, got.OrderID, map[string]any{"status": "confirmed"}), http.StatusOK)
	confirmed := stream.next(t)
	if confirmed.name != data.EventOrderStatusChanged || decodeEvent[restaurantEvent](t, confirmed).OrderStatus != data.StatusConfirmed {
		t.Errorf("event = %+v", confirmed)
	}

	otherStream.expectNothing(t, 200*time.Millisecond)

	t.Run("reconnecting clients catch up", func(t *testing.T) {
		replay := s.ts.openStream(t, s.path("/events"), s.owner, http.Header{"Last-Event-ID": {placed.id}})
		e := replay.next(t)
		if e.id != confirmed.id || e.name != data.EventOrderStatusChanged {
			t.Errorf("replayed %+v; want the confirmation", e)
		}
	})
}

func TestTrackingEventStream(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	startPolling(t, s.app)

	placed := s.placeOrder(t, s.deliveryOrder())
	id := s.newestOrderID(t)

	stream := s.ts.openStream(t, "/v1/tracking/"+placed.TrackingToken+"/events", "", nil)
	if stream.res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("tracking streams should allow any origin")
	}

	snapshot := stream.next(t)
	if snapshot.name != "order" || decodeEvent[publicOrderResponse](t, snapshot).Status != data.StatusPending {
		t.Errorf("snapshot = %+v", snapshot)
	}
	if strings.Contains(snapshot.data, "Ana") {
		t.Errorf("tracking stream exposes customer details: %s", snapshot.data)
	}

	assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "confirmed"}), http.StatusOK)
	if o := decodeEvent[publicOrderResponse](t, stream.next(t)); o.Status != data.StatusConfirmed || o.CanCancel {
		t.Errorf("after confirmation = %+v", o)
	}

	assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "cancelled", "reason": "out of dough"}), http.StatusOK)
	if o := decodeEvent[publicOrderResponse](t, stream.next(t)); o.Status != data.StatusCancelled {
		t.Errorf("after cancellation = %+v", o)
	}

	// Finished orders end the stream.
	if e := stream.next(t); !e.ended {
		t.Errorf("stream should end after the order is finished, got %+v", e)
	}

	t.Run("finished orders end at once", func(t *testing.T) {
		stream := s.ts.openStream(t, "/v1/tracking/"+placed.TrackingToken+"/events", "", nil)
		if e := stream.next(t); e.name != "order" {
			t.Errorf("first = %+v; want the snapshot", e)
		}
		if e := stream.next(t); !e.ended {
			t.Errorf("then = %+v; want the end", e)
		}
	})

	assertStatus(t, s.ts.do(t, http.MethodGet, "/v1/tracking/"+strings.Repeat("A", 26)+"/events", "", nil), http.StatusNotFound)
}

func TestDriverEventStream(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	startPolling(t, s.app)

	joao, joaoToken := s.newDriver(t, "joao@example.com", "João")
	maria, _ := s.newDriver(t, "maria@example.com", "Maria")
	orderID, _ := s.orderIn(t, "confirmed")

	stream := s.ts.openStream(t, "/v1/me/events", joaoToken, nil)

	assertStatus(t, s.assign(t, s.owner, orderID, joao.ID), http.StatusOK)
	e := stream.next(t)
	if got := decodeEvent[driverEvent](t, e); e.name != data.EventDeliveryAssigned || got.OrderID != orderID || got.RestaurantID != s.restaurant.ID {
		t.Errorf("event = %+v", e)
	}

	assertStatus(t, s.setStatus(t, s.owner, orderID, map[string]any{"status": "preparing"}), http.StatusOK)
	if e := stream.next(t); e.name != data.EventOrderStatusChanged || decodeEvent[driverEvent](t, e).OrderStatus != data.StatusPreparing {
		t.Errorf("event = %+v", e)
	}

	assertStatus(t, s.assign(t, s.owner, orderID, maria.ID), http.StatusOK)
	if e := stream.next(t); e.name != data.EventDeliveryUnassigned {
		t.Errorf("event = %+v", e)
	}

	// No longer João's order: he hears nothing more about it.
	assertStatus(t, s.setStatus(t, s.owner, orderID, map[string]any{"status": "ready_for_delivery"}), http.StatusOK)
	stream.expectNothing(t, 200*time.Millisecond)
}

func TestStreamEndsWhenAccessIsRevoked(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	s.app.streams.recheckInterval = 50 * time.Millisecond

	staffID, staff := s.ts.signUp(t, "staff@example.com")
	assertStatus(t, s.ts.addMember(t, s.owner, s.restaurant.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)

	stream := s.ts.openStream(t, s.path("/events"), staff, nil)
	assertStatus(t, s.ts.do(t, http.MethodDelete, s.path(fmt.Sprintf("/members/%d", staffID)), s.owner, nil), http.StatusNoContent)

	if e := stream.next(t); e.name != "end" || !strings.Contains(e.data, "revoked") {
		t.Errorf("event = %+v; want end", e)
	}
	if e := stream.next(t); !e.ended {
		t.Errorf("stream should be closed, got %+v", e)
	}

	t.Run("logging out ends driver streams", func(t *testing.T) {
		_, token := s.ts.signUp(t, "driver@example.com")
		stream := s.ts.openStream(t, "/v1/me/events", token, nil)
		assertStatus(t, s.ts.do(t, http.MethodDelete, "/v1/tokens/authentication", token, nil), http.StatusNoContent)
		if e := stream.next(t); e.name != "end" {
			t.Errorf("event = %+v; want end", e)
		}
	})
}

func TestStreamHeartbeatAndShutdown(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	s.app.streams.heartbeatInterval = 30 * time.Millisecond

	stream := s.ts.openStream(t, s.path("/events"), s.owner, nil)

	select {
	case c := <-stream.comments:
		if c != "ping" {
			t.Errorf("comment = %q", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no heartbeat")
	}

	s.app.closeStreams()
	if e := stream.next(t); !e.ended {
		t.Errorf("stream should end on shutdown, got %+v", e)
	}
}

// TestStreamsOutliveServerTimeouts runs a server with read and write
// timeouts far shorter than the stream, as in production.
func TestStreamsOutliveServerTimeouts(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	startPolling(t, s.app)

	srv := httptest.NewUnstartedServer(s.app.routes())
	srv.Config.ReadTimeout = 100 * time.Millisecond
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	ts := &testServer{srv}

	stream := ts.openStream(t, s.path("/events"), s.owner, nil)
	time.Sleep(300 * time.Millisecond)

	s.placeOrder(t, s.deliveryOrder())
	if e := stream.next(t); e.name != data.EventOrderPlaced {
		t.Errorf("event = %+v", e)
	}
}

func TestPollerCatchesLateCommits(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ctx := context.Background()

	sub := app.broker.Subscribe(func(data.Event) bool { return true }, 10)
	p := app.newEventPoller()
	p.prime(ctx)

	insert := `INSERT INTO events (restaurant_id, order_id, type, order_status) VALUES (1, ?, 'order.placed', 'pending')`

	// Transaction A takes a lower ID but commits after B.
	txA, err := app.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback()
	if _, err := txA.Exec(insert, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec(insert, 2); err != nil {
		t.Fatal(err)
	}

	p.poll(ctx, true)
	if err := txA.Commit(); err != nil {
		t.Fatal(err)
	}
	p.poll(ctx, true)
	p.poll(ctx, true) // nothing new: no duplicates

	var orders []string
	for len(sub.C) > 0 {
		orders = append(orders, strconv.FormatInt((<-sub.C).OrderID, 10))
	}
	if strings.Join(orders, ",") != "2,1" {
		t.Errorf("published orders %v; want 2 then the late 1, once each", orders)
	}
}
