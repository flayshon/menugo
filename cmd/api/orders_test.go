package main

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"menugo.flayshon.com/internal/data"
)

func orderPath(restaurantID int64, rest string) string {
	return fmt.Sprintf("/v1/restaurants/%d/orders%s", restaurantID, rest)
}

// orderID finds the ID of a placed order from the restaurant's order list:
// customers never see order IDs.
func (s *shop) newestOrderID(t *testing.T) int64 {
	t.Helper()
	res := s.ts.do(t, http.MethodGet, orderPath(s.restaurant.ID, "?page_size=1"), s.owner, nil)
	assertStatus(t, res, http.StatusOK)
	orders := decode[struct {
		Orders []orderResponse `json:"orders"`
	}](t, res.body).Orders
	if len(orders) != 1 {
		t.Fatalf("no orders")
	}
	return orders[0].ID
}

func (s *shop) setStatus(t *testing.T, token string, orderID int64, body map[string]any) response {
	t.Helper()
	return s.ts.do(t, http.MethodPatch, orderPath(s.restaurant.ID, fmt.Sprintf("/%d", orderID)), token, body)
}

func TestOrderLifecycle(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	ownerID := s.userID(t, s.owner)
	placed := s.placeOrder(t, s.deliveryOrder())
	id := s.newestOrderID(t)
	path := orderPath(s.restaurant.ID, fmt.Sprintf("/%d", id))

	res := s.ts.do(t, http.MethodGet, path, s.owner, nil)
	assertStatus(t, res, http.StatusOK)
	o := decode[struct {
		Order orderResponse `json:"order"`
	}](t, res.body).Order

	// The restaurant sees what the customer can't.
	if o.Customer.Name != "Ana Souza" || o.Customer.Phone != "+5581999990000" || o.Address == nil || o.Address.Details != "apto 12" {
		t.Errorf("order = %+v", o)
	}
	if o.DeliveryZoneID == nil || len(o.Items) != 2 || o.Items[0].MenuItemID == nil || *o.Items[0].MenuItemID != s.pizza.ID {
		t.Errorf("order = %+v", o)
	}
	if !slices.Equal(o.NextStatuses, []data.OrderStatus{data.StatusConfirmed, data.StatusCancelled}) {
		t.Errorf("next_statuses = %v", o.NextStatuses)
	}
	if len(o.StatusHistory) != 1 || o.StatusHistory[0].From != nil || o.StatusHistory[0].ActorType != data.ActorCustomer {
		t.Errorf("history = %+v", o.StatusHistory)
	}

	t.Run("invalid moves are rejected", func(t *testing.T) {
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "delivered"}), http.StatusConflict)
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "ready_for_pickup"}), http.StatusConflict)
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "lost"}), http.StatusUnprocessableEntity)
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "confirmed", "reason": "x"}), http.StatusUnprocessableEntity)
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "confirmed", "total_cents": 1}), http.StatusBadRequest)
	})

	t.Run("walk the delivery path", func(t *testing.T) {
		for _, status := range []data.OrderStatus{data.StatusConfirmed, data.StatusPreparing, data.StatusReadyForDelivery, data.StatusOutForDelivery, data.StatusDelivered} {
			res := s.setStatus(t, s.owner, id, map[string]any{"status": status})
			assertStatus(t, res, http.StatusOK)
			if got := decode[struct {
				Order orderResponse `json:"order"`
			}](t, res.body).Order.Status; got != status {
				t.Fatalf("status = %s; want %s", got, status)
			}
		}

		res := s.ts.do(t, http.MethodGet, path, s.owner, nil)
		o := decode[struct {
			Order orderResponse `json:"order"`
		}](t, res.body).Order
		if len(o.StatusHistory) != 6 || len(o.NextStatuses) != 0 {
			t.Errorf("history = %d entries, next = %v", len(o.StatusHistory), o.NextStatuses)
		}
		last := o.StatusHistory[5]
		if *last.From != data.StatusOutForDelivery || last.To != data.StatusDelivered || last.ActorUserID == nil || *last.ActorUserID != ownerID {
			t.Errorf("last change = %+v", last)
		}

		// Final.
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "cancelled"}), http.StatusConflict)
	})

	t.Run("the customer sees the progress", func(t *testing.T) {
		res := s.ts.do(t, http.MethodGet, "/v1/tracking/"+placed.TrackingToken, "", nil)
		o := decode[struct {
			Order publicOrderResponse `json:"order"`
		}](t, res.body).Order
		if o.Status != data.StatusDelivered || len(o.StatusHistory) != 6 {
			t.Errorf("tracked = %+v", o)
		}
	})
}

