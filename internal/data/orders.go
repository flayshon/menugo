package data

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"menugo.flayshon.com/internal/validator"
)

const (
	maxOrderLines    = 50
	maxLineQuantity  = 99
	trackingTokenLen = 26
)

// ItemUnavailableError means an order asked for an item that can't be
// ordered: it doesn't exist in the restaurant, its category is hidden, or
// it's sold out.
type ItemUnavailableError struct {
	MenuItemID int64
}

func (e *ItemUnavailableError) Error() string {
	return fmt.Sprintf("menu item %d is not available", e.MenuItemID)
}

// BelowMinimumOrderError means a delivery order's subtotal is below its
// zone's minimum.
type BelowMinimumOrderError struct {
	MinOrderCents int64
}

func (e *BelowMinimumOrderError) Error() string {
	return fmt.Sprintf("the minimum order for this address is %d", e.MinOrderCents)
}

// TotalMismatchError means the total the customer was shown is no longer
// the real total, e.g. because a price changed while they were ordering.
type TotalMismatchError struct {
	TotalCents int64
}

func (e *TotalMismatchError) Error() string {
	return fmt.Sprintf("the order total is now %d", e.TotalCents)
}

// Address is where a delivery goes. PostalCode is normalized.
type Address struct {
	Line       string
	Details    string // apartment, reference point, etc.
	City       string
	PostalCode string
}

// OrderLine is one line of an order request.
type OrderLine struct {
	MenuItemID int64
	Quantity   int
	Notes      string
}

// OrderRequest is what a customer submits to place an order.
type OrderRequest struct {
	Fulfillment   Fulfillment
	CustomerName  string
	CustomerPhone string // normalized with NormalizePhone
	CustomerEmail string
	Address       Address
	Lines         []OrderLine
	Notes         string
	// ExpectedTotalCents, if set, is the total the customer agreed to; the
	// order is refused if the real total differs.
	ExpectedTotalCents *int64
}

type OrderItem struct {
	ID             int64
	MenuItemID     int64 // 0 if the menu item has since been deleted
	Name           string
	UnitPriceCents int64
	Quantity       int
	LineTotalCents int64
	Notes          string
}

// StatusChange is one entry of an order's status history.
type StatusChange struct {
	From        OrderStatus // empty for the order being placed
	To          OrderStatus
	ActorType   ActorType
	ActorUserID int64
	Reason      string // why the order was cancelled; empty otherwise
	At          time.Time
}

type Order struct {
	ID               int64
	RestaurantID     int64
	CustomerID       int64
	Fulfillment      Fulfillment
	Status           OrderStatus
	CustomerName     string
	CustomerPhone    string
	CustomerEmail    string
	Address          Address
	DeliveryZoneID   int64 // 0 for pickup, or if the zone was deleted
	Notes            string
	Currency         string
	SubtotalCents    int64
	DeliveryFeeCents int64
	TotalCents       int64
	Version          int32
	CreatedAt        time.Time
	UpdatedAt        time.Time

	Items    []OrderItem
	History  []StatusChange
	Delivery *Delivery // the current delivery (see DeliveryModel.Current), if any; only on single orders
}

var phoneRX = regexp.MustCompile(`^\+?[0-9]{8,15}$`)

