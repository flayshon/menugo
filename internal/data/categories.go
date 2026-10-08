package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"menugo.flayshon.com/internal/validator"
)

var (
	ErrDuplicateCategoryName = errors.New("duplicate category name")
	ErrCategoryNotEmpty      = errors.New("category has items")
)

// MaxSortOrder bounds sort_order values for categories and items.
const MaxSortOrder = 1_000_000

// Category groups menu items, e.g. "Pizzas" or "Drinks". Hidden categories
// (IsVisible false) are left out of the public menu along with their items.
type Category struct {
	ID           int64
	RestaurantID int64
	Name         string
	Description  string
	SortOrder    int32
	IsVisible    bool
	Version      int32
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func ValidateCategory(v *validator.Validator, c *Category) {
	v.Check(validator.NotBlank(c.Name), "name", "must be provided")
	v.Check(validator.MaxChars(c.Name, 100), "name", "must not be more than 100 characters long")
	v.Check(validator.MaxChars(c.Description, 500), "description", "must not be more than 500 characters long")
	v.Check(c.SortOrder >= 0 && c.SortOrder <= MaxSortOrder, "sort_order", fmt.Sprintf("must be between 0 and %d", MaxSortOrder))
}

type CategoryModel struct {
	DB *sql.DB
}

const categoryColumns = `id, restaurant_id, name, description, sort_order, is_visible, version, created_at, updated_at`

func scanCategory(row interface{ Scan(...any) error }) (*Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.RestaurantID, &c.Name, &c.Description, &c.SortOrder,
		&c.IsVisible, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	return &c, err
}

// Insert creates c in restaurant c.RestaurantID.
func (m CategoryModel) Insert(ctx context.Context, c *Category) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		INSERT INTO menu_categories (restaurant_id, name, description, sort_order, is_visible)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id, version, created_at, updated_at`

	err := m.DB.QueryRowContext(ctx, query, c.RestaurantID, c.Name, c.Description, c.SortOrder, c.IsVisible).
		Scan(&c.ID, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err, "menu_categories_restaurant_name_uk") {
			return ErrDuplicateCategoryName
		}
		return fmt.Errorf("inserting category: %w", err)
	}
	return nil
}

// Get returns category id of restaurantID, or ErrRecordNotFound if there is
// no such category in that restaurant.
func (m CategoryModel) Get(ctx context.Context, restaurantID, id int64) (*Category, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `SELECT ` + categoryColumns + ` FROM menu_categories WHERE restaurant_id = ? AND id = ?`

	c, err := scanCategory(m.DB.QueryRowContext(ctx, query, restaurantID, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting category: %w", err)
	}
	return c, nil
}

// List returns all of restaurantID's categories in menu order.
func (m CategoryModel) List(ctx context.Context, restaurantID int64) ([]*Category, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		SELECT ` + categoryColumns + `
		FROM menu_categories
		WHERE restaurant_id = ?
		ORDER BY sort_order, id`

	rows, err := m.DB.QueryContext(ctx, query, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("listing categories: %w", err)
	}
	defer rows.Close()

	categories := []*Category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning category: %w", err)
		}
		categories = append(categories, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing categories: %w", err)
	}
	return categories, nil
}

// Update saves c if its version is unchanged since it was read; otherwise it
// returns ErrEditConflict.
func (m CategoryModel) Update(ctx context.Context, c *Category) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	updatedAt := now()

	query := `
		UPDATE menu_categories
		SET name = ?, description = ?, sort_order = ?, is_visible = ?, version = version + 1, updated_at = ?
		WHERE restaurant_id = ? AND id = ? AND version = ?`

	result, err := m.DB.ExecContext(ctx, query,
		c.Name, c.Description, c.SortOrder, c.IsVisible, updatedAt,
		c.RestaurantID, c.ID, c.Version)
	if err != nil {
		if isDuplicateKey(err, "menu_categories_restaurant_name_uk") {
			return ErrDuplicateCategoryName
		}
		return fmt.Errorf("updating category: %w", err)
	}

	if err := expectOneRow(result, ErrEditConflict); err != nil {
		return err
	}

	c.Version++
	c.UpdatedAt = updatedAt
	return nil
}

// Delete removes an empty category. It returns ErrCategoryNotEmpty if the
// category still has items, so they are never deleted as a side effect.
func (m CategoryModel) Delete(ctx context.Context, restaurantID, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Locking the category blocks concurrent inserts of items into it (their
	// foreign key check needs a shared lock on this row) until we're done.
	var locked int64
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM menu_categories WHERE restaurant_id = ? AND id = ? FOR UPDATE",
		restaurantID, id).Scan(&locked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRecordNotFound
		}
		return fmt.Errorf("locking category: %w", err)
	}

	var hasItems bool
	err = tx.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM menu_items WHERE restaurant_id = ? AND category_id = ?)",
		restaurantID, id).Scan(&hasItems)
	if err != nil {
		return fmt.Errorf("checking category items: %w", err)
	}
	if hasItems {
		return ErrCategoryNotEmpty
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM menu_categories WHERE restaurant_id = ? AND id = ?", restaurantID, id)
	if err != nil {
		return fmt.Errorf("deleting category: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing category deletion: %w", err)
	}
	return nil
}
