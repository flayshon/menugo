package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Event types.
const (
	EventOrderPlaced        = "order.placed"
	EventOrderStatusChanged = "order.status_changed"
	EventDeliveryAssigned   = "delivery.assigned"
	EventDeliveryUnassigned = "delivery.unassigned"
)

// Event records that something happened to an order. Events are written in
// the same transaction as the change, so there is an event exactly when the
// change was committed.
type Event struct {
	ID           int64
	RestaurantID int64
	OrderID      int64
	Type         string
	OrderStatus  OrderStatus // the order's status after the change
	DriverUserID int64       // see the events table; 0 if none
	CreatedAt    time.Time
}

func recordEvent(ctx context.Context, tx *sql.Tx, e Event) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO events (restaurant_id, order_id, type, order_status, driver_user_id)
		VALUES (?, ?, ?, ?, ?)`,
		e.RestaurantID, e.OrderID, e.Type, e.OrderStatus,
		sql.NullInt64{Int64: e.DriverUserID, Valid: e.DriverUserID != 0})
	if err != nil {
		return fmt.Errorf("recording %s event: %w", e.Type, err)
	}
	return nil
}

// activeDriverUserID returns the user ID of the order's driver while a
// delivery is in progress, or 0.
func activeDriverUserID(ctx context.Context, tx *sql.Tx, restaurantID, orderID int64) (int64, error) {
	var userID int64
	err := tx.QueryRowContext(ctx, `
		SELECT d.user_id FROM deliveries dl
		INNER JOIN drivers d ON d.id = dl.driver_id
		WHERE dl.restaurant_id = ? AND dl.order_id = ? AND dl.status IN ('assigned', 'picked_up')`,
		restaurantID, orderID).Scan(&userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("getting order's driver: %w", err)
	}
	return userID, nil
}

type EventModel struct {
	DB *sql.DB
}

const eventColumns = `id, restaurant_id, order_id, type, order_status, driver_user_id, created_at`

func (m EventModel) query(ctx context.Context, query string, args ...any) ([]Event, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var driver sql.NullInt64
		if err := rows.Scan(&e.ID, &e.RestaurantID, &e.OrderID, &e.Type, &e.OrderStatus, &driver, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		e.DriverUserID = driver.Int64
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	return events, nil
}

// Recent returns events created in the last window, by the database's clock,
// oldest first. If there are more than limit, it returns the newest limit.
//
// Pollers read a window rather than "everything after the last ID seen":
// IDs are assigned when a row is inserted but become visible when its
// transaction commits, so a lower ID can appear after a higher one. The
// window must be longer than any transaction (queryTimeout bounds them), and
// pollers must skip IDs they have already seen.
func (m EventModel) Recent(ctx context.Context, window time.Duration, limit int) ([]Event, error) {
	events, err := m.query(ctx, `
		SELECT `+eventColumns+` FROM events
		WHERE created_at >= NOW(6) - INTERVAL ? MICROSECOND
		ORDER BY id DESC
		LIMIT ?`, window.Microseconds(), limit)
	slices.Reverse(events)
	return events, err
}

// ForRestaurantAfter returns restaurantID's events with IDs above afterID,
// oldest first, at most limit of them. It lets a reconnecting client catch
// up on what it missed.
func (m EventModel) ForRestaurantAfter(ctx context.Context, restaurantID, afterID int64, limit int) ([]Event, error) {
	return m.query(ctx, `
		SELECT `+eventColumns+` FROM events
		WHERE restaurant_id = ? AND id > ?
		ORDER BY id
		LIMIT ?`, restaurantID, afterID, limit)
}

// DeleteOlderThan removes events older than age.
func (m EventModel) DeleteOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	result, err := m.DB.ExecContext(ctx,
		"DELETE FROM events WHERE created_at < NOW(6) - INTERVAL ? MICROSECOND", age.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("deleting old events: %w", err)
	}
	return result.RowsAffected()
}