// NormalizePhone removes spaces and punctuation from a phone number, keeping
// a leading "+" and the digits.
func NormalizePhone(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for i, r := range s {
		if (r >= '0' && r <= '9') || (r == '+' && i == 0) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ValidateOrderRequest(v *validator.Validator, req *OrderRequest) {
	v.Check(req.Fulfillment.Valid(), "fulfillment", "must be delivery or pickup")

	v.Check(validator.NotBlank(req.CustomerName), "customer.name", "must be provided")
	v.Check(validator.MaxChars(req.CustomerName, 100), "customer.name", "must not be more than 100 characters long")
	v.Check(req.CustomerPhone != "", "customer.phone", "must be provided")
	v.Check(validator.Matches(req.CustomerPhone, phoneRX), "customer.phone", "must be a phone number with 8 to 15 digits")
	if req.CustomerEmail != "" {
		v.Check(validator.MaxChars(req.CustomerEmail, 254), "customer.email", "must not be more than 254 characters long")
		v.Check(validator.Matches(req.CustomerEmail, validator.EmailRX), "customer.email", "must be a valid email address")
	}

	a := req.Address
	switch req.Fulfillment {
	case FulfillmentDelivery:
		v.Check(validator.NotBlank(a.Line), "address.line", "must be provided")
		v.Check(validator.MaxChars(a.Line, 255), "address.line", "must not be more than 255 characters long")
		v.Check(validator.MaxChars(a.Details, 255), "address.details", "must not be more than 255 characters long")
		v.Check(validator.NotBlank(a.City), "address.city", "must be provided")
		v.Check(validator.MaxChars(a.City, 100), "address.city", "must not be more than 100 characters long")
		v.Check(len(a.PostalCode) >= minPostalCodeLen && len(a.PostalCode) <= maxPostalCodeLen,
			"address.postal_code", "must be a valid postal code")
	case FulfillmentPickup:
		v.Check(a == Address{}, "address", "must not be provided for pickup orders")
	}

	v.Check(len(req.Lines) > 0, "items", "must contain at least one item")
	v.Check(len(req.Lines) <= maxOrderLines, "items", fmt.Sprintf("must not contain more than %d lines", maxOrderLines))
	for i, line := range req.Lines {
		key := fmt.Sprintf("items[%d]", i)
		v.Check(line.MenuItemID > 0, key+".item_id", "must be provided")
		v.Check(line.Quantity >= 1 && line.Quantity <= maxLineQuantity, key+".quantity", fmt.Sprintf("must be between 1 and %d", maxLineQuantity))
		v.Check(validator.MaxChars(line.Notes, 200), key+".notes", "must not be more than 200 characters long")
	}

	v.Check(validator.MaxChars(req.Notes, 500), "notes", "must not be more than 500 characters long")
	if req.ExpectedTotalCents != nil {
		v.Check(*req.ExpectedTotalCents >= 0, "expected_total_cents", "must not be negative")
	}
}

// orderableItem is what pricing needs to know about a menu item.
type orderableItem struct {
	ID              int64
	Name            string
	PriceCents      int64
	IsAvailable     bool
	CategoryVisible bool
}

// priceLines turns order lines into order items using the menu's current
// prices, and returns them with the subtotal. Prices from the client are
// never used.
func priceLines(lines []OrderLine, menu map[int64]orderableItem) ([]OrderItem, int64, error) {
	items := make([]OrderItem, 0, len(lines))
	var subtotal int64

	for _, line := range lines {
		mi, ok := menu[line.MenuItemID]
		if !ok || !mi.IsAvailable || !mi.CategoryVisible {
			return nil, 0, &ItemUnavailableError{MenuItemID: line.MenuItemID}
		}

		lineTotal := mi.PriceCents * int64(line.Quantity)
		items = append(items, OrderItem{
			MenuItemID:     mi.ID,
			Name:           mi.Name,
			UnitPriceCents: mi.PriceCents,
			Quantity:       line.Quantity,
			LineTotalCents: lineTotal,
			Notes:          line.Notes,
		})
		subtotal += lineTotal
	}

	return items, subtotal, nil
}

type OrderModel struct {
	DB *sql.DB
}

// Create places an order for restaurant, which must be published, and
// returns it with the plaintext tracking token. Everything happens in one
// transaction: either the whole order is stored, or nothing is.
func (m OrderModel) Create(ctx context.Context, restaurant *Restaurant, req *OrderRequest) (*Order, string, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Re-check the restaurant inside the transaction, with a shared lock:
	// deleting a restaurant takes an exclusive lock on this row, so an order
	// can't be placed while it's being deleted.
	var live, accepting bool
	var timezone string
	err = tx.QueryRowContext(ctx,
		"SELECT is_published AND deleted_at IS NULL, accepting_orders, timezone FROM restaurants WHERE id = ? LOCK IN SHARE MODE",
		restaurant.ID).Scan(&live, &accepting, &timezone)
	if err != nil || !live {
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrRecordNotFound
		}
		return nil, "", fmt.Errorf("checking restaurant: %w", err)
	}

	hours, err := openingHours(ctx, tx, restaurant.ID)
	if err != nil {
		return nil, "", err
	}
	current := Restaurant{Timezone: timezone}
	if !accepting || !IsOpen(hours, current.Location(), time.Now()) {
		return nil, "", ErrRestaurantClosed
	}

	menu, err := orderableItems(ctx, tx, restaurant.ID, req.Lines)
	if err != nil {
		return nil, "", err
	}

	items, subtotal, err := priceLines(req.Lines, menu)
	if err != nil {
		return nil, "", err
	}

	order := &Order{
		RestaurantID:  restaurant.ID,
		Fulfillment:   req.Fulfillment,
		Status:        StatusPending,
		CustomerName:  req.CustomerName,
		CustomerPhone: req.CustomerPhone,
		CustomerEmail: req.CustomerEmail,
		Address:       req.Address,
		Notes:         req.Notes,
		Currency:      restaurant.Currency,
		SubtotalCents: subtotal,
		Items:         items,
	}

	if req.Fulfillment == FulfillmentDelivery {
		zone, err := findZoneForPostalCode(ctx, tx, restaurant.ID, req.Address.PostalCode)
		if err != nil {
			return nil, "", err
		}
		if subtotal < zone.MinOrderCents {
			return nil, "", &BelowMinimumOrderError{MinOrderCents: zone.MinOrderCents}
		}
		order.DeliveryZoneID = zone.ID
		order.DeliveryFeeCents = zone.FeeCents
	}

	order.TotalCents = order.SubtotalCents + order.DeliveryFeeCents

	if req.ExpectedTotalCents != nil && *req.ExpectedTotalCents != order.TotalCents {
		return nil, "", &TotalMismatchError{TotalCents: order.TotalCents}
	}

	order.CustomerID, err = upsertCustomer(ctx, tx, restaurant.ID, req)
	if err != nil {
		return nil, "", err
	}

	token := rand.Text()
	tokenHash := sha256.Sum256([]byte(token))
	placedAt := now()

	err = tx.QueryRowContext(ctx, `
		INSERT INTO orders (
			restaurant_id, customer_id, tracking_token_hash, fulfillment, status,
			customer_name, customer_phone, customer_email,
			address_line, address_details, address_city, address_postal_code, delivery_zone_id,
			notes, currency, subtotal_cents, delivery_fee_cents, total_cents, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, version, created_at, updated_at`,
		order.RestaurantID, order.CustomerID, tokenHash[:], order.Fulfillment, order.Status,
		order.CustomerName, order.CustomerPhone, order.CustomerEmail,
		order.Address.Line, order.Address.Details, order.Address.City, order.Address.PostalCode,
		sql.NullInt64{Int64: order.DeliveryZoneID, Valid: order.DeliveryZoneID != 0},
		order.Notes, order.Currency, order.SubtotalCents, order.DeliveryFeeCents, order.TotalCents,
		placedAt, placedAt,
	).Scan(&order.ID, &order.Version, &order.CreatedAt, &order.UpdatedAt)
	if err != nil {
		return nil, "", fmt.Errorf("inserting order: %w", err)
	}

	if err := insertOrderItems(ctx, tx, order); err != nil {
		return nil, "", err
	}

	placed := StatusChange{To: StatusPending, ActorType: ActorCustomer, At: placedAt}
	if err := insertStatusChange(ctx, tx, order, placed); err != nil {
		return nil, "", err
	}

	event := Event{RestaurantID: order.RestaurantID, OrderID: order.ID, Type: EventOrderPlaced, OrderStatus: StatusPending}
	if err := recordEvent(ctx, tx, event); err != nil {
		return nil, "", err
	}
	order.History = []StatusChange{placed}

	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("committing order: %w", err)
	}

	return order, token, nil
}

