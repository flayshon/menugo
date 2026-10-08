package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"menugo.flayshon.com/internal/data"
)

// shop is a published restaurant with a pizza (45.00), a soda (6.00) and a
// delivery zone for postal codes starting 50030 (fee 8.00, minimum 50.00).
type shop struct {
	ts         *testServer
	app        *application
	owner      string
	restaurant restaurantResponse
	pizza      menuItemResponse
	soda       menuItemResponse
}

func newShop(t *testing.T) *shop {
	t.Helper()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	s := &shop{ts: ts, app: app}
	_, s.owner = ts.signUp(t, "owner@example.com")
	s.restaurant = ts.createRestaurant(t, s.owner, "pizza")

	c := ts.createCategory(t, s.owner, s.restaurant.ID, "Menu", 0)
	s.pizza = ts.createMenuItem(t, s.owner, s.restaurant.ID, c.ID, "Pizza", 4500)
	s.soda = ts.createMenuItem(t, s.owner, s.restaurant.ID, c.ID, "Soda", 600)
	ts.createZone(t, s.owner, s.restaurant.ID, map[string]any{
		"name": "Centre", "fee_cents": 800, "min_order_cents": 5000, "postal_codes": []string{"50030"},
	})

	res := ts.do(t, http.MethodPatch, restaurantPath(s.restaurant.ID), s.owner, map[string]any{"is_published": true})
	assertStatus(t, res, http.StatusOK)
	return s
}

func (s *shop) deliveryOrder() map[string]any {
	return map[string]any{
		"fulfillment": "delivery",
		"customer":    map[string]any{"name": "Ana Souza", "phone": "+55 (81) 99999-0000"},
		"address":     map[string]any{"line": "Rua da Aurora, 100", "details": "apto 12", "city": "Recife", "postal_code": "50030-230"},
		"items": []map[string]any{
			{"item_id": s.pizza.ID, "quantity": 1, "notes": "no olives"},
			{"item_id": s.soda.ID, "quantity": 2},
		},
	}
}

type placedOrder struct {
	Order         publicOrderResponse `json:"order"`
	TrackingToken string              `json:"tracking_token"`
}

func (s *shop) placeOrder(t *testing.T, body map[string]any) placedOrder {
	t.Helper()
	res := s.ts.do(t, http.MethodPost, "/v1/menus/pizza/orders", "", body)
	assertStatus(t, res, http.StatusCreated)
	return decode[placedOrder](t, res.body)
}

func TestDeliveryQuote(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	res := s.ts.do(t, http.MethodGet, "/v1/menus/pizza/delivery-quote?postal_code=50030-230", "", nil)
	assertStatus(t, res, http.StatusOK)
	if res.header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing Access-Control-Allow-Origin")
	}
	quote := decode[struct {
		Quote struct {
			PostalCode    string `json:"postal_code"`
			FeeCents      int64  `json:"fee_cents"`
			MinOrderCents int64  `json:"min_order_cents"`
		} `json:"delivery_quote"`
	}](t, res.body).Quote
	if quote.PostalCode != "50030230" || quote.FeeCents != 800 || quote.MinOrderCents != 5000 {
		t.Errorf("quote = %+v", quote)
	}

	assertStatus(t, s.ts.do(t, http.MethodGet, "/v1/menus/pizza/delivery-quote?postal_code=99999-999", "", nil), http.StatusUnprocessableEntity)
	assertStatus(t, s.ts.do(t, http.MethodGet, "/v1/menus/pizza/delivery-quote", "", nil), http.StatusUnprocessableEntity)
	assertStatus(t, s.ts.do(t, http.MethodGet, "/v1/menus/nowhere/delivery-quote?postal_code=50030", "", nil), http.StatusNotFound)
}