func TestCancelOrderWithReason(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	body := s.deliveryOrder()
	body["fulfillment"] = "pickup"
	delete(body, "address")
	placed := s.placeOrder(t, body)
	id := s.newestOrderID(t)

	res := s.ts.do(t, http.MethodGet, orderPath(s.restaurant.ID, fmt.Sprintf("/%d", id)), s.owner, nil)
	o := decode[struct {
		Order orderResponse `json:"order"`
	}](t, res.body).Order
	if o.Address != nil || o.DeliveryZoneID != nil {
		t.Errorf("pickup order has address %+v, zone %v", o.Address, o.DeliveryZoneID)
	}

	for _, status := range []string{"confirmed", "preparing", "ready_for_pickup"} {
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": status}), http.StatusOK)
	}
	res = s.setStatus(t, s.owner, id, map[string]any{"status": "cancelled", "reason": "Customer did not show up"})
	assertStatus(t, res, http.StatusOK)

	res = s.ts.do(t, http.MethodGet, "/v1/tracking/"+placed.TrackingToken, "", nil)
	tracked := decode[struct {
		Order publicOrderResponse `json:"order"`
	}](t, res.body).Order
	last := tracked.StatusHistory[len(tracked.StatusHistory)-1]
	if tracked.Status != data.StatusCancelled || last.Reason != "Customer did not show up" {
		t.Errorf("tracked = %+v", tracked)
	}
}

func TestListOrdersQuery(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	for range 3 {
		s.placeOrder(t, s.deliveryOrder())
	}
	first := s.newestOrderID(t) - 2
	assertStatus(t, s.setStatus(t, s.owner, first, map[string]any{"status": "confirmed"}), http.StatusOK)

	res := s.ts.do(t, http.MethodGet, orderPath(s.restaurant.ID, "?status=confirmed,preparing"), s.owner, nil)
	assertStatus(t, res, http.StatusOK)
	got := decode[struct {
		Orders   []orderResponse `json:"orders"`
		Metadata struct {
			TotalRecords int `json:"total_records"`
			LastPage     int `json:"last_page"`
		} `json:"metadata"`
	}](t, res.body)
	if len(got.Orders) != 1 || got.Orders[0].ID != first || got.Metadata.TotalRecords != 1 {
		t.Errorf("confirmed orders = %+v", got)
	}
	if len(got.Orders[0].Items) != 2 || got.Orders[0].StatusHistory != nil {
		t.Errorf("list entries should have items but no history: %+v", got.Orders[0])
	}

	res = s.ts.do(t, http.MethodGet, orderPath(s.restaurant.ID, "?sort=created_at&page_size=2&page=2"), s.owner, nil)
	assertStatus(t, res, http.StatusOK)
	got = decode[struct {
		Orders   []orderResponse `json:"orders"`
		Metadata struct {
			TotalRecords int `json:"total_records"`
			LastPage     int `json:"last_page"`
		} `json:"metadata"`
	}](t, res.body)
	if len(got.Orders) != 1 || got.Orders[0].ID != first+2 || got.Metadata.TotalRecords != 3 || got.Metadata.LastPage != 2 {
		t.Errorf("page 2 = %+v", got)
	}

	for _, query := range []string{"?status=lost", "?fulfillment=drone", "?page_size=1000", "?page=0", "?page=x", "?from=yesterday", "?sort=total", "?from=2026-10-09T00:00:00Z&to=2026-10-08T00:00:00Z"} {
		t.Run(query, func(t *testing.T) {
			assertStatus(t, s.ts.do(t, http.MethodGet, orderPath(s.restaurant.ID, query), s.owner, nil), http.StatusUnprocessableEntity)
		})
	}
}