// orderableItems loads the menu items an order refers to, from restaurantID
// only, keyed by ID.
func orderableItems(ctx context.Context, tx *sql.Tx, restaurantID int64, lines []OrderLine) (map[int64]orderableItem, error) {
	args := []any{restaurantID}
	for _, line := range lines {
		args = append(args, line.MenuItemID)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(lines)), ",")

	rows, err := tx.QueryContext(ctx, `
		SELECT mi.id, mi.name, mi.price_cents, mi.is_available, mc.is_visible
		FROM menu_items mi
		INNER JOIN menu_categories mc ON mc.restaurant_id = mi.restaurant_id AND mc.id = mi.category_id
		WHERE mi.restaurant_id = ? AND mi.id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("loading ordered items: %w", err)
	}
	defer rows.Close()

	menu := make(map[int64]orderableItem)
	for rows.Next() {
		var mi orderableItem
		if err := rows.Scan(&mi.ID, &mi.Name, &mi.PriceCents, &mi.IsAvailable, &mi.CategoryVisible); err != nil {
			return nil, fmt.Errorf("scanning ordered item: %w", err)
		}
		menu[mi.ID] = mi
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading ordered items: %w", err)
	}
	return menu, nil
}

// upsertCustomer returns the ID of the restaurant's customer with the
// request's phone number, creating one if needed. Existing customers' details
// are left alone: anyone can type any phone number, so an order must not be
// able to rewrite someone else's record. The order keeps its own copy.
func upsertCustomer(ctx context.Context, tx *sql.Tx, restaurantID int64, req *OrderRequest) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO customers (restaurant_id, name, phone, email)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`,
		restaurantID, req.CustomerName, req.CustomerPhone, req.CustomerEmail)
	if err != nil {
		return 0, fmt.Errorf("saving customer: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("saving customer: %w", err)
	}
	return id, nil
}