func TestPlaceOrder(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	res := s.ts.do(t, http.MethodPost, "/v1/menus/pizza/orders", "", s.deliveryOrder())
	assertStatus(t, res, http.StatusCreated)
	if res.header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing Access-Control-Allow-Origin")
	}

	placed := decode[placedOrder](t, res.body)
	o := placed.Order
	if o.Status != data.StatusPending || o.SubtotalCents != 5700 || o.DeliveryFeeCents != 800 || o.TotalCents != 6500 || o.Currency != "BRL" {
		t.Errorf("order = %+v", o)
	}
	if len(o.Items) != 2 || o.Items[0].Notes != "no olives" || o.Items[1].LineTotalCents != 1200 {
		t.Errorf("items = %+v", o.Items)
	}
	if !o.CanCancel || len(o.StatusHistory) != 1 || o.Restaurant.Slug != "pizza" {
		t.Errorf("order = %+v", o)
	}
	if len(placed.TrackingToken) != 26 || res.header.Get("Location") != "/v1/tracking/"+placed.TrackingToken {
		t.Errorf("token = %q, Location = %q", placed.TrackingToken, res.header.Get("Location"))
	}

	// A tracking link may be shared, so it must not reveal who ordered or where to.
	for _, private := range []string{"Ana", "99999", "Aurora", "apto", "50030"} {
		if strings.Contains(string(res.body), private) {
			t.Errorf("order response contains %q:\n%s", private, res.body)
		}
	}

	t.Run("the restaurant sees the full order", func(t *testing.T) {
		stored, err := s.app.models.Orders.GetByTrackingToken(context.Background(), placed.TrackingToken)
		if err != nil {
			t.Fatal(err)
		}
		if stored.CustomerPhone != "+5581999990000" || stored.Address.PostalCode != "50030230" || stored.Address.Details != "apto 12" {
			t.Errorf("stored order = %+v", stored)
		}
	})

	t.Run("pickup", func(t *testing.T) {
		body := s.deliveryOrder()
		body["fulfillment"] = "pickup"
		delete(body, "address")
		body["items"] = []map[string]any{{"item_id": s.soda.ID, "quantity": 1}}

		o := s.placeOrder(t, body).Order
		if o.Fulfillment != data.FulfillmentPickup || o.DeliveryFeeCents != 0 || o.TotalCents != 600 {
			t.Errorf("order = %+v", o)
		}
	})

	t.Run("matching expected total", func(t *testing.T) {
		body := s.deliveryOrder()
		body["expected_total_cents"] = 6500
		s.placeOrder(t, body)
	})
}

