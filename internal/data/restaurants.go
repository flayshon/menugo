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
	ErrDuplicateSlug = errors.New("duplicate slug")
	// ErrRestaurantBusy means a restaurant can't be deleted because it has
	// orders in progress.
	ErrRestaurantBusy = errors.New("restaurant has orders in progress")
)

type Restaurant struct {
	ID          int64
	Slug        string
	Name        string
	Description string
	Phone       string
	Email       string
	AddressLine string
	City        string
	PostalCode  string
	Currency    string
	Timezone    string // IANA name; opening hours are in this zone
	// IsPublished makes the restaurant's menu visible to the public.
	IsPublished bool
	// AcceptingOrders pauses ordering when false, whatever the opening hours.
	AcceptingOrders bool
	Version         int32
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// RestaurantWithRole is a restaurant as seen by one of its members.
type RestaurantWithRole struct {
	Restaurant
	Role Role
}

// SupportedCurrencies are the ISO 4217 codes a restaurant may price in. All of
// them have two minor units, so amounts are stored in cents.
var SupportedCurrencies = []string{"BRL", "USD", "EUR", "GBP", "CAD", "AUD"}

func ValidateRestaurant(v *validator.Validator, r *Restaurant) {
	v.Check(validator.NotBlank(r.Name), "name", "must be provided")
	v.Check(validator.MaxChars(r.Name, 200), "name", "must not be more than 200 characters long")

	v.Check(r.Slug != "", "slug", "must be provided")
	v.Check(validator.MinChars(r.Slug, 3), "slug", "must be at least 3 characters long")
	v.Check(validator.MaxChars(r.Slug, 63), "slug", "must not be more than 63 characters long")
	v.Check(validator.Matches(r.Slug, validator.SlugRX), "slug", "must contain only lowercase letters, digits and single hyphens")

	v.Check(validator.MaxChars(r.Description, 2000), "description", "must not be more than 2000 characters long")
	v.Check(validator.MaxChars(r.Phone, 30), "phone", "must not be more than 30 characters long")
	if r.Email != "" {
		v.Check(validator.MaxChars(r.Email, 254), "email", "must not be more than 254 characters long")
		v.Check(validator.Matches(r.Email, validator.EmailRX), "email", "must be a valid email address")
	}
	v.Check(validator.MaxChars(r.AddressLine, 255), "address_line", "must not be more than 255 characters long")
	v.Check(validator.MaxChars(r.City, 100), "city", "must not be more than 100 characters long")
	v.Check(validator.MaxChars(r.PostalCode, 20), "postal_code", "must not be more than 20 characters long")

	v.Check(r.Currency != "", "currency", "must be provided")
	v.Check(validator.PermittedValue(r.Currency, SupportedCurrencies...), "currency", "is not a supported currency")

	ValidateTimezone(v, r.Timezone)
}

// Location returns the restaurant's time zone. Timezone is validated when
// saved, so this only falls back to UTC if the time zone database changed.
func (r *Restaurant) Location() *time.Location {
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

type RestaurantModel struct {
	DB *sql.DB
}

const restaurantColumns = `id, slug, name, description, phone, email, address_line, city, postal_code,
	currency, timezone, is_published, accepting_orders, version, created_at, updated_at`

// scanRestaurant scans restaurantColumns, followed by any extra destinations.
func scanRestaurant(row interface{ Scan(...any) error }, extra ...any) (*Restaurant, error) {
	var r Restaurant
	dest := []any{
		&r.ID, &r.Slug, &r.Name, &r.Description, &r.Phone, &r.Email, &r.AddressLine, &r.City,
		&r.PostalCode, &r.Currency, &r.Timezone, &r.IsPublished, &r.AcceptingOrders, &r.Version, &r.CreatedAt, &r.UpdatedAt,
	}
	err := row.Scan(append(dest, extra...)...)
	return &r, err
}

// InsertWithOwner creates restaurant and makes ownerID its owner, atomically.
func (m RestaurantModel) InsertWithOwner(ctx context.Context, r *Restaurant, ownerID int64) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO restaurants (slug, name, description, phone, email, address_line, city, postal_code,
		                         currency, timezone, is_published, accepting_orders)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, version, created_at, updated_at`

	args := []any{r.Slug, r.Name, r.Description, r.Phone, r.Email, r.AddressLine, r.City, r.PostalCode,
		r.Currency, r.Timezone, r.IsPublished, r.AcceptingOrders}

	err = tx.QueryRowContext(ctx, query, args...).Scan(&r.ID, &r.Version, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err, "restaurants_live_slug_uk") {
			return ErrDuplicateSlug
		}
		return fmt.Errorf("inserting restaurant: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		"INSERT INTO restaurant_users (restaurant_id, user_id, role) VALUES (?, ?, ?)",
		r.ID, ownerID, RoleOwner)
	if err != nil {
		return fmt.Errorf("inserting restaurant owner: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing restaurant: %w", err)
	}
	return nil
}

func (m RestaurantModel) Get(ctx context.Context, id int64) (*Restaurant, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `SELECT ` + restaurantColumns + ` FROM restaurants WHERE id = ?`

	r, err := scanRestaurant(m.DB.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting restaurant: %w", err)
	}
	return r, nil
}

// GetPublishedBySlug returns the published restaurant with the given slug.
// Unpublished restaurants are reported as ErrRecordNotFound, exactly like
// missing ones.
func (m RestaurantModel) GetPublishedBySlug(ctx context.Context, slug string) (*Restaurant, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `SELECT ` + restaurantColumns + ` FROM restaurants WHERE slug = ? AND is_published AND deleted_at IS NULL`

	r, err := scanRestaurant(m.DB.QueryRowContext(ctx, query, slug))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting restaurant by slug: %w", err)
	}
	return r, nil
}

// Update saves r if nobody else has changed it since it was read (r.Version
// still matches); otherwise it returns ErrEditConflict.
func (m RestaurantModel) Update(ctx context.Context, r *Restaurant) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	updatedAt := now()

	query := `
		UPDATE restaurants
		SET slug = ?, name = ?, description = ?, phone = ?, email = ?, address_line = ?,
		    city = ?, postal_code = ?, currency = ?, timezone = ?, is_published = ?, accepting_orders = ?,
		    version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`

	args := []any{
		r.Slug, r.Name, r.Description, r.Phone, r.Email, r.AddressLine,
		r.City, r.PostalCode, r.Currency, r.Timezone, r.IsPublished, r.AcceptingOrders, updatedAt,
		r.ID, r.Version,
	}

	result, err := m.DB.ExecContext(ctx, query, args...)
	if err != nil {
		if isDuplicateKey(err, "restaurants_live_slug_uk") {
			return ErrDuplicateSlug
		}
		return fmt.Errorf("updating restaurant: %w", err)
	}

	if err := expectOneRow(result, ErrEditConflict); err != nil {
		return err
	}

	r.Version++
	r.UpdatedAt = updatedAt
	return nil
}

// Delete soft-deletes a restaurant: it is unpublished and hidden from its
// members (see MembershipModel.Get), but its data, orders above all, is
// kept, and existing orders stay trackable. Its slug becomes free for reuse.
// It returns ErrRestaurantBusy while the restaurant has orders in progress.
func (m RestaurantModel) Delete(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// The lock makes concurrent order placement (which share-locks this
	// row) wait, so no order can sneak in after the check below.
	var locked int64
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM restaurants WHERE id = ? AND deleted_at IS NULL FOR UPDATE", id).Scan(&locked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRecordNotFound
		}
		return fmt.Errorf("locking restaurant: %w", err)
	}

	var busy bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM orders
			WHERE restaurant_id = ? AND status NOT IN ('delivered', 'picked_up', 'cancelled')
		)`, id).Scan(&busy)
	if err != nil {
		return fmt.Errorf("checking active orders: %w", err)
	}
	if busy {
		return ErrRestaurantBusy
	}

	deletedAt := now()
	_, err = tx.ExecContext(ctx, `
		UPDATE restaurants
		SET deleted_at = ?, is_published = FALSE, version = version + 1, updated_at = ?
		WHERE id = ?`, deletedAt, deletedAt, id)
	if err != nil {
		return fmt.Errorf("deleting restaurant: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing restaurant deletion: %w", err)
	}
	return nil
}

