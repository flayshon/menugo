package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"menugo.flayshon.com/internal/validator"
)

// ErrInvalidCategory means a menu item referred to a category that doesn't
// exist in the item's restaurant.
var ErrInvalidCategory = errors.New("invalid category")

// MaxPriceCents is the highest price accepted for a menu item (100,000.00).
const MaxPriceCents = 10_000_000

// MenuItem is something a customer can order. IsAvailable is the "sold out"
// switch: unavailable items stay on the menu but can't be ordered.
type MenuItem struct {
	ID           int64
	RestaurantID int64
	CategoryID   int64
	Name         string
	Description  string
	PriceCents   int64
	ImageURL     string
	IsAvailable  bool
	SortOrder    int32
	Version      int32
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func ValidateMenuItem(v *validator.Validator, item *MenuItem) {
	v.Check(item.CategoryID > 0, "category_id", "must be provided")

	v.Check(validator.NotBlank(item.Name), "name", "must be provided")
	v.Check(validator.MaxChars(item.Name, 200), "name", "must not be more than 200 characters long")
	v.Check(validator.MaxChars(item.Description, 2000), "description", "must not be more than 2000 characters long")

	v.Check(item.PriceCents >= 0, "price_cents", "must not be negative")
	v.Check(item.PriceCents <= MaxPriceCents, "price_cents", fmt.Sprintf("must not be more than %d", MaxPriceCents))

	if item.ImageURL != "" {
		v.Check(validator.MaxChars(item.ImageURL, 2048), "image_url", "must not be more than 2048 characters long")
		v.Check(validator.IsHTTPURL(item.ImageURL), "image_url", "must be an absolute http or https URL")
	}

	v.Check(item.SortOrder >= 0 && item.SortOrder <= MaxSortOrder, "sort_order", fmt.Sprintf("must be between 0 and %d", MaxSortOrder))
}

type MenuItemModel struct {
	DB *sql.DB
}

const menuItemColumns = `id, restaurant_id, category_id, name, description, price_cents, image_url,
	is_available, sort_order, version, created_at, updated_at`

func scanMenuItem(row interface{ Scan(...any) error }) (*MenuItem, error) {
	var item MenuItem
	err := row.Scan(&item.ID, &item.RestaurantID, &item.CategoryID, &item.Name, &item.Description,
		&item.PriceCents, &item.ImageURL, &item.IsAvailable, &item.SortOrder, &item.Version,
		&item.CreatedAt, &item.UpdatedAt)
	return &item, err
}

// Insert creates item in restaurant item.RestaurantID. It returns
// ErrInvalidCategory unless item.CategoryID is a category of that restaurant.
func (m MenuItemModel) Insert(ctx context.Context, item *MenuItem) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		INSERT INTO menu_items (restaurant_id, category_id, name, description, price_cents, image_url, is_available, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, version, created_at, updated_at`

	args := []any{
		item.RestaurantID, item.CategoryID, item.Name, item.Description,
		item.PriceCents, item.ImageURL, item.IsAvailable, item.SortOrder,
	}

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(&item.ID, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if isForeignKeyViolation(err, "menu_items_category_fk") {
			return ErrInvalidCategory
		}
		return fmt.Errorf("inserting menu item: %w", err)
	}
	return nil
}

// Get returns item id of restaurantID, or ErrRecordNotFound.
func (m MenuItemModel) Get(ctx context.Context, restaurantID, id int64) (*MenuItem, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `SELECT ` + menuItemColumns + ` FROM menu_items WHERE restaurant_id = ? AND id = ?`

	item, err := scanMenuItem(m.DB.QueryRowContext(ctx, query, restaurantID, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting menu item: %w", err)
	}
	return item, nil
}

// List returns restaurantID's items in menu order. If categoryID is not
// zero, only items in that category are returned.
func (m MenuItemModel) List(ctx context.Context, restaurantID, categoryID int64) ([]*MenuItem, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		SELECT ` + menuItemColumns + `
		FROM menu_items
		WHERE restaurant_id = ? AND (? = 0 OR category_id = ?)
		ORDER BY category_id, sort_order, id`

	rows, err := m.DB.QueryContext(ctx, query, restaurantID, categoryID, categoryID)
	if err != nil {
		return nil, fmt.Errorf("listing menu items: %w", err)
	}
	defer rows.Close()

	items := []*MenuItem{}
	for rows.Next() {
		item, err := scanMenuItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning menu item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing menu items: %w", err)
	}
	return items, nil
}

// Update saves item if its version is unchanged since it was read; otherwise
// it returns ErrEditConflict. Moving it to a category of another restaurant,
// or one that doesn't exist, returns ErrInvalidCategory.
func (m MenuItemModel) Update(ctx context.Context, item *MenuItem) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	updatedAt := now()

	query := `
		UPDATE menu_items
		SET category_id = ?, name = ?, description = ?, price_cents = ?, image_url = ?,
		    is_available = ?, sort_order = ?, version = version + 1, updated_at = ?
		WHERE restaurant_id = ? AND id = ? AND version = ?`

	args := []any{
		item.CategoryID, item.Name, item.Description, item.PriceCents, item.ImageURL,
		item.IsAvailable, item.SortOrder, updatedAt,
		item.RestaurantID, item.ID, item.Version,
	}

	result, err := m.DB.ExecContext(ctx, query, args...)
	if err != nil {
		if isForeignKeyViolation(err, "menu_items_category_fk") {
			return ErrInvalidCategory
		}
		return fmt.Errorf("updating menu item: %w", err)
	}

	if err := expectOneRow(result, ErrEditConflict); err != nil {
		return err
	}

	item.Version++
	item.UpdatedAt = updatedAt
	return nil
}

// SetAvailability marks an item as available or sold out, regardless of its
// version: it's a single switch, so there's nothing to merge.
func (m MenuItemModel) SetAvailability(ctx context.Context, restaurantID, id int64, available bool) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		UPDATE menu_items
		SET is_available = ?, version = version + 1, updated_at = ?
		WHERE restaurant_id = ? AND id = ?`

	result, err := m.DB.ExecContext(ctx, query, available, now(), restaurantID, id)
	if err != nil {
		return fmt.Errorf("setting menu item availability: %w", err)
	}
	return expectOneRow(result, ErrRecordNotFound)
}

func (m MenuItemModel) Delete(ctx context.Context, restaurantID, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	result, err := m.DB.ExecContext(ctx, "DELETE FROM menu_items WHERE restaurant_id = ? AND id = ?", restaurantID, id)
	if err != nil {
		return fmt.Errorf("deleting menu item: %w", err)
	}
	return expectOneRow(result, ErrRecordNotFound)
}
