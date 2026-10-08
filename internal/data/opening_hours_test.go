package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"menugo.flayshon.com/internal/validator"
)

func TestParseClock(t *testing.T) {
	tests := map[string]int{
		"00:00": 0, "09:30": 570, "23:59": 1439, "24:00": 1440,
		"9:30": -1, "24:01": -1, "25:00": -1, "12:60": -1, "noon": -1, "": -1, "09:30:00": -1,
	}
	for in, want := range tests {
		got, ok := ParseClock(in)
		if want == -1 {
			if ok {
				t.Errorf("ParseClock(%q) = %d; want an error", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("ParseClock(%q) = %d, %t; want %d", in, got, ok, want)
		}
		if FormatClock(got) != in {
			t.Errorf("FormatClock(%d) = %q; want %q", got, FormatClock(got), in)
		}
	}

	if d, ok := ParseWeekday("saturday"); !ok || d != time.Saturday {
		t.Errorf("ParseWeekday(saturday) = %v, %t", d, ok)
	}
	for _, bad := range []string{"Saturday", "sat", ""} {
		if _, ok := ParseWeekday(bad); ok {
			t.Errorf("ParseWeekday(%q) should fail", bad)
		}
	}
}

func TestIsOpen(t *testing.T) {
	clock := func(s string) int { m, _ := ParseClock(s); return m }
	recife, _ := time.LoadLocation("America/Recife") // UTC-3, no DST
	berlin, _ := time.LoadLocation("Europe/Berlin")

	// 2026-10-09 is a Friday.
	at := func(loc *time.Location, day int, hhmm string) time.Time {
		m := clock(hhmm)
		return time.Date(2026, 10, day, m/60, m%60, 0, 0, loc)
	}

	lunchAndDinner := []OpeningInterval{
		{time.Friday, clock("11:30"), clock("15:00")},
		{time.Friday, clock("18:00"), clock("02:00")},   // into Saturday
		{time.Saturday, clock("22:00"), clock("03:00")}, // into Sunday
		{time.Sunday, clock("10:00"), clock("24:00")},
	}

	tests := []struct {
		name  string
		hours []OpeningInterval
		t     time.Time
		loc   *time.Location // the restaurant's time zone; Recife if nil
		want  bool
	}{
		{"no schedule means always open", nil, at(recife, 9, "04:00"), nil, true},
		{"before lunch", lunchAndDinner, at(recife, 9, "11:29"), nil, false},
		{"opening minute is open", lunchAndDinner, at(recife, 9, "11:30"), nil, true},
		{"closing minute is closed", lunchAndDinner, at(recife, 9, "15:00"), nil, false},
		{"between services", lunchAndDinner, at(recife, 9, "17:00"), nil, false},
		{"dinner", lunchAndDinner, at(recife, 9, "23:00"), nil, true},
		{"after midnight, from Friday's dinner", lunchAndDinner, at(recife, 10, "01:30"), nil, true},
		{"Friday dinner has ended", lunchAndDinner, at(recife, 10, "02:00"), nil, false},
		{"Saturday afternoon: no Saturday daytime hours", lunchAndDinner, at(recife, 10, "14:00"), nil, false},
		{"Saturday night into Sunday", lunchAndDinner, at(recife, 11, "02:59"), nil, true},
		{"Sunday until midnight", lunchAndDinner, at(recife, 11, "23:59"), nil, true},
		{"Monday", lunchAndDinner, at(recife, 12, "12:00"), nil, false},
		// The schedule is in the restaurant's time zone: 14:00 UTC is 11:00 in Recife.
		{"time zone: before opening", lunchAndDinner, time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC), nil, false},
		{"time zone: open", lunchAndDinner, time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC), nil, true},
		// Berlin leaves summer time on 2026-10-25 at 03:00; wall-clock hours still apply.
		// 08:30 UTC is 09:30 in Berlin after the change (10:30 before it).
		{"daylight saving change", []OpeningInterval{{time.Sunday, clock("09:00"), clock("10:00")}}, time.Date(2026, 10, 25, 8, 30, 0, 0, time.UTC), berlin, true},
		{"daylight saving change, other side", []OpeningInterval{{time.Sunday, clock("09:00"), clock("10:00")}}, time.Date(2026, 10, 25, 7, 30, 0, 0, time.UTC), berlin, false},
	}

	for _, tt := range tests {
		loc := recife
		if tt.loc != nil {
			loc = tt.loc
		}
		if got := IsOpen(tt.hours, loc, tt.t); got != tt.want {
			t.Errorf("%s: IsOpen at %v = %t; want %t", tt.name, tt.t.In(loc), got, tt.want)
		}
	}
}

func TestValidateOpeningHours(t *testing.T) {
	tests := []struct {
		hours []OpeningInterval
		field string
	}{
		{[]OpeningInterval{{time.Monday, 600, 900}, {time.Monday, 1080, 120}}, ""},
		{[]OpeningInterval{{time.Monday, 0, 1440}}, ""},
		{[]OpeningInterval{{time.Monday, 600, 600}}, "opening_hours[0].closes_at"},
		{[]OpeningInterval{{time.Monday, 1440, 60}}, "opening_hours[0].opens_at"},
		{make([]OpeningInterval, maxOpeningIntervals+1), "opening_hours"},
	}
	for _, tt := range tests {
		v := validator.New()
		ValidateOpeningHours(v, tt.hours)
		if tt.field == "" {
			checkField(t, v, "")
			continue
		}
		if _, ok := v.Errors[tt.field]; !ok {
			t.Errorf("want an error for %q, got %v", tt.field, v.Errors)
		}
	}
}

func TestOrdersOnlyWhileOpen(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()
	rid := f.restaurant.ID

	// One minute, twelve hours from now: closed now, whatever the time.
	later := time.Now().UTC().Add(12 * time.Hour)
	minute := later.Hour()*60 + later.Minute()
	closedNow := []OpeningInterval{{later.Weekday(), minute, minute + 1}}
	if err := f.m.OpeningHours.Replace(ctx, rid, closedNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.Orders.Create(ctx, f.restaurant, f.request()); !errors.Is(err, ErrRestaurantClosed) {
		t.Errorf("outside opening hours: err = %v; want ErrRestaurantClosed", err)
	}

	var allWeek []OpeningInterval
	for d := time.Sunday; d <= time.Saturday; d++ {
		allWeek = append(allWeek, OpeningInterval{d, 0, 24 * 60})
	}
	if err := f.m.OpeningHours.Replace(ctx, rid, allWeek); err != nil {
		t.Fatal(err)
	}
	if got, err := f.m.OpeningHours.Get(ctx, rid); err != nil || len(got) != 7 {
		t.Fatalf("hours = %v, %v", got, err)
	}
	if _, _, err := f.m.Orders.Create(ctx, f.restaurant, f.request()); err != nil {
		t.Errorf("open all week: %v", err)
	}

	if err := f.m.Restaurants.SetAcceptingOrders(ctx, rid, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.Orders.Create(ctx, f.restaurant, f.request()); !errors.Is(err, ErrRestaurantClosed) {
		t.Errorf("paused: err = %v; want ErrRestaurantClosed", err)
	}
}
