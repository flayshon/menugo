package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// DeliveryStatus is where a delivery is. It follows the order's status:
//
//	assigned   a driver has been assigned; the order isn't out yet
//	picked_up  the order is out for delivery
//	delivered  the order was delivered
//	unassigned the driver was taken off the order (reassigned or unassigned)
//	cancelled  the order was cancelled
type DeliveryStatus string

const (
	DeliveryAssigned   DeliveryStatus = "assigned"
	DeliveryPickedUp   DeliveryStatus = "picked_up"
	DeliveryDelivered  DeliveryStatus = "delivered"
	DeliveryUnassigned DeliveryStatus = "unassigned"
	DeliveryCancelled  DeliveryStatus = "cancelled"
)

// AllDeliveryStatuses lists every delivery status.
var AllDeliveryStatuses = []DeliveryStatus{DeliveryAssigned, DeliveryPickedUp, DeliveryDelivered, DeliveryUnassigned, DeliveryCancelled}

func (s DeliveryStatus) Valid() bool {
	return slices.Contains(AllDeliveryStatuses, s)
}

// driverOrderStatus maps the delivery statuses a driver may set to the order
// status that goes with them.
var driverOrderStatus = map[DeliveryStatus]OrderStatus{
	DeliveryPickedUp:  StatusOutForDelivery,
	DeliveryDelivered: StatusDelivered,
}

// DriverCanSet reports whether a driver may set a delivery to status s.
func DriverCanSet(s DeliveryStatus) bool {
	_, ok := driverOrderStatus[s]
	return ok
}

// assignableStatuses are the order statuses in which a driver can be
// assigned, reassigned or unassigned: after confirmation, before it leaves.
var assignableStatuses = []OrderStatus{StatusConfirmed, StatusPreparing, StatusReadyForDelivery}

var (
	// ErrDriverUnavailable means the driver doesn't exist in the
	// restaurant or is inactive.
	ErrDriverUnavailable = errors.New("driver unavailable")
	// ErrNoActiveDelivery means an order has no delivery in progress.
	ErrNoActiveDelivery = errors.New("no delivery in progress")
)

// NotAssignableError means the order can't have its driver changed now.
type NotAssignableError struct {
	Fulfillment Fulfillment
	Status      OrderStatus
}

func (e *NotAssignableError) Error() string {
	if e.Fulfillment != FulfillmentDelivery {
		return "pickup orders have no driver"
	}
	return fmt.Sprintf("drivers can only be changed while the order is confirmed, preparing or ready for delivery, not %s", e.Status)
}

// Delivery is one driver assignment for an order. The driver fields are
// empty if the driver's profile has since been deleted; the time fields are
// zero until they happen.
type Delivery struct {
	ID               int64
	RestaurantID     int64
	OrderID          int64
	DriverID         int64
	DriverName       string
	DriverPhone      string
	DriverVehicle    string
	Status           DeliveryStatus
	AssignedByUserID int64
	AssignedAt       time.Time
	PickedUpAt       time.Time
	DeliveredAt      time.Time
	EndedAt          time.Time
}

const deliverySelect = `
	SELECT dl.id, dl.restaurant_id, dl.order_id, dl.driver_id, COALESCE(d.name, ''), COALESCE(d.phone, ''),
	       COALESCE(d.vehicle, ''), dl.status, dl.assigned_by_user_id, dl.assigned_at, dl.picked_up_at,
	       dl.delivered_at, dl.ended_at
	FROM deliveries dl
	LEFT JOIN drivers d ON d.id = dl.driver_id`

func scanDelivery(row interface{ Scan(...any) error }) (*Delivery, error) {
	var dl Delivery
	var driverID, assignedBy sql.NullInt64
	var pickedUp, delivered, ended sql.NullTime
	err := row.Scan(&dl.ID, &dl.RestaurantID, &dl.OrderID, &driverID, &dl.DriverName, &dl.DriverPhone,
		&dl.DriverVehicle, &dl.Status, &assignedBy, &dl.AssignedAt, &pickedUp, &delivered, &ended)
	dl.DriverID = driverID.Int64
	dl.AssignedByUserID = assignedBy.Int64
	dl.PickedUpAt = pickedUp.Time
	dl.DeliveredAt = delivered.Time
	dl.EndedAt = ended.Time
	return &dl, err
}

