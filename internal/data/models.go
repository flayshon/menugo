// Package data contains the domain types and the database access code for
// them. Each model wraps a *sql.DB and uses plain SQL.
package data

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

var (
	ErrRecordNotFound = errors.New("record not found")
	ErrEditConflict   = errors.New("edit conflict")
)

// queryTimeout bounds how long a single model method may spend in the database.
const queryTimeout = 3 * time.Second

// Models groups all the models so they can be passed around as one value.
type Models struct {
	Users       UserModel
	Tokens      TokenModel
	Restaurants RestaurantModel
	Memberships MembershipModel
	Categories  CategoryModel
	MenuItems   MenuItemModel
	Zones       DeliveryZoneModel
	Orders      OrderModel
}

func NewModels(db *sql.DB) Models {
	return Models{
		Users:       UserModel{DB: db},
		Tokens:      TokenModel{DB: db},
		Restaurants: RestaurantModel{DB: db},
		Memberships: MembershipModel{DB: db},
		Categories:  CategoryModel{DB: db},
		MenuItems:   MenuItemModel{DB: db},
		Zones:       DeliveryZoneModel{DB: db},
		Orders:      OrderModel{DB: db},
	}
}

// MariaDB error numbers we react to.
const (
	errDupEntry        = 1062 // unique key violation
	errNoReferencedRow = 1452 // foreign key points at a missing row
)

// isDuplicateKey reports whether err is a unique-constraint violation on the
// named key ("PRIMARY" for the primary key).
func isDuplicateKey(err error, key string) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != errDupEntry {
		return false
	}
	// The message looks like: Duplicate entry 'x' for key 'users_email_uk'
	return strings.HasSuffix(mysqlErr.Message, "'"+key+"'")
}

// isForeignKeyViolation reports whether err is an insert or update that
// broke the named foreign key constraint.
func isForeignKeyViolation(err error, constraint string) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != errNoReferencedRow {
		return false
	}
	return strings.Contains(mysqlErr.Message, "CONSTRAINT `"+constraint+"`")
}

// now returns the current UTC time at the precision MariaDB stores (DATETIME(6)),
// so values written and read back compare equal.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

// expectOneRow returns errNone if result affected no rows. Use it for
// statements that target exactly one row.
func expectOneRow(result sql.Result, errNone error) error {
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("getting rows affected: %w", err)
	}
	if n == 0 {
		return errNone
	}
	return nil
}