func TestPlaceOrderRejections(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	_, other := s.ts.signUp(t, "other@example.com")
	theirs := s.ts.createRestaurant(t, other, "theirs")
	theirCategory := s.ts.createCategory(t, other, theirs.ID, "Menu", 0)
	theirItem := s.ts.createMenuItem(t, other, theirs.ID, theirCategory.ID, "Theirs", 100)

	soldOut := s.ts.createMenuItem(t, s.owner, s.restaurant.ID, s.pizza.CategoryID, "Sold out", 100)
	assertStatus(t, s.ts.do(t, http.MethodPut, menuPath(s.restaurant.ID, fmt.Sprintf("/items/%d/availability", soldOut.ID)), s.owner,
		map[string]any{"is_available": false}), http.StatusOK)

	tests := []struct {
		name   string
		modify func(map[string]any)
		status int
		field  string
	}{
		{"pickup with an address", func(b map[string]any) { b["fulfillment"] = "pickup" }, 422, "address"},
		{"delivery without an address", func(b map[string]any) { delete(b, "address") }, 422, "address.line"},
		{"bad phone", func(b map[string]any) { b["customer"] = map[string]any{"name": "Ana", "phone": "123"} }, 422, "customer.phone"},
		{"no items", func(b map[string]any) { b["items"] = []map[string]any{} }, 422, "items"},
		{"zero quantity", func(b map[string]any) { b["items"] = []map[string]any{{"item_id": s.pizza.ID, "quantity": 0}} }, 422, "items[0].quantity"},
		{"sold out item", func(b map[string]any) {
			b["items"] = []map[string]any{{"item_id": s.pizza.ID, "quantity": 2}, {"item_id": soldOut.ID, "quantity": 1}}
		}, 422, "items[1].item_id"},
		{"another restaurant's item", func(b map[string]any) {
			b["items"] = []map[string]any{{"item_id": s.pizza.ID, "quantity": 2}, {"item_id": theirItem.ID, "quantity": 1}}
		}, 422, "items[1].item_id"},
		{"outside delivery area", func(b map[string]any) {
			b["address"] = map[string]any{"line": "x", "city": "y", "postal_code": "99999-999"}
		}, 422, "address.postal_code"},
		{"below minimum", func(b map[string]any) { b["items"] = []map[string]any{{"item_id": s.soda.ID, "quantity": 1}} }, 422, "items"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := s.deliveryOrder()
			tt.modify(body)
			res := s.ts.do(t, http.MethodPost, "/v1/menus/pizza/orders", "", body)
			assertStatus(t, res, tt.status)
			if _, ok := validationErrors(t, res.body)[tt.field]; !ok {
				t.Errorf("expected an error for %q: %s", tt.field, res.body)
			}
		})
	}

	t.Run("client cannot set prices", func(t *testing.T) {
		body := s.deliveryOrder()
		body["items"] = []map[string]any{{"item_id": s.pizza.ID, "quantity": 2, "price_cents": 1}}
		assertStatus(t, s.ts.do(t, http.MethodPost, "/v1/menus/pizza/orders", "", body), http.StatusBadRequest)
	})

	t.Run("total changed", func(t *testing.T) {
		body := s.deliveryOrder()
		body["expected_total_cents"] = 6000
		res := s.ts.do(t, http.MethodPost, "/v1/menus/pizza/orders", "", body)
		assertStatus(t, res, http.StatusConflict)
		got := decode[struct {
			TotalCents int64 `json:"total_cents"`
		}](t, res.body)
		if got.TotalCents != 6500 {
			t.Errorf("total_cents = %d; want 6500", got.TotalCents)
		}
	})

	t.Run("unpublished restaurant", func(t *testing.T) {
		body := s.deliveryOrder()
		body["items"] = []map[string]any{{"item_id": theirItem.ID, "quantity": 1}}
		assertStatus(t, s.ts.do(t, http.MethodPost, "/v1/menus/theirs/orders", "", body), http.StatusNotFound)
	})
}

func TestTrackingAndCustomerCancel(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	placed := s.placeOrder(t, s.deliveryOrder())
	trackPath := "/v1/tracking/" + placed.TrackingToken

	res := s.ts.do(t, http.MethodGet, trackPath, "", nil)
	assertStatus(t, res, http.StatusOK)
	tracked := decode[struct {
		Order publicOrderResponse `json:"order"`
	}](t, res.body).Order
	if tracked.TotalCents != 6500 || tracked.Status != data.StatusPending {
		t.Errorf("tracked = %+v", tracked)
	}

	assertStatus(t, s.ts.do(t, http.MethodGet, "/v1/tracking/"+strings.Repeat("A", 26), "", nil), http.StatusNotFound)
	assertStatus(t, s.ts.do(t, http.MethodGet, "/v1/tracking/short", "", nil), http.StatusNotFound)

	t.Run("customer can cancel a pending order once", func(t *testing.T) {
		res := s.ts.do(t, http.MethodPost, trackPath+"/cancel", "", nil)
		assertStatus(t, res, http.StatusOK)
		o := decode[struct {
			Order publicOrderResponse `json:"order"`
		}](t, res.body).Order
		if o.Status != data.StatusCancelled || o.CanCancel || len(o.StatusHistory) != 2 {
			t.Errorf("order = %+v", o)
		}

		assertStatus(t, s.ts.do(t, http.MethodPost, trackPath+"/cancel", "", nil), http.StatusConflict)
	})

	t.Run("customer cannot cancel once confirmed", func(t *testing.T) {
		placed := s.placeOrder(t, s.deliveryOrder())
		stored, err := s.app.models.Orders.GetByTrackingToken(context.Background(), placed.TrackingToken)
		if err != nil {
			t.Fatal(err)
		}
		err = s.app.models.Orders.Transition(context.Background(), stored.RestaurantID, stored.ID, data.StatusConfirmed, data.Actor{Type: data.ActorUser}, "")
		if err != nil {
			t.Fatal(err)
		}

		res := s.ts.do(t, http.MethodGet, "/v1/tracking/"+placed.TrackingToken, "", nil)
		if o := decode[struct {
			Order publicOrderResponse `json:"order"`
		}](t, res.body).Order; o.CanCancel {
			t.Error("can_cancel should be false once confirmed")
		}
		assertStatus(t, s.ts.do(t, http.MethodPost, "/v1/tracking/"+placed.TrackingToken+"/cancel", "", nil), http.StatusConflict)
	})
}