// syncDelivery moves the order's delivery in progress, if any, along with
// the order's new status. Orders without a driver are left alone: a
// restaurant doesn't have to use driver assignment.
func syncDelivery(ctx context.Context, tx *sql.Tx, restaurantID, orderID int64, to OrderStatus, at time.Time) error {
	var query string
	var args []any

	switch to {
	case StatusOutForDelivery:
		query = "UPDATE deliveries SET status = 'picked_up', picked_up_at = ? WHERE restaurant_id = ? AND order_id = ? AND status = 'assigned'"
		args = []any{at, restaurantID, orderID}
	case StatusDelivered:
		query = `UPDATE deliveries SET status = 'delivered', delivered_at = ?, picked_up_at = COALESCE(picked_up_at, ?)
			WHERE restaurant_id = ? AND order_id = ? AND status IN ('assigned', 'picked_up')`
		args = []any{at, at, restaurantID, orderID}
	case StatusCancelled:
		query = "UPDATE deliveries SET status = 'cancelled', ended_at = ? WHERE restaurant_id = ? AND order_id = ? AND status IN ('assigned', 'picked_up')"
		args = []any{at, restaurantID, orderID}
	default:
		return nil
	}

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("updating delivery: %w", err)
	}
	return nil
}

type DeliveryModel struct {
	DB *sql.DB
}

