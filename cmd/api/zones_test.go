package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"menugo.flayshon.com/internal/data"
)

func zonePath(restaurantID int64, rest string) string {
	return fmt.Sprintf("/v1/restaurants/%d/delivery-zones%s", restaurantID, rest)
}

func (ts *testServer) createZone(t *testing.T, token string, restaurantID int64, body map[string]any) zoneResponse {
	t.Helper()
	res := ts.do(t, http.MethodPost, zonePath(restaurantID, ""), token, body)
	assertStatus(t, res, http.StatusCreated)
	return decode[struct {
		Zone zoneResponse `json:"delivery_zone"`
	}](t, res.body).Zone
}

func TestDeliveryZoneManagement(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, owner := ts.signUp(t, "owner@example.com")
	_, staff := ts.signUp(t, "staff@example.com")
	_, driver := ts.signUp(t, "driver@example.com")
	_, stranger := ts.signUp(t, "stranger@example.com")

	r := ts.createRestaurant(t, owner, "pizza")
	assertStatus(t, ts.addMember(t, owner, r.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)
	assertStatus(t, ts.addMember(t, owner, r.ID, "driver@example.com", data.RoleDriver), http.StatusCreated)
	strangers := ts.createRestaurant(t, stranger, "strangers")

	zone := ts.createZone(t, owner, r.ID, map[string]any{
		"name": "Centre", "fee_cents": 800, "min_order_cents": 3000,
		"postal_codes": []string{"50030-230", "50030 230", "5004"},
	})
	if strings.Join(zone.PostalCodes, ",") != "50030230,5004" || !zone.IsActive {
		t.Errorf("zone = %+v; want normalized, de-duplicated codes", zone)
	}
	path := zonePath(r.ID, fmt.Sprintf("/%d", zone.ID))

	t.Run("validation", func(t *testing.T) {
		res := ts.do(t, http.MethodPost, zonePath(r.ID, ""), owner, map[string]any{"name": "Free?", "postal_codes": []string{"60"}})
		assertStatus(t, res, http.StatusUnprocessableEntity)
		if _, ok := validationErrors(t, res.body)["fee_cents"]; !ok {
			t.Errorf("a missing fee must be an error: %s", res.body)
		}
	})

	t.Run("postal code already in another zone", func(t *testing.T) {
		res := ts.do(t, http.MethodPost, zonePath(r.ID, ""), owner, map[string]any{
			"name": "Overlap", "fee_cents": 1, "postal_codes": []string{"5004"},
		})
		assertStatus(t, res, http.StatusUnprocessableEntity)
		if msg := validationErrors(t, res.body)["postal_codes"]; !strings.Contains(msg, "5004") {
			t.Errorf("message = %q", msg)
		}
	})

	t.Run("update", func(t *testing.T) {
		res := ts.do(t, http.MethodPatch, path, owner, map[string]any{"fee_cents": 900, "postal_codes": []string{"60000"}})
		assertStatus(t, res, http.StatusOK)
		got := decode[struct {
			Zone zoneResponse `json:"delivery_zone"`
		}](t, res.body).Zone
		if got.FeeCents != 900 || got.MinOrderCents != 3000 || strings.Join(got.PostalCodes, ",") != "60000" {
			t.Errorf("zone = %+v", got)
		}

		res = ts.do(t, http.MethodPatch, path, owner, map[string]any{"name": "Stale", "version": 1})
		assertStatus(t, res, http.StatusConflict)
	})

	tests := []struct {
		name   string
		token  string
		method string
		path   string
		body   any
		want   int
	}{
		{"staff can list", staff, http.MethodGet, zonePath(r.ID, ""), nil, http.StatusOK},
		{"staff can view", staff, http.MethodGet, path, nil, http.StatusOK},
		{"staff cannot create", staff, http.MethodPost, zonePath(r.ID, ""), map[string]any{"name": "x", "fee_cents": 1, "postal_codes": []string{"70"}}, http.StatusForbidden},
		{"staff cannot edit", staff, http.MethodPatch, path, map[string]any{"fee_cents": 0}, http.StatusForbidden},
		{"driver cannot view", driver, http.MethodGet, zonePath(r.ID, ""), nil, http.StatusForbidden},
		{"other restaurant cannot view", stranger, http.MethodGet, path, nil, http.StatusNotFound},
		{"zone ID through another restaurant", stranger, http.MethodGet, zonePath(strangers.ID, fmt.Sprintf("/%d", zone.ID)), nil, http.StatusNotFound},
		{"zone ID through another restaurant, delete", stranger, http.MethodDelete, zonePath(strangers.ID, fmt.Sprintf("/%d", zone.ID)), nil, http.StatusNotFound},
		{"owner can delete", owner, http.MethodDelete, path, nil, http.StatusNoContent},
		{"deleted", owner, http.MethodGet, path, nil, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, ts.do(t, tt.method, tt.path, tt.token, tt.body), tt.want)
		})
	}
}
