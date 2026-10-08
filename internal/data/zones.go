package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"menugo.flayshon.com/internal/validator"
)

var ErrDuplicateZoneName = errors.New("duplicate delivery zone name")

// PostalCodeInUseError means a postal code entry is already covered by
// another of the restaurant's zones.
type PostalCodeInUseError struct {
	PostalCode string
}

func (e *PostalCodeInUseError) Error() string {
	return fmt.Sprintf("postal code %s is already in another delivery zone", e.PostalCode)
}

const (
	maxZonePostalCodes = 1000
	minPostalCodeLen   = 2
	maxPostalCodeLen   = 10
)

// DeliveryZone is an area a restaurant delivers to, with its fee and minimum
// order. PostalCodes holds normalized entries; each matches postal codes that
// start with it (a full code matches only itself).
type DeliveryZone struct {
	ID            int64
	RestaurantID  int64
	Name          string
	FeeCents      int64
	MinOrderCents int64
	IsActive      bool
	PostalCodes   []string
	Version       int32
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NormalizePostalCode uppercases a postal code and removes everything but
// letters and digits, so "50030-230" and "50030230" are the same code.
func NormalizePostalCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SetPostalCodes normalizes codes, removes duplicates, sorts them and stores
// them on the zone.
func (z *DeliveryZone) SetPostalCodes(codes []string) {
	normalized := make([]string, 0, len(codes))
	for _, c := range codes {
		normalized = append(normalized, NormalizePostalCode(c))
	}
	slices.Sort(normalized)
	z.PostalCodes = slices.Compact(normalized)
}

func ValidateDeliveryZone(v *validator.Validator, z *DeliveryZone) {
	v.Check(validator.NotBlank(z.Name), "name", "must be provided")
	v.Check(validator.MaxChars(z.Name, 100), "name", "must not be more than 100 characters long")

	v.Check(z.FeeCents >= 0, "fee_cents", "must not be negative")
	v.Check(z.FeeCents <= MaxPriceCents, "fee_cents", fmt.Sprintf("must not be more than %d", MaxPriceCents))
	v.Check(z.MinOrderCents >= 0, "min_order_cents", "must not be negative")
	v.Check(z.MinOrderCents <= MaxPriceCents, "min_order_cents", fmt.Sprintf("must not be more than %d", MaxPriceCents))

	v.Check(len(z.PostalCodes) > 0, "postal_codes", "must contain at least one postal code")
	v.Check(len(z.PostalCodes) <= maxZonePostalCodes, "postal_codes", fmt.Sprintf("must not contain more than %d entries", maxZonePostalCodes))
	for _, code := range z.PostalCodes {
		v.Check(len(code) >= minPostalCodeLen && len(code) <= maxPostalCodeLen, "postal_codes",
			fmt.Sprintf("entries must have %d to %d letters or digits", minPostalCodeLen, maxPostalCodeLen))
	}
}

type DeliveryZoneModel struct {
	DB *sql.DB
}

const zoneColumns = `id, restaurant_id, name, fee_cents, min_order_cents, is_active, version, created_at, updated_at`

func scanZone(row interface{ Scan(...any) error }) (*DeliveryZone, error) {
	var z DeliveryZone
	err := row.Scan(&z.ID, &z.RestaurantID, &z.Name, &z.FeeCents, &z.MinOrderCents,
		&z.IsActive, &z.Version, &z.CreatedAt, &z.UpdatedAt)
	return &z, err
}

// Insert creates z and its postal codes in restaurant z.RestaurantID.
func (m DeliveryZoneModel) Insert(ctx context.Context, z *DeliveryZone) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO delivery_zones (restaurant_id, name, fee_cents, min_order_cents, is_active)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id, version, created_at, updated_at`

	err = tx.QueryRowContext(ctx, query, z.RestaurantID, z.Name, z.FeeCents, z.MinOrderCents, z.IsActive).
		Scan(&z.ID, &z.Version, &z.CreatedAt, &z.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err, "delivery_zones_restaurant_name_uk") {
			return ErrDuplicateZoneName
		}
		return fmt.Errorf("inserting delivery zone: %w", err)
	}

	if err := replacePostalCodes(ctx, tx, z); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing delivery zone: %w", err)
	}
	return nil
}

// replacePostalCodes makes z.PostalCodes the zone's complete list.
func replacePostalCodes(ctx context.Context, tx *sql.Tx, z *DeliveryZone) error {
	_, err := tx.ExecContext(ctx,
		"DELETE FROM delivery_zone_postal_codes WHERE restaurant_id = ? AND zone_id = ?",
		z.RestaurantID, z.ID)
	if err != nil {
		return fmt.Errorf("deleting zone postal codes: %w", err)
	}

	if len(z.PostalCodes) == 0 {
		return nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("(?, ?, ?),", len(z.PostalCodes)), ",")
	args := make([]any, 0, 3*len(z.PostalCodes))
	for _, code := range z.PostalCodes {
		args = append(args, z.RestaurantID, code, z.ID)
	}

	_, err = tx.ExecContext(ctx,
		"INSERT INTO delivery_zone_postal_codes (restaurant_id, postal_code, zone_id) VALUES "+placeholders,
		args...)
	if err != nil {
		if isDuplicateKey(err, "PRIMARY") {
			return postalCodeConflict(ctx, tx, z)
		}
		return fmt.Errorf("inserting zone postal codes: %w", err)
	}
	return nil
}

// postalCodeConflict finds which of z's postal codes another zone already
// has, to tell the client exactly what clashed.
func postalCodeConflict(ctx context.Context, tx *sql.Tx, z *DeliveryZone) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(z.PostalCodes)), ",")
	args := []any{z.RestaurantID, z.ID}
	for _, code := range z.PostalCodes {
		args = append(args, code)
	}

	var code string
	err := tx.QueryRowContext(ctx, `
		SELECT postal_code FROM delivery_zone_postal_codes
		WHERE restaurant_id = ? AND zone_id <> ? AND postal_code IN (`+placeholders+`)
		ORDER BY postal_code LIMIT 1`, args...).Scan(&code)
	if err != nil {
		return fmt.Errorf("finding conflicting postal code: %w", err)
	}
	return &PostalCodeInUseError{PostalCode: code}
}

// Get returns zone id of restaurantID with its postal codes.
func (m DeliveryZoneModel) Get(ctx context.Context, restaurantID, id int64) (*DeliveryZone, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `SELECT ` + zoneColumns + ` FROM delivery_zones WHERE restaurant_id = ? AND id = ?`

	z, err := scanZone(m.DB.QueryRowContext(ctx, query, restaurantID, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting delivery zone: %w", err)
	}

	codes, err := m.postalCodes(ctx, restaurantID, id)
	if err != nil {
		return nil, err
	}
	z.PostalCodes = codes[id]
	return z, nil
}

// List returns all of restaurantID's zones, by name, with their postal codes.
func (m DeliveryZoneModel) List(ctx context.Context, restaurantID int64) ([]*DeliveryZone, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `SELECT ` + zoneColumns + ` FROM delivery_zones WHERE restaurant_id = ? ORDER BY name, id`

	rows, err := m.DB.QueryContext(ctx, query, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("listing delivery zones: %w", err)
	}
	defer rows.Close()

	zones := []*DeliveryZone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning delivery zone: %w", err)
		}
		zones = append(zones, z)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing delivery zones: %w", err)
	}

	codes, err := m.postalCodes(ctx, restaurantID, 0)
	if err != nil {
		return nil, err
	}
	for _, z := range zones {
		z.PostalCodes = codes[z.ID]
	}
	return zones, nil
}

// postalCodes returns the postal codes of restaurantID's zones, sorted, keyed
// by zone ID. If zoneID isn't zero, only that zone's are returned.
func (m DeliveryZoneModel) postalCodes(ctx context.Context, restaurantID, zoneID int64) (map[int64][]string, error) {
	rows, err := m.DB.QueryContext(ctx, `
		SELECT zone_id, postal_code FROM delivery_zone_postal_codes
		WHERE restaurant_id = ? AND (? = 0 OR zone_id = ?)
		ORDER BY postal_code`, restaurantID, zoneID, zoneID)
	if err != nil {
		return nil, fmt.Errorf("listing zone postal codes: %w", err)
	}
	defer rows.Close()

	codes := make(map[int64][]string)
	for rows.Next() {
		var id int64
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, fmt.Errorf("scanning zone postal code: %w", err)
		}
		codes[id] = append(codes[id], code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing zone postal codes: %w", err)
	}
	return codes, nil
}

// Update saves z, replacing its postal codes, if its version is unchanged
// since it was read; otherwise it returns ErrEditConflict.
func (m DeliveryZoneModel) Update(ctx context.Context, z *DeliveryZone) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	updatedAt := now()

	result, err := tx.ExecContext(ctx, `
		UPDATE delivery_zones
		SET name = ?, fee_cents = ?, min_order_cents = ?, is_active = ?, version = version + 1, updated_at = ?
		WHERE restaurant_id = ? AND id = ? AND version = ?`,
		z.Name, z.FeeCents, z.MinOrderCents, z.IsActive, updatedAt,
		z.RestaurantID, z.ID, z.Version)
	if err != nil {
		if isDuplicateKey(err, "delivery_zones_restaurant_name_uk") {
			return ErrDuplicateZoneName
		}
		return fmt.Errorf("updating delivery zone: %w", err)
	}
	if err := expectOneRow(result, ErrEditConflict); err != nil {
		return err
	}

	if err := replacePostalCodes(ctx, tx, z); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing delivery zone: %w", err)
	}

	z.Version++
	z.UpdatedAt = updatedAt
	return nil
}

// Delete removes a zone. Orders placed in it keep their address and fee.
func (m DeliveryZoneModel) Delete(ctx context.Context, restaurantID, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	result, err := m.DB.ExecContext(ctx, "DELETE FROM delivery_zones WHERE restaurant_id = ? AND id = ?", restaurantID, id)
	if err != nil {
		return fmt.Errorf("deleting delivery zone: %w", err)
	}
	return expectOneRow(result, ErrRecordNotFound)
}

// ErrNotDeliverable means none of the restaurant's active zones covers a
// postal code.
var ErrNotDeliverable = errors.New("not deliverable to this postal code")

// FindForPostalCode returns the zone that delivers to postalCode (which must
// be normalized). The longest matching entry decides: if its zone is
// inactive, the code isn't delivered to, even if a shorter entry of an
// active zone also matches.
func (m DeliveryZoneModel) FindForPostalCode(ctx context.Context, restaurantID int64, postalCode string) (*DeliveryZone, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	return findZoneForPostalCode(ctx, m.DB, restaurantID, postalCode)
}

// querier is satisfied by *sql.DB and *sql.Tx, so lookups can run inside or
// outside a transaction.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func findZoneForPostalCode(ctx context.Context, q querier, restaurantID int64, postalCode string) (*DeliveryZone, error) {
	if postalCode == "" {
		return nil, ErrNotDeliverable
	}

	// Entries only contain letters and digits, so they're safe in LIKE.
	query := `
		SELECT ` + qualify("z", zoneColumns) + `
		FROM delivery_zone_postal_codes pc
		INNER JOIN delivery_zones z ON z.restaurant_id = pc.restaurant_id AND z.id = pc.zone_id
		WHERE pc.restaurant_id = ? AND ? LIKE CONCAT(pc.postal_code, '%')
		ORDER BY LENGTH(pc.postal_code) DESC
		LIMIT 1`

	z, err := scanZone(q.QueryRowContext(ctx, query, restaurantID, postalCode))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotDeliverable
		}
		return nil, fmt.Errorf("finding delivery zone: %w", err)
	}
	if !z.IsActive {
		return nil, ErrNotDeliverable
	}
	return z, nil
}

// qualify prefixes each column in a comma-separated list with alias.
func qualify(alias, columns string) string {
	cols := strings.Split(columns, ",")
	for i, c := range cols {
		cols[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}