// Assign makes driverID the driver of order orderID, both of restaurantID.
// If another driver was assigned, their delivery ends as unassigned.
// Assigning the current driver again changes nothing.
func (m DeliveryModel) Assign(ctx context.Context, restaurantID, orderID, driverID, byUserID int64) (*Delivery, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if err := lockAssignableOrder(ctx, tx, restaurantID, orderID); err != nil {
		return nil, err
	}

	// The shared lock keeps the driver from being deleted until we commit.
	var active bool
	err = tx.QueryRowContext(ctx,
		"SELECT is_active FROM drivers WHERE restaurant_id = ? AND id = ? LOCK IN SHARE MODE",
		restaurantID, driverID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !active) {
		return nil, ErrDriverUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("getting driver: %w", err)
	}

	var currentDriver sql.NullInt64
	err = tx.QueryRowContext(ctx,
		"SELECT driver_id FROM deliveries WHERE restaurant_id = ? AND order_id = ? AND status = 'assigned'",
		restaurantID, orderID).Scan(&currentDriver)
	switch {
	case err == nil && currentDriver.Int64 == driverID:
		// Already assigned to this driver.
	case err == nil || errors.Is(err, sql.ErrNoRows):
		at := now()
		if _, err := tx.ExecContext(ctx,
			"UPDATE deliveries SET status = 'unassigned', ended_at = ? WHERE restaurant_id = ? AND order_id = ? AND status = 'assigned'",
			at, restaurantID, orderID); err != nil {
			return nil, fmt.Errorf("unassigning previous driver: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO deliveries (restaurant_id, order_id, driver_id, status, assigned_by_user_id, assigned_at)
			VALUES (?, ?, ?, 'assigned', ?, ?)`,
			restaurantID, orderID, driverID, sql.NullInt64{Int64: byUserID, Valid: byUserID != 0}, at)
		if err != nil {
			return nil, fmt.Errorf("inserting delivery: %w", err)
		}
	default:
		return nil, fmt.Errorf("getting current delivery: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing delivery: %w", err)
	}

	return m.Current(ctx, restaurantID, orderID)
}

// Unassign takes the driver off order orderID of restaurantID. It returns
// ErrNoActiveDelivery if the order has no driver.
func (m DeliveryModel) Unassign(ctx context.Context, restaurantID, orderID int64) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if err := lockAssignableOrder(ctx, tx, restaurantID, orderID); err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx,
		"UPDATE deliveries SET status = 'unassigned', ended_at = ? WHERE restaurant_id = ? AND order_id = ? AND status = 'assigned'",
		now(), restaurantID, orderID)
	if err != nil {
		return fmt.Errorf("unassigning driver: %w", err)
	}
	if err := expectOneRow(result, ErrNoActiveDelivery); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing unassignment: %w", err)
	}
	return nil
}

// lockAssignableOrder locks the order and checks its driver can be changed.
func lockAssignableOrder(ctx context.Context, tx *sql.Tx, restaurantID, orderID int64) error {
	order, err := lockOrder(ctx, tx, restaurantID, orderID)
	if err != nil {
		return err
	}
	if order.Fulfillment != FulfillmentDelivery || !slices.Contains(assignableStatuses, order.Status) {
		return &NotAssignableError{Fulfillment: order.Fulfillment, Status: order.Status}
	}
	return nil
}

// Current returns the order's latest delivery that wasn't unassigned: the
// one in progress, or how it ended. It returns ErrRecordNotFound if there is
// none.
func (m DeliveryModel) Current(ctx context.Context, restaurantID, orderID int64) (*Delivery, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	return currentDelivery(ctx, m.DB, restaurantID, orderID)
}

func currentDelivery(ctx context.Context, q querier, restaurantID, orderID int64) (*Delivery, error) {
	dl, err := scanDelivery(q.QueryRowContext(ctx, deliverySelect+`
		WHERE dl.restaurant_id = ? AND dl.order_id = ? AND dl.status <> 'unassigned'
		ORDER BY dl.id DESC
		LIMIT 1`, restaurantID, orderID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting delivery: %w", err)
	}
	return dl, nil
}

// DriverDelivery is a delivery as its driver sees it: with the order and
// the restaurant to collect it from.
type DriverDelivery struct {
	Delivery
	Order      *Order
	Restaurant *Restaurant
}

// ListForDriver returns the deliveries assigned to userID, in any of the
// restaurants they drive for, newest first. If statuses isn't empty, only
// deliveries in those statuses are returned.
func (m DeliveryModel) ListForDriver(ctx context.Context, userID int64, statuses []DeliveryStatus, p Pagination) ([]*DriverDelivery, Metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	where := "d.user_id = ?"
	args := []any{userID}
	if len(statuses) > 0 {
		where += " AND dl.status IN (" + strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",") + ")"
		for _, s := range statuses {
			args = append(args, s)
		}
	}
	args = append(args, p.limit(), p.offset())

	rows, err := m.DB.QueryContext(ctx, `
		SELECT COUNT(*) OVER(), dl.id
		FROM deliveries dl
		INNER JOIN drivers d ON d.id = dl.driver_id
		WHERE `+where+`
		ORDER BY dl.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, Metadata{}, fmt.Errorf("listing deliveries: %w", err)
	}
	defer rows.Close()

	total := 0
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&total, &id); err != nil {
			return nil, Metadata{}, fmt.Errorf("scanning delivery: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, Metadata{}, fmt.Errorf("listing deliveries: %w", err)
	}

	deliveries := make([]*DriverDelivery, 0, len(ids))
	for _, id := range ids {
		dd, err := m.getForDriver(ctx, userID, id)
		if err != nil {
			return nil, Metadata{}, err
		}
		deliveries = append(deliveries, dd)
	}

	return deliveries, calculateMetadata(total, p), nil
}

// GetForDriver returns delivery id if it is assigned to userID, or
// ErrRecordNotFound.
func (m DeliveryModel) GetForDriver(ctx context.Context, userID, id int64) (*DriverDelivery, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	return m.getForDriver(ctx, userID, id)
}

func (m DeliveryModel) getForDriver(ctx context.Context, userID, id int64) (*DriverDelivery, error) {
	dl, err := scanDelivery(m.DB.QueryRowContext(ctx, deliverySelect+` WHERE dl.id = ? AND d.user_id = ?`, id, userID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting delivery: %w", err)
	}

	order, err := OrderModel{DB: m.DB}.getOrder(ctx, "restaurant_id = ? AND id = ?", dl.RestaurantID, dl.OrderID)
	if err != nil {
		return nil, err
	}
	restaurant, err := RestaurantModel{DB: m.DB}.Get(ctx, dl.RestaurantID)
	if err != nil {
		return nil, err
	}

	return &DriverDelivery{Delivery: *dl, Order: order, Restaurant: restaurant}, nil
}

// UpdateByDriver lets the driver userID move their delivery id to status to
// (picked_up or delivered; see DriverCanSet). The order moves with it, under
// the same rules as any other status change. It returns ErrRecordNotFound
// unless the delivery is assigned to userID, and ErrNoActiveDelivery if it is
// no longer in progress (e.g. it was reassigned).
func (m DeliveryModel) UpdateByDriver(ctx context.Context, userID, id int64, to DeliveryStatus) error {
	orderStatus, ok := driverOrderStatus[to]
	if !ok {
		return fmt.Errorf("drivers cannot set deliveries to %s", to)
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// Find the order first, without locks...
	var restaurantID, orderID int64
	err := m.DB.QueryRowContext(ctx, `
		SELECT dl.restaurant_id, dl.order_id FROM deliveries dl
		INNER JOIN drivers d ON d.id = dl.driver_id
		WHERE dl.id = ? AND d.user_id = ?`, id, userID).Scan(&restaurantID, &orderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRecordNotFound
		}
		return fmt.Errorf("getting delivery: %w", err)
	}

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// ...then lock the order before the delivery, in the same order as every
	// other change, and check the delivery is still this driver's and in
	// progress now that nothing can change it.
	if _, err := lockOrder(ctx, tx, restaurantID, orderID); err != nil {
		return err
	}

	var stillActive bool
	err = tx.QueryRowContext(ctx, `
		SELECT dl.status IN ('assigned', 'picked_up') FROM deliveries dl
		INNER JOIN drivers d ON d.id = dl.driver_id
		WHERE dl.id = ? AND d.user_id = ?
		FOR UPDATE`, id, userID).Scan(&stillActive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRecordNotFound
		}
		return fmt.Errorf("locking delivery: %w", err)
	}
	if !stillActive {
		return ErrNoActiveDelivery
	}

	if _, err := transitionTx(ctx, tx, restaurantID, orderID, orderStatus, Actor{Type: ActorUser, UserID: userID}, ""); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing delivery update: %w", err)
	}
	return nil
}