func insertOrderItems(ctx context.Context, tx *sql.Tx, order *Order) error {
	placeholders := strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?, ?, ?, ?),", len(order.Items)), ",")
	args := make([]any, 0, 8*len(order.Items))
	for _, item := range order.Items {
		args = append(args, order.RestaurantID, order.ID, item.MenuItemID, item.Name,
			item.UnitPriceCents, item.Quantity, item.LineTotalCents, item.Notes)
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO order_items (restaurant_id, order_id, menu_item_id, name, unit_price_cents, quantity, line_total_cents, notes)
		VALUES `+placeholders, args...)
	if err != nil {
		return fmt.Errorf("inserting order items: %w", err)
	}
	return nil
}

func insertStatusChange(ctx context.Context, tx *sql.Tx, order *Order, c StatusChange) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO order_status_history (restaurant_id, order_id, from_status, to_status, actor_type, actor_user_id, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		order.RestaurantID, order.ID,
		sql.NullString{String: string(c.From), Valid: c.From != ""},
		c.To, c.ActorType,
		sql.NullInt64{Int64: c.ActorUserID, Valid: c.ActorUserID != 0},
		c.Reason, c.At)
	if err != nil {
		return fmt.Errorf("recording order status change: %w", err)
	}
	return nil
}

const orderColumns = `id, restaurant_id, customer_id, fulfillment, status, customer_name, customer_phone,
	customer_email, address_line, address_details, address_city, address_postal_code, delivery_zone_id,
	notes, currency, subtotal_cents, delivery_fee_cents, total_cents, version, created_at, updated_at`

func scanOrder(row interface{ Scan(...any) error }) (*Order, error) {
	var o Order
	var zoneID sql.NullInt64
	err := row.Scan(&o.ID, &o.RestaurantID, &o.CustomerID, &o.Fulfillment, &o.Status,
		&o.CustomerName, &o.CustomerPhone, &o.CustomerEmail,
		&o.Address.Line, &o.Address.Details, &o.Address.City, &o.Address.PostalCode, &zoneID,
		&o.Notes, &o.Currency, &o.SubtotalCents, &o.DeliveryFeeCents, &o.TotalCents,
		&o.Version, &o.CreatedAt, &o.UpdatedAt)
	o.DeliveryZoneID = zoneID.Int64
	return &o, err
}

// Get returns order id of restaurantID with its items and history.
func (m OrderModel) Get(ctx context.Context, restaurantID, id int64) (*Order, error) {
	return m.getOrder(ctx, "restaurant_id = ? AND id = ?", restaurantID, id)
}

// GetByTrackingToken returns the order a customer's tracking token belongs
// to, with its items and history.
func (m OrderModel) GetByTrackingToken(ctx context.Context, token string) (*Order, error) {
	if len(token) != trackingTokenLen {
		return nil, ErrRecordNotFound
	}
	hash := sha256.Sum256([]byte(token))
	return m.getOrder(ctx, "tracking_token_hash = ?", hash[:])
}

func (m OrderModel) getOrder(ctx context.Context, where string, args ...any) (*Order, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// where is always a constant from this file, never user input.
	order, err := scanOrder(m.DB.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE `+where, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting order: %w", err)
	}

	if order.Items, err = m.items(ctx, order); err != nil {
		return nil, err
	}
	if order.History, err = m.history(ctx, order); err != nil {
		return nil, err
	}
	order.Delivery, err = currentDelivery(ctx, m.DB, order.RestaurantID, order.ID)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return nil, err
	}
	return order, nil
}