// ListForUser returns the restaurants userID is a member of, with their role
// in each, ordered by name.
func (m RestaurantModel) ListForUser(ctx context.Context, userID int64) ([]RestaurantWithRole, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		SELECT ` + restaurantColumns + `, ru.role
		FROM restaurants
		INNER JOIN (
			-- Only these columns, so restaurantColumns stays unambiguous.
			SELECT restaurant_id, role FROM restaurant_users WHERE user_id = ?
		) ru ON ru.restaurant_id = restaurants.id
		WHERE deleted_at IS NULL
		ORDER BY name, id`

	rows, err := m.DB.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("listing restaurants: %w", err)
	}
	defer rows.Close()

	restaurants := []RestaurantWithRole{}
	for rows.Next() {
		var role Role
		r, err := scanRestaurant(rows, &role)
		if err != nil {
			return nil, fmt.Errorf("scanning restaurant: %w", err)
		}
		restaurants = append(restaurants, RestaurantWithRole{Restaurant: *r, Role: role})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing restaurants: %w", err)
	}

	return restaurants, nil
}

// SetAcceptingOrders pauses or resumes ordering, regardless of version: it's
// a single switch, so there's nothing to merge.
func (m RestaurantModel) SetAcceptingOrders(ctx context.Context, id int64, accepting bool) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	result, err := m.DB.ExecContext(ctx,
		"UPDATE restaurants SET accepting_orders = ?, version = version + 1, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		accepting, now(), id)
	if err != nil {
		return fmt.Errorf("setting accepting_orders: %w", err)
	}
	return expectOneRow(result, ErrRecordNotFound)
}
