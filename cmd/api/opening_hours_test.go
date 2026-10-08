package main

import (
	"net/http"
	"testing"

	"menugo.flayshon.com/internal/data"
)

func TestOpeningHoursAPI(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	_, staff := s.ts.signUp(t, "staff@example.com")
	assertStatus(t, s.ts.addMember(t, s.owner, s.restaurant.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)

	res := s.ts.do(t, http.MethodGet, s.path("/opening-hours"), staff, nil)
	assertStatus(t, res, http.StatusOK)
	hours := decode[struct {
		Hours openingHoursResponse `json:"opening_hours"`
	}](t, res.body).Hours
	if hours.Timezone != "UTC" || !hours.IsOpen || len(hours.OpeningHours) != 0 {
		t.Errorf("default = %+v; want UTC, open, no schedule", hours)
	}

	t.Run("validation", func(t *testing.T) {
		for name, body := range map[string]any{
			"missing list": map[string]any{},
			"bad day":      map[string]any{"opening_hours": []map[string]string{{"day": "Monday", "opens_at": "09:00", "closes_at": "17:00"}}},
			"bad time":     map[string]any{"opening_hours": []map[string]string{{"day": "monday", "opens_at": "9:00", "closes_at": "17:00"}}},
			"25 o'clock":   map[string]any{"opening_hours": []map[string]string{{"day": "monday", "opens_at": "09:00", "closes_at": "25:00"}}},
			"zero length":  map[string]any{"opening_hours": []map[string]string{{"day": "monday", "opens_at": "09:00", "closes_at": "09:00"}}},
		} {
			t.Run(name, func(t *testing.T) {
				assertStatus(t, s.ts.do(t, http.MethodPut, s.path("/opening-hours"), s.owner, body), http.StatusUnprocessableEntity)
			})
		}
	})

	t.Run("staff can't change the schedule", func(t *testing.T) {
		body := map[string]any{"opening_hours": []map[string]string{}}
		assertStatus(t, s.ts.do(t, http.MethodPut, s.path("/opening-hours"), staff, body), http.StatusForbidden)
	})

	t.Run("time zone", func(t *testing.T) {
		assertStatus(t, s.ts.do(t, http.MethodPatch, restaurantPath(s.restaurant.ID), s.owner, map[string]any{"timezone": "Recife"}), http.StatusUnprocessableEntity)
		assertStatus(t, s.ts.do(t, http.MethodPatch, restaurantPath(s.restaurant.ID), s.owner, map[string]any{"timezone": "America/Recife"}), http.StatusOK)
	})

	// Open around the clock, with an overnight interval in the list.
	week := []map[string]string{}
	for _, day := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		week = append(week, map[string]string{"day": day, "opens_at": "00:00", "closes_at": "24:00"})
	}
	week = append(week, map[string]string{"day": "friday", "opens_at": "18:00", "closes_at": "02:00"})

	res = s.ts.do(t, http.MethodPut, s.path("/opening-hours"), s.owner, map[string]any{"opening_hours": week})
	assertStatus(t, res, http.StatusOK)
	hours = decode[struct {
		Hours openingHoursResponse `json:"opening_hours"`
	}](t, res.body).Hours
	if len(hours.OpeningHours) != 8 || hours.OpeningHours[0].Day != "sunday" || hours.OpeningHours[0].ClosesAt != "24:00" || hours.Timezone != "America/Recife" || !hours.IsOpen {
		t.Errorf("hours = %+v", hours)
	}

	t.Run("staff can pause orders", func(t *testing.T) {
		res := s.ts.do(t, http.MethodPut, s.path("/accepting-orders"), staff, map[string]any{"accepting_orders": false})
		assertStatus(t, res, http.StatusOK)
		if h := decode[struct {
			Hours openingHoursResponse `json:"opening_hours"`
		}](t, res.body).Hours; h.IsOpen || h.AcceptingOrders {
			t.Errorf("after pausing = %+v", h)
		}

		res = s.ts.do(t, http.MethodGet, "/v1/menus/pizza", "", nil)
		if menu := decode[struct {
			Menu struct {
				Hours openingHoursResponse `json:"opening_hours"`
			} `json:"menu"`
		}](t, res.body).Menu; menu.Hours.IsOpen || len(menu.Hours.OpeningHours) != 8 {
			t.Errorf("public menu hours = %+v", menu.Hours)
		}

		res = s.ts.do(t, http.MethodPost, "/v1/menus/pizza/orders", "", s.deliveryOrder())
		assertStatus(t, res, http.StatusConflict)

		assertStatus(t, s.ts.do(t, http.MethodPut, s.path("/accepting-orders"), staff, map[string]any{"accepting_orders": true}), http.StatusOK)
		s.placeOrder(t, s.deliveryOrder())
	})
}