func (m OrderModel) items(ctx context.Context, order *Order) ([]OrderItem, error) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT id, menu_item_id, name, unit_price_cents, quantity, line_total_cents, notes
		FROM order_items
		WHERE restaurant_id = ? AND order_id = ?
		ORDER BY id`, order.RestaurantID, order.ID)
	if err != nil {
		return nil, fmt.Errorf("listing order items: %w", err)
	}
	defer rows.Close()

	items := []OrderItem{}
	for rows.Next() {
		item, err := scanOrderItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning order item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing order items: %w", err)
	}
	return items, nil
}

// scanOrderItem scans id, menu_item_id, name, unit_price_cents, quantity,
// line_total_cents, notes.
func scanOrderItem(row interface{ Scan(...any) error }) (OrderItem, error) {
	var item OrderItem
	var menuItemID sql.NullInt64
	err := row.Scan(&item.ID, &menuItemID, &item.Name, &item.UnitPriceCents,
		&item.Quantity, &item.LineTotalCents, &item.Notes)
	item.MenuItemID = menuItemID.Int64
	return item, err
}

func (m OrderModel) history(ctx context.Context, order *Order) ([]StatusChange, error) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT from_status, to_status, actor_type, actor_user_id, reason, created_at
		FROM order_status_history
		WHERE restaurant_id = ? AND order_id = ?
		ORDER BY id`, order.RestaurantID, order.ID)
	if err != nil {
		return nil, fmt.Errorf("listing order history: %w", err)
	}
	defer rows.Close()

	history := []StatusChange{}
	for rows.Next() {
		var c StatusChange
		var from sql.NullString
		var userID sql.NullInt64
		if err := rows.Scan(&from, &c.To, &c.ActorType, &userID, &c.Reason, &c.At); err != nil {
			return nil, fmt.Errorf("scanning order history: %w", err)
		}
		c.From = OrderStatus(from.String)
		c.ActorUserID = userID.Int64
		history = append(history, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing order history: %w", err)
	}
	return history, nil
}

// Transition moves order id of restaurantID to status to, if the rules allow
// actor to (see CanTransition), and records the change and reason (see
// ValidateStatusChange) in its history. The order's delivery, if it has one
// in progress, follows: see syncDelivery.
func (m OrderModel) Transition(ctx context.Context, restaurantID, id int64, to OrderStatus, actor Actor, reason string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := transitionTx(ctx, tx, restaurantID, id, to, actor, reason); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing order status change: %w", err)
	}
	return nil
}

// lockOrder locks order id of restaurantID until tx ends and returns its
// kind and status. Everything that changes an order or its deliveries locks
// the order first, so they never deadlock with each other.
func lockOrder(ctx context.Context, tx *sql.Tx, restaurantID, id int64) (*Order, error) {
	order := &Order{RestaurantID: restaurantID, ID: id}
	err := tx.QueryRowContext(ctx,
		"SELECT fulfillment, status FROM orders WHERE restaurant_id = ? AND id = ? FOR UPDATE",
		restaurantID, id).Scan(&order.Fulfillment, &order.Status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("locking order: %w", err)
	}
	return order, nil
}

// transitionTx does the work of Transition inside tx and returns the status
// the order had before.
func transitionTx(ctx context.Context, tx *sql.Tx, restaurantID, id int64, to OrderStatus, actor Actor, reason string) (OrderStatus, error) {
	order, err := lockOrder(ctx, tx, restaurantID, id)
	if err != nil {
		return "", err
	}

	if !CanTransition(order.Fulfillment, order.Status, to, actor.Type) {
		return "", &InvalidTransitionError{From: order.Status, To: to}
	}

	changedAt := now()

	_, err = tx.ExecContext(ctx,
		"UPDATE orders SET status = ?, version = version + 1, updated_at = ? WHERE restaurant_id = ? AND id = ?",
		to, changedAt, restaurantID, id)
	if err != nil {
		return "", fmt.Errorf("updating order status: %w", err)
	}

	change := StatusChange{From: order.Status, To: to, ActorType: actor.Type, ActorUserID: actor.UserID, Reason: reason, At: changedAt}
	if err := insertStatusChange(ctx, tx, order, change); err != nil {
		return "", err
	}

	// Before syncDelivery, which may end the delivery.
	driverUserID, err := activeDriverUserID(ctx, tx, restaurantID, id)
	if err != nil {
		return "", err
	}

	if err := syncDelivery(ctx, tx, restaurantID, id, to, changedAt); err != nil {
		return "", err
	}

	event := Event{RestaurantID: restaurantID, OrderID: id, Type: EventOrderStatusChanged, OrderStatus: to, DriverUserID: driverUserID}
	if err := recordEvent(ctx, tx, event); err != nil {
		return "", err
	}

	return order.Status, nil
}
