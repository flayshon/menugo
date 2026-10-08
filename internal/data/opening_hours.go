package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"menugo.flayshon.com/internal/validator"
)

// ErrRestaurantClosed means a restaurant isn't taking orders right now:
// it's outside its opening hours, or orders are paused.
var ErrRestaurantClosed = errors.New("restaurant is not accepting orders")

const maxOpeningIntervals = 50

// OpeningInterval is one period a restaurant is open, in its time zone.
// Opens and Closes are minutes since midnight. If Closes isn't after Opens,
// the interval runs past midnight: {Friday, 18:00, 02:00} covers Friday
// 18:00 to Saturday 02:00. Closes may be 24:00 (1440).
type OpeningInterval struct {
	Day    time.Weekday
	Opens  int
	Closes int
}

// ParseWeekday parses a lowercase English day name ("monday").
func ParseWeekday(s string) (time.Weekday, bool) {
	for d := time.Sunday; d <= time.Saturday; d++ {
		if strings.ToLower(d.String()) == s {
			return d, true
		}
	}
	return 0, false
}

// ParseClock parses "HH:MM" into minutes since midnight. "24:00" is allowed,
// for closing times.
func ParseClock(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		if s == "24:00" {
			return 24 * 60, true
		}
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// FormatClock formats minutes since midnight as "HH:MM".
func FormatClock(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

func ValidateOpeningHours(v *validator.Validator, hours []OpeningInterval) {
	v.Check(len(hours) <= maxOpeningIntervals, "opening_hours", fmt.Sprintf("must not have more than %d intervals", maxOpeningIntervals))
	for i, h := range hours {
		key := fmt.Sprintf("opening_hours[%d]", i)
		v.Check(h.Opens >= 0 && h.Opens < 24*60, key+".opens_at", "must be between 00:00 and 23:59")
		v.Check(h.Closes > 0 && h.Closes <= 24*60, key+".closes_at", "must be between 00:01 and 24:00")
		v.Check(h.Opens != h.Closes, key+".closes_at", "must be different from opens_at")
	}
}

// IsOpen reports whether a schedule is open at t in loc. An empty schedule
// is always open. Times are compared on the local wall clock, so daylight
// saving changes need no special handling.
func IsOpen(hours []OpeningInterval, loc *time.Location, t time.Time) bool {
	if len(hours) == 0 {
		return true
	}

	local := t.In(loc)
	day := local.Weekday()
	minute := local.Hour()*60 + local.Minute()
	yesterday := (day + 6) % 7

	for _, h := range hours {
		if h.Closes > h.Opens {
			if h.Day == day && minute >= h.Opens && minute < h.Closes {
				return true
			}
			continue
		}
		// Past midnight: the evening part today, or the early-morning part
		// of an interval that started yesterday.
		if (h.Day == day && minute >= h.Opens) || (h.Day == yesterday && minute < h.Closes) {
			return true
		}
	}
	return false
}

// ValidateTimezone checks that tz is an IANA time zone name.
func ValidateTimezone(v *validator.Validator, tz string) {
	v.Check(tz != "", "timezone", "must be provided")
	_, err := time.LoadLocation(tz)
	// "Local" depends on the server's settings, so it isn't allowed.
	v.Check(err == nil && tz != "Local", "timezone", "must be an IANA time zone, e.g. America/Recife")
}

type OpeningHoursModel struct {
	DB *sql.DB
}

// Get returns restaurantID's schedule, ordered by day and time.
func (m OpeningHoursModel) Get(ctx context.Context, restaurantID int64) ([]OpeningInterval, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	return openingHours(ctx, m.DB, restaurantID)
}

func openingHours(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, restaurantID int64) ([]OpeningInterval, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT day_of_week, opens_minute, closes_minute
		FROM restaurant_opening_hours
		WHERE restaurant_id = ?
		ORDER BY day_of_week, opens_minute`, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("getting opening hours: %w", err)
	}
	defer rows.Close()

	hours := []OpeningInterval{}
	for rows.Next() {
		var h OpeningInterval
		if err := rows.Scan(&h.Day, &h.Opens, &h.Closes); err != nil {
			return nil, fmt.Errorf("scanning opening hours: %w", err)
		}
		hours = append(hours, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("getting opening hours: %w", err)
	}
	return hours, nil
}

// Replace makes hours restaurantID's whole schedule. An empty schedule means
// always open.
func (m OpeningHoursModel) Replace(ctx context.Context, restaurantID int64, hours []OpeningInterval) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM restaurant_opening_hours WHERE restaurant_id = ?", restaurantID); err != nil {
		return fmt.Errorf("deleting opening hours: %w", err)
	}

	if len(hours) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?),", len(hours)), ",")
		args := make([]any, 0, 4*len(hours))
		for _, h := range hours {
			args = append(args, restaurantID, int(h.Day), h.Opens, h.Closes)
		}
		_, err := tx.ExecContext(ctx,
			"INSERT INTO restaurant_opening_hours (restaurant_id, day_of_week, opens_minute, closes_minute) VALUES "+placeholders,
			args...)
		if err != nil {
			return fmt.Errorf("inserting opening hours: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing opening hours: %w", err)
	}
	return nil
}