func TestOrderAccess(t *testing.T) {
	t.Parallel()
	s := newShop(t)
	s.placeOrder(t, s.deliveryOrder())
	id := s.newestOrderID(t)
	path := orderPath(s.restaurant.ID, fmt.Sprintf("/%d", id))

	_, staff := s.ts.signUp(t, "staff@example.com")
	_, driver := s.ts.signUp(t, "driver@example.com")
	_, stranger := s.ts.signUp(t, "stranger@example.com")
	assertStatus(t, s.ts.addMember(t, s.owner, s.restaurant.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)
	assertStatus(t, s.ts.addMember(t, s.owner, s.restaurant.ID, "driver@example.com", data.RoleDriver), http.StatusCreated)
	strangers := s.ts.createRestaurant(t, stranger, "strangers")
	viaOwnRestaurant := orderPath(strangers.ID, fmt.Sprintf("/%d", id))

	tests := []struct {
		name   string
		token  string
		method string
		path   string
		body   any
		want   int
	}{
		{"staff can list", staff, http.MethodGet, orderPath(s.restaurant.ID, ""), nil, http.StatusOK},
		{"staff can view", staff, http.MethodGet, path, nil, http.StatusOK},
		{"staff can confirm", staff, http.MethodPatch, path, map[string]any{"status": "confirmed"}, http.StatusOK},
		{"driver cannot list", driver, http.MethodGet, orderPath(s.restaurant.ID, ""), nil, http.StatusForbidden},
		{"driver cannot change status", driver, http.MethodPatch, path, map[string]any{"status": "preparing"}, http.StatusForbidden},
		{"anonymous", "", http.MethodGet, path, nil, http.StatusUnauthorized},
		{"other restaurant: list", stranger, http.MethodGet, orderPath(s.restaurant.ID, ""), nil, http.StatusNotFound},
		{"other restaurant: view", stranger, http.MethodGet, path, nil, http.StatusNotFound},
		{"other restaurant: view through own", stranger, http.MethodGet, viaOwnRestaurant, nil, http.StatusNotFound},
		{"other restaurant: cancel through own", stranger, http.MethodPatch, viaOwnRestaurant, map[string]any{"status": "cancelled"}, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, s.ts.do(t, tt.method, tt.path, tt.token, tt.body), tt.want)
		})
	}

	res := s.ts.do(t, http.MethodGet, orderPath(strangers.ID, ""), stranger, nil)
	if orders := decode[struct {
		Orders []orderResponse `json:"orders"`
	}](t, res.body).Orders; len(orders) != 0 {
		t.Errorf("stranger sees orders: %+v", orders)
	}

	res = s.ts.do(t, http.MethodGet, path, s.owner, nil)
	if o := decode[struct {
		Order orderResponse `json:"order"`
	}](t, res.body).Order; o.Status != data.StatusConfirmed {
		t.Errorf("status = %s; want confirmed (and not cancelled by the stranger)", o.Status)
	}
}

// userID returns the ID of the user a token belongs to.
func (s *shop) userID(t *testing.T, token string) int64 {
	t.Helper()
	res := s.ts.do(t, http.MethodGet, "/v1/users/me", token, nil)
	assertStatus(t, res, http.StatusOK)
	return decode[struct {
		User userResponse `json:"user"`
	}](t, res.body).User.ID
}