func TestPublicPreflight(t *testing.T) {
	app := newTestApplication(t)
	ts := newTestServer(t, app.routes())

	for _, path := range []string{"/v1/menus/pizza/orders", "/v1/tracking/" + strings.Repeat("A", 26) + "/cancel"} {
		res := ts.do(t, http.MethodOptions, path, "", nil)
		assertStatus(t, res, http.StatusNoContent)
		if res.header.Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(res.header.Get("Access-Control-Allow-Headers"), "Content-Type") {
			t.Errorf("%s: headers = %v", path, res.header)
		}
	}
}

func TestTrackingTokensAreNotLogged(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	var logs bytes.Buffer
	s.app.logger = slog.New(slog.NewTextHandler(&logs, nil))

	placed := s.placeOrder(t, s.deliveryOrder())
	token := placed.TrackingToken

	s.ts.do(t, http.MethodGet, "/v1/tracking/"+token, "", nil)
	s.ts.do(t, http.MethodPost, "/v1/tracking/"+token+"/cancel", "", nil)
	// Rejected by the authenticate middleware, before the handler runs.
	s.ts.do(t, http.MethodGet, "/v1/tracking/"+token, "not-a-valid-token", nil)

	if strings.Contains(logs.String(), token) {
		t.Errorf("tracking token found in logs:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "path=/v1/tracking/{token}") {
		t.Errorf("expected the route pattern in the logs:\n%s", logs.String())
	}
}

func TestDeletedRestaurant(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	placed := s.placeOrder(t, s.deliveryOrder())
	id := s.newestOrderID(t)

	res := s.ts.do(t, http.MethodDelete, restaurantPath(s.restaurant.ID), s.owner, nil)
	assertStatus(t, res, http.StatusConflict)

	assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": "cancelled"}), http.StatusOK)
	assertStatus(t, s.ts.do(t, http.MethodDelete, restaurantPath(s.restaurant.ID), s.owner, nil), http.StatusNoContent)

	for _, path := range []string{restaurantPath(s.restaurant.ID), orderPath(s.restaurant.ID, ""), menuPath(s.restaurant.ID, "")} {
		assertStatus(t, s.ts.do(t, http.MethodGet, path, s.owner, nil), http.StatusNotFound)
	}
	assertStatus(t, s.ts.do(t, http.MethodGet, "/v1/menus/pizza", "", nil), http.StatusNotFound)

	// The customer can still see what happened to their order.
	res = s.ts.do(t, http.MethodGet, "/v1/tracking/"+placed.TrackingToken, "", nil)
	assertStatus(t, res, http.StatusOK)

	// And the slug is free again.
	s.ts.createRestaurant(t, s.owner, "pizza")
}
