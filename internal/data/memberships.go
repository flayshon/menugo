package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrDuplicateMembership = errors.New("duplicate membership")

// Role is what a user is allowed to do within one restaurant.
type Role string

const (
	RoleOwner  Role = "restaurant_owner"
	RoleAdmin  Role = "restaurant_admin"
	RoleStaff  Role = "restaurant_staff"
	RoleDriver Role = "driver"
)

// Role groups used by route authorization.
var (
	AnyRole      = []Role{RoleOwner, RoleAdmin, RoleStaff, RoleDriver}
	StaffRoles   = []Role{RoleOwner, RoleAdmin, RoleStaff}
	ManagerRoles = []Role{RoleOwner, RoleAdmin}
	OwnerRoles   = []Role{RoleOwner}
)

func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleStaff, RoleDriver:
		return true
	}
	return false
}

// CanManage reports whether a member with role r may add or remove a member
// with role target. Owners manage everyone except other owners; admins manage
// staff and drivers. Ownership can't be granted or removed through the API.
func (r Role) CanManage(target Role) bool {
	switch r {
	case RoleOwner:
		return target == RoleAdmin || target == RoleStaff || target == RoleDriver
	case RoleAdmin:
		return target == RoleStaff || target == RoleDriver
	}
	return false
}

// Membership links a user to a restaurant with a role.
type Membership struct {
	RestaurantID int64
	UserID       int64
	Role         Role
	CreatedAt    time.Time
}

// Member is a membership together with the user's public details.
type Member struct {
	Membership
	Name  string
	Email string
}

type MembershipModel struct {
	DB *sql.DB
}

// Get returns userID's membership of restaurantID, or ErrRecordNotFound.
func (m MembershipModel) Get(ctx context.Context, restaurantID, userID int64) (*Membership, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		SELECT restaurant_id, user_id, role, created_at
		FROM restaurant_users
		WHERE restaurant_id = ? AND user_id = ?`

	var ms Membership
	err := m.DB.QueryRowContext(ctx, query, restaurantID, userID).
		Scan(&ms.RestaurantID, &ms.UserID, &ms.Role, &ms.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("getting membership: %w", err)
	}

	return &ms, nil
}

func (m MembershipModel) Insert(ctx context.Context, ms *Membership) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		INSERT INTO restaurant_users (restaurant_id, user_id, role)
		VALUES (?, ?, ?)
		RETURNING created_at`

	err := m.DB.QueryRowContext(ctx, query, ms.RestaurantID, ms.UserID, ms.Role).Scan(&ms.CreatedAt)
	if err != nil {
		if isDuplicateKey(err, "PRIMARY") {
			return ErrDuplicateMembership
		}
		return fmt.Errorf("inserting membership: %w", err)
	}
	return nil
}

// Delete removes userID's membership of restaurantID, but only if they still
// have the role the caller checked; otherwise it returns ErrEditConflict.
// Removing a driver also removes their driver profile, and is refused with
// ErrDriverBusy while they have deliveries in progress.
func (m MembershipModel) Delete(ctx context.Context, restaurantID, userID int64, role Role) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if role == RoleDriver {
		if err := checkDriverIdle(ctx, tx, restaurantID, userID); err != nil {
			return err
		}
	}

	result, err := tx.ExecContext(ctx,
		"DELETE FROM restaurant_users WHERE restaurant_id = ? AND user_id = ? AND role = ?",
		restaurantID, userID, role)
	if err != nil {
		return fmt.Errorf("deleting membership: %w", err)
	}
	if err := expectOneRow(result, ErrEditConflict); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing membership deletion: %w", err)
	}
	return nil
}

// ListMembers returns everyone with access to restaurantID, oldest first.
func (m MembershipModel) ListMembers(ctx context.Context, restaurantID int64) ([]Member, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	query := `
		SELECT ru.restaurant_id, ru.user_id, ru.role, ru.created_at, u.name, u.email
		FROM restaurant_users ru
		INNER JOIN users u ON u.id = ru.user_id
		WHERE ru.restaurant_id = ?
		ORDER BY ru.created_at, ru.user_id`

	rows, err := m.DB.QueryContext(ctx, query, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}
	defer rows.Close()

	members := []Member{}
	for rows.Next() {
		var mb Member
		err := rows.Scan(&mb.RestaurantID, &mb.UserID, &mb.Role, &mb.CreatedAt, &mb.Name, &mb.Email)
		if err != nil {
			return nil, fmt.Errorf("scanning member: %w", err)
		}
		members = append(members, mb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}

	return members, nil
}
