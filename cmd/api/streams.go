package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/events"
	"menugo.flayshon.com/internal/validator"
)

// streamSettings tunes real-time event delivery. Tests shorten them.
type streamSettings struct {
	pollInterval      time.Duration // how often this instance checks for new events
	heartbeatInterval time.Duration // comment lines that keep proxies from closing idle streams
	recheckInterval   time.Duration // how often a stream re-checks the client's access
	maxDuration       time.Duration // streams end after this; clients reconnect
}

var defaultStreamSettings = streamSettings{
	pollInterval:      time.Second,
	heartbeatInterval: 25 * time.Second,
	recheckInterval:   time.Minute,
	maxDuration:       30 * time.Minute,
}

const (
	// eventWindow is how far back the poller looks on every poll; it must
	// be longer than any transaction that writes events (see
	// data.EventModel.Recent).
	eventWindow = 10 * time.Second
	// eventPollLimit caps one poll. Reaching it means more than this many
	// events in eventWindow, and some may be missed: it is logged.
	eventPollLimit = 5000
	// eventRetention is how long events are kept, which bounds how far back
	// a reconnecting client can catch up.
	eventRetention = 24 * time.Hour
	// replayLimit caps how many missed events a reconnecting client gets.
	replayLimit = 1000
	// subscriptionBuffer is how many events may wait for a slow client
	// before its stream is dropped (it then reconnects and catches up).
	subscriptionBuffer = 64
)

// eventPoller publishes events committed by any API instance to this
// instance's broker.
type eventPoller struct {
	app  *application
	seen map[int64]time.Time // published IDs, kept for twice eventWindow
}

func (app *application) newEventPoller() *eventPoller {
	return &eventPoller{app: app, seen: make(map[int64]time.Time)}
}

// pollEvents polls for events until ctx is cancelled.
func (app *application) pollEvents(ctx context.Context) {
	p := app.newEventPoller()
	p.prime(ctx)
	p.run(ctx)
}

// prime marks the events already in the window as seen without publishing
// them: they happened before this instance started listening.
func (p *eventPoller) prime(ctx context.Context) {
	if err := p.poll(ctx, false); err != nil && ctx.Err() == nil {
		p.app.logger.Error("polling events", "error", err)
	}
}

func (p *eventPoller) run(ctx context.Context) {
	ticker := time.NewTicker(p.app.streams.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.poll(ctx, true); err != nil && ctx.Err() == nil {
				p.app.logger.Error("polling events", "error", err)
			}
		}
	}
}

// poll reads the recent window of events and publishes those not published
// before.
func (p *eventPoller) poll(ctx context.Context, publish bool) error {
	recent, err := p.app.models.Events.Recent(ctx, eventWindow, eventPollLimit)
	if err != nil {
		return err
	}
	if len(recent) == eventPollLimit {
		p.app.logger.Warn("event poll limit reached; some real-time updates may be missed", "limit", eventPollLimit)
	}

	now := time.Now()
	for _, e := range recent {
		if _, ok := p.seen[e.ID]; ok {
			continue
		}
		p.seen[e.ID] = now
		if publish {
			p.app.broker.Publish(e)
		}
	}

	for id, at := range p.seen {
		if now.Sub(at) > 2*eventWindow {
			delete(p.seen, id)
		}
	}
	return nil
}

func (app *application) cleanupOldEvents(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := app.models.Events.DeleteOlderThan(ctx, eventRetention); err != nil && ctx.Err() == nil {
				app.logger.Error("deleting old events", "error", err)
			}
		}
	}
}

// closeStreams ends every open stream, so that graceful shutdown doesn't
// wait for clients that never hang up. The server calls it on Shutdown.
func (app *application) closeStreams() {
	app.closeStreamsOnce.Do(func() { close(app.streamsClosed) })
}

// sseWriter writes server-sent events.
type sseWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

// startSSE sends the headers for an event stream. Streams outlive the
// server's read and write timeouts, so those are lifted for this request.
func (app *application) startSSE(w http.ResponseWriter) (*sseWriter, error) {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("lifting read deadline: %w", err)
	}
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("lifting write deadline: %w", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no") // ask nginx not to buffer
	w.WriteHeader(http.StatusOK)

	s := &sseWriter{w: w, rc: rc}
	// Ask clients to wait 3s before reconnecting after a drop.
	if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
		return nil, err
	}
	return s, rc.Flush()
}

// event sends one event. id 0 means "no id": the client's Last-Event-ID
// doesn't change.
func (s *sseWriter) event(id int64, name string, payload any) error {
	js, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if id > 0 {
		if _, err := fmt.Fprintf(s.w, "id: %d\n", id); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, js); err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s *sseWriter) comment(text string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return err
	}
	return s.rc.Flush()
}

// stream sends events from sub through send until the client goes away,
// send reports it's done, sub is dropped, access is revoked (recheck returns
// false; nil means never re-check), the stream reaches its maximum duration,
// or the server shuts down.
func (app *application) stream(r *http.Request, sse *sseWriter, sub *events.Subscription,
	recheck func(context.Context) bool, send func(data.Event) (done bool, err error)) {

	heartbeat := time.NewTicker(app.streams.heartbeatInterval)
	defer heartbeat.Stop()
	recheckTicker := time.NewTicker(app.streams.recheckInterval)
	defer recheckTicker.Stop()
	deadline := time.NewTimer(app.streams.maxDuration)
	defer deadline.Stop()

	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				return // dropped for falling behind; the client reconnects
			}
			done, err := send(e)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					app.logError(r, err)
				}
				return
			}
			if done {
				return
			}
		case <-heartbeat.C:
			if sse.comment("ping") != nil {
				return
			}
		case <-recheckTicker.C:
			if recheck != nil && !recheck(r.Context()) {
				sse.event(0, "end", map[string]string{"reason": "access revoked"})
				return
			}
		case <-deadline.C:
			return
		case <-app.streamsClosed:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// tokenStillValid re-checks the request's bearer token, for long-lived
