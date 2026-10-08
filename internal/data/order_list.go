package data

import (
	"context"
	"fmt"
	"strings"
	"time"

	"menugo.flayshon.com/internal/validator"
)

// OrderFilters selects and orders the orders a list returns. Zero values mean
// "no filter".
type OrderFilters struct {
	Statuses    []OrderStatus
	Fulfillment Fulfillment
	From        time.Time // placed at or after
	To          time.Time // placed before
	OldestFirst bool      // default is newest first
	Pagination
}

func ValidateOrderFilters(v *validator.Validator, f OrderFilters) {
	for _, s := range f.Statuses {
		v.Check(s.Valid(), "status", fmt.Sprintf("%q is not a valid order status", s))
	}
	if f.Fulfillment != "" {
		v.Check(f.Fulfillment.Valid(), "fulfillment", "must be delivery or pickup")
	}
	if !f.From.IsZero() && !f.To.IsZero() {
		v.Check(f.From.Before(f.To), "to", "must be after from")
	}
	ValidatePagination(v, f.Pagination)
}

// List returns a page of restaurantID's orders matching f, with their items
// but not their history.
func (m OrderModel) List(ctx context.Context, restaurantID int64, f OrderFilters) ([]*Order, Metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// Every condition is a constant from this function; only args come from
	// the client.
	where := []string{"restaurant_id = ?"}
	args := []any{restaurantID}

	if len(f.Statuses) > 0 {
		where = append(where, "status IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.Statuses)), ",")+")")
		for _, s := range f.Statuses {
			args = append(args, s)
		}
	}
	if f.Fulfillment != "" {
		where = append(where, "fulfillment = ?")
		args = append(args, f.Fulfillment)
	}
	if !f.From.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.From.UTC())
	}
	if !f.To.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, f.To.UTC())
	}

	order := "created_at DESC, id DESC"
	if f.OldestFirst {
		order = "created_at ASC, id ASC"
	}

	query := `
		SELECT COUNT(*) OVER(), ` + orderColumns + `
		FROM orders
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + order + `
		LIMIT ? OFFSET ?`
	args = append(args, f.limit(), f.offset())

	rows, err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, Metadata{}, fmt.Errorf("listing orders: %w", err)
	}
	defer rows.Close()

	totalRecords := 0
	orders := []*Order{}
	for rows.Next() {
		var total int
		o, err := scanOrder(scannerWithPrefix{rows, &total})
		if err != nil {
			return nil, Metadata{}, fmt.Errorf("scanning order: %w", err)
		}
		totalRecords = total
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, Metadata{}, fmt.Errorf("listing orders: %w", err)
	}

	if err := m.attachItems(ctx, restaurantID, orders); err != nil {
		return nil, Metadata{}, err
	}

	return orders, calculateMetadata(totalRecords, f.Pagination), nil
}

// scannerWithPrefix scans extra leading columns before the ones a scan
// function expects.
type scannerWithPrefix struct {
	row    interface{ Scan(...any) error }
	prefix any
}

func (s scannerWithPrefix) Scan(dest ...any) error {
	return s.row.Scan(append([]any{s.prefix}, dest...)...)
}

// attachItems loads the items of all orders in one query.
func (m OrderModel) attachItems(ctx context.Context, restaurantID int64, orders []*Order) error {
	if len(orders) == 0 {
		return nil
	}

	byID := make(map[int64]*Order, len(orders))
	args := []any{restaurantID}
	for _, o := range orders {
		byID[o.ID] = o
		o.Items = []OrderItem{}
		args = append(args, o.ID)
	}

	rows, err := m.DB.QueryContext(ctx, `
		SELECT order_id, id, menu_item_id, name, unit_price_cents, quantity, line_total_cents, notes
		FROM order_items
		WHERE restaurant_id = ? AND order_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(orders)), ",")+`)
		ORDER BY id`, args...)
	if err != nil {
		return fmt.Errorf("listing order items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var orderID int64
		item, err := scanOrderItem(scannerWithPrefix{rows, &orderID})
		if err != nil {
			return fmt.Errorf("scanning order item: %w", err)
		}
		byID[orderID].Items = append(byID[orderID].Items, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing order items: %w", err)
	}
	return nil
}
