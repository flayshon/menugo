// Package data contains the domain types and the database access code for
// them. Each model wraps a *sql.DB and uses plain SQL.
package data

import (
	"database/sql"
	"errors"
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
}

func NewModels(db *sql.DB) Models {
	return Models{
		Users:       UserModel{DB: db},
		Tokens:      TokenModel{DB: db},
		Restaurants: RestaurantModel{DB: db},
		Memberships: MembershipModel{DB: db},
	}
}

// errDupEntry is MariaDB's error number for a unique key violation.
const errDupEntry = 1062

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

// now returns the current UTC time at the precision MariaDB stores (DATETIME(6)),
// so values written and read back compare equal.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
