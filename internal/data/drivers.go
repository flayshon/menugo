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
	ErrDuplicateDriver = errors.New("user is already a driver of this restaurant")
	// ErrDriverBusy means a driver still has deliveries in progress, so
	// they can't be removed until those are finished or reassigned.
	ErrDriverBusy = errors.New("driver has deliveries in progress")
)

// MemberRoleConflictError means a user can't become a driver because they
// already have another role in the restaurant.
type MemberRoleConflictError struct {
	Role Role
}

func (e *MemberRoleConflictError) Error() string {
	return fmt.Sprintf("the user is already a member of this restaurant as %s", e.Role)
}

// Driver is a user's driver profile in one restaurant.
type Driver struct {
	ID           int64
	RestaurantID int64
	UserID       int64
	Email        string // the user's login email; read only
	Name         string
	Phone        string // normalized with NormalizePhone
	Vehicle      string
	IsActive     bool
	Version      int32
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func ValidateDriver(v *validator.Validator, d *Driver) {
	v.Check(validator.NotBlank(d.Name), "name", "must be provided")
	v.Check(validator.MaxChars(d.Name, 100), "name", "must not be more than 100 characters long")
	v.Check(d.Phone != "", "phone", "must be provided")
	v.Check(validator.Matches(d.Phone, phoneRX), "phone", "must be a phone number with 8 to 15 digits")
	v.Check(validator.MaxChars(d.Vehicle, 100), "vehicle", "must not be more than 100 characters long")
}

type DriverModel struct {
	DB *sql.DB
}

const driverSelect = `
	SELECT d.id, d.restaurant_id, d.user_id, u.email, d.name, d.phone, d.vehicle, d.is_active,
	       d.version, d.created_at, d.updated_at
	FROM drivers d
	INNER JOIN users u ON u.id = d.user_id`

func scanDriver(row interface{ Scan(...any) error }) (*Driver, error) {
	var d Driver
	err := row.Scan(&d.ID, &d.RestaurantID, &d.UserID, &d.Email, &d.Name, &d.Phone, &d.Vehicle,
		&d.IsActive, &d.Version, &d.CreatedAt, &d.UpdatedAt)
	return &d, err
}

// Create makes d.UserID a driver of d.RestaurantID: it gives them the driver
// role if they aren't a member yet, and creates their driver profile, in one
// transaction. Users with another role get a MemberRoleConflictError.
func (m DriverModel) Create(ctx context.Context, d *Driver) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var role Role
	err = tx.QueryRowContext(ctx,
		"SELECT role FROM restaurant_users WHERE restaurant_id = ? AND user_id = ? FOR UPDATE",
		d.RestaurantID, d.UserID).Scan(&role)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx,
			"INSERT INTO restaurant_users (restaurant_id, user_id, role) VALUES (?, ?, ?)",
			d.RestaurantID, d.UserID, RoleDriver)
		if err != nil {
			return fmt.Errorf("inserting driver membership: %w", err)
		}
	case err != nil:
		return fmt.Errorf("getting membership: %w", err)
	case role != RoleDriver:
		return &MemberRoleConflictError{Role: role}
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO drivers (restaurant_id, user_id, name, phone, vehicle, is_active)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING id, version, created_at, updated_at`,
		d.RestaurantID, d.UserID, d.Name, d.Phone, d.Vehicle, d.IsActive,
	).Scan(&d.ID, &d.Version, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err, "drivers_restaurant_user_uk") {
			return ErrDuplicateDriver
		}
		return fmt.Errorf("inserting driver: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing driver: %w", err)
	}
	return nil
}

func (m DriverModel) Get(ctx context.Context, restaurantID, id int64) (*Driver, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	d, err := scanDriver(m.DB.QueryRowContext(ctx, driverSelect+` WHERE d.restaurant_id = ? AND d.id = ?`, restaurantID, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting driver: %w", err)
	}
	return d, nil
}

// List returns restaurantID's drivers by name, optionally only active ones.
func (m DriverModel) List(ctx context.Context, restaurantID int64, activeOnly bool) ([]*Driver, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := m.DB.QueryContext(ctx,
		driverSelect+` WHERE d.restaurant_id = ? AND (d.is_active OR NOT ?) ORDER BY d.name, d.id`,
		restaurantID, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("listing drivers: %w", err)
	}
	defer rows.Close()

	drivers := []*Driver{}
	for rows.Next() {
		d, err := scanDriver(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning driver: %w", err)
		}
		drivers = append(drivers, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing drivers: %w", err)
	}
	return drivers, nil
}

// Update saves d's profile if its version is unchanged since it was read;
// otherwise it returns ErrEditConflict.
func (m DriverModel) Update(ctx context.Context, d *Driver) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	updatedAt := now()

	result, err := m.DB.ExecContext(ctx, `
		UPDATE drivers
		SET name = ?, phone = ?, vehicle = ?, is_active = ?, version = version + 1, updated_at = ?
		WHERE restaurant_id = ? AND id = ? AND version = ?`,
		d.Name, d.Phone, d.Vehicle, d.IsActive, updatedAt,
		d.RestaurantID, d.ID, d.Version)
	if err != nil {
		return fmt.Errorf("updating driver: %w", err)
	}
	if err := expectOneRow(result, ErrEditConflict); err != nil {
		return err
	}

	d.Version++
	d.UpdatedAt = updatedAt
	return nil
}

// Delete removes a driver from the restaurant: their profile and their
// driver membership. It returns ErrDriverBusy while they have deliveries in
// progress. Finished deliveries keep their record, without the driver.
func (m DriverModel) Delete(ctx context.Context, restaurantID, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var userID int64
	err = tx.QueryRowContext(ctx,
		"SELECT user_id FROM drivers WHERE restaurant_id = ? AND id = ? FOR UPDATE",
		restaurantID, id).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRecordNotFound
		}
		return fmt.Errorf("locking driver: %w", err)
	}

	if err := checkDriverIdle(ctx, tx, restaurantID, userID); err != nil {
		return err
	}

	// Deleting the membership deletes the profile through its foreign key.
	_, err = tx.ExecContext(ctx,
		"DELETE FROM restaurant_users WHERE restaurant_id = ? AND user_id = ? AND role = ?",
		restaurantID, userID, RoleDriver)
	if err != nil {
		return fmt.Errorf("deleting driver membership: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing driver deletion: %w", err)
	}
	return nil
}

// checkDriverIdle returns ErrDriverBusy if userID has a driver profile in
// restaurantID with deliveries in progress. It locks the profile until tx
// ends, which blocks new assignments to it (they take a shared lock on it).
func checkDriverIdle(ctx context.Context, tx *sql.Tx, restaurantID, userID int64) error {
	_, err := tx.ExecContext(ctx,
		"SELECT id FROM drivers WHERE restaurant_id = ? AND user_id = ? FOR UPDATE",
		restaurantID, userID)
	if err != nil {
		return fmt.Errorf("locking driver: %w", err)
	}

	var busy bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM deliveries dl
			INNER JOIN drivers d ON d.id = dl.driver_id
			WHERE d.restaurant_id = ? AND d.user_id = ? AND dl.status IN ('assigned', 'picked_up')
		)`, restaurantID, userID).Scan(&busy)
	if err != nil {
		return fmt.Errorf("checking driver deliveries: %w", err)
	}
	if busy {
		return ErrDriverBusy
	}
	return nil
}