// streams: a logged-out or expired token ends the stream.
func (app *application) tokenStillValid(ctx context.Context, r *http.Request) (*data.User, bool) {
	token, _, _ := bearerToken(r)
	user, err := app.models.Users.GetForToken(ctx, data.ScopeAuthentication, token)
	return user, err == nil
}

type restaurantEvent struct {
	OrderID      int64            `json:"order_id"`
	OrderStatus  data.OrderStatus `json:"order_status"`
	DriverUserID *int64           `json:"driver_user_id"`
	At           time.Time        `json:"at"`
}

// restaurantEventsHandler streams what happens to the restaurant's orders:
// order.placed, order.status_changed, delivery.assigned and
// delivery.unassigned. Events are small; clients fetch details through the
// REST API. Reconnecting clients (Last-Event-ID) first get what they missed.
func (app *application) restaurantEventsHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var lastID int64
	if s := r.Header.Get("Last-Event-ID"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 0 {
			app.badRequestResponse(w, r, errors.New("invalid Last-Event-ID header"))
			return
		}
		lastID = id
	}

	// Subscribe before reading missed events, so nothing falls in between.
	sub := app.broker.Subscribe(func(e data.Event) bool { return e.RestaurantID == ms.RestaurantID }, subscriptionBuffer)
	defer app.broker.Unsubscribe(sub)

	var missed []data.Event
	if lastID > 0 {
		var err error
		missed, err = app.models.Events.ForRestaurantAfter(r.Context(), ms.RestaurantID, lastID, replayLimit)
		if err != nil {
			app.serverErrorResponse(w, r, err)
			return
		}
	}

	sse, err := app.startSSE(w)
	if err != nil {
		app.logError(r, err)
		return
	}

	sent := make(map[int64]bool)
	send := func(e data.Event) (bool, error) {
		if sent[e.ID] {
			return false, nil
		}
		sent[e.ID] = true
		return false, sse.event(e.ID, e.Type, restaurantEvent{e.OrderID, e.OrderStatus, nonZero(e.DriverUserID), e.CreatedAt})
	}

	for _, e := range missed {
		if _, err := send(e); err != nil {
			return
		}
	}

	recheck := func(ctx context.Context) bool {
		user, ok := app.tokenStillValid(ctx, r)
		if !ok {
			return false
		}
		current, err := app.models.Memberships.Get(ctx, ms.RestaurantID, user.ID)
		return err == nil && validator.PermittedValue(current.Role, data.StaffRoles...)
	}

	app.stream(r, sse, sub, recheck, send)
}

// trackingEventsHandler streams a customer's order: the current state at
// once, then again after every change. It ends when the order is finished.
// No credentials are involved, so a browser's EventSource can use it from
// any origin.
func (app *application) trackingEventsHandler(w http.ResponseWriter, r *http.Request) {
	order, restaurant := app.getTrackedOrder(w, r)
	if order == nil {
		return
	}

	sub := app.broker.Subscribe(func(e data.Event) bool { return e.OrderID == order.ID }, subscriptionBuffer)
	defer app.broker.Unsubscribe(sub)

	sse, err := app.startSSE(w)
	if err != nil {
		app.logError(r, err)
		return
	}

	finished := func(o *data.Order) bool {
		return len(data.NextStatuses(o.Fulfillment, o.Status)) == 0
	}

	// Events from just before the snapshot can still arrive after it, so
	// only send the order when what the customer sees has changed.
	last := newPublicOrderResponse(order, restaurant)
	if err := sse.event(0, "order", last); err != nil || finished(order) {
		return
	}

	send := func(e data.Event) (bool, error) {
		current, err := app.models.Orders.Get(r.Context(), order.RestaurantID, order.ID)
		if err != nil {
			return true, err
		}
		view := newPublicOrderResponse(current, restaurant)
		if !reflect.DeepEqual(view, last) {
			if err := sse.event(e.ID, "order", view); err != nil {
				return true, err
			}
			last = view
		}
		return finished(current), nil
	}

	app.stream(r, sse, sub, nil, send)
}

type driverEvent struct {
	OrderID      int64            `json:"order_id"`
	RestaurantID int64            `json:"restaurant_id"`
	OrderStatus  data.OrderStatus `json:"order_status"`
	At           time.Time        `json:"at"`
}

// myEventsHandler streams events for the authenticated driver: deliveries
// assigned to or taken from them, and status changes of orders they are
// delivering. Clients fetch details from /v1/me/deliveries.
func (app *application) myEventsHandler(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)

	sub := app.broker.Subscribe(func(e data.Event) bool { return e.DriverUserID == user.ID }, subscriptionBuffer)
	defer app.broker.Unsubscribe(sub)

	sse, err := app.startSSE(w)
	if err != nil {
		app.logError(r, err)
		return
	}

	recheck := func(ctx context.Context) bool {
		_, ok := app.tokenStillValid(ctx, r)
		return ok
	}
	send := func(e data.Event) (bool, error) {
		return false, sse.event(e.ID, e.Type, driverEvent{e.OrderID, e.RestaurantID, e.OrderStatus, e.CreatedAt})
	}

	app.stream(r, sse, sub, recheck, send)
}
