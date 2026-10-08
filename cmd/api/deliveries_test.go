package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"menugo.flayshon.com/internal/data"
)

func (s *shop) path(rest string) string {
	return fmt.Sprintf("/v1/restaurants/%d%s", s.restaurant.ID, rest)
}

// newDriver registers a user and makes them a driver of the shop.
func (s *shop) newDriver(t *testing.T, email, name string) (driverResponse, string) {
	t.Helper()
	_, token := s.ts.signUp(t, email)
	res := s.ts.do(t, http.MethodPost, s.path("/drivers"), s.owner, map[string]any{
		"email": email, "name": name, "phone": "(81) 98888-7777", "vehicle": "Honda CG 160",
	})
	assertStatus(t, res, http.StatusCreated)
	return decode[struct {
		Driver driverResponse `json:"driver"`
	}](t, res.body).Driver, token
}

// orderIn places a delivery order, moves it through the given statuses and
// returns its ID and tracking token.
func (s *shop) orderIn(t *testing.T, statuses ...string) (int64, string) {
	t.Helper()
	placed := s.placeOrder(t, s.deliveryOrder())
	id := s.newestOrderID(t)
	for _, status := range statuses {
		assertStatus(t, s.setStatus(t, s.owner, id, map[string]any{"status": status}), http.StatusOK)
	}
	return id, placed.TrackingToken
}

func (s *shop) assign(t *testing.T, token string, orderID, driverID int64) response {
	t.Helper()
	return s.ts.do(t, http.MethodPut, s.path(fmt.Sprintf("/orders/%d/driver", orderID)), token, map[string]any{"driver_id": driverID})
}

func (s *shop) staffOrder(t *testing.T, orderID int64) orderResponse {
	t.Helper()
	res := s.ts.do(t, http.MethodGet, s.path(fmt.Sprintf("/orders/%d", orderID)), s.owner, nil)
	assertStatus(t, res, http.StatusOK)
	return decode[struct {
		Order orderResponse `json:"order"`
	}](t, res.body).Order
}

func myDeliveries(t *testing.T, ts *testServer, token, query string) []driverDeliveryResponse {
	t.Helper()
	res := ts.do(t, http.MethodGet, "/v1/me/deliveries"+query, token, nil)
	assertStatus(t, res, http.StatusOK)
	return decode[struct {
		Deliveries []driverDeliveryResponse `json:"deliveries"`
	}](t, res.body).Deliveries
}

func TestDriverManagement(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	joao, _ := s.newDriver(t, "joao@example.com", "João Silva")
	if joao.Phone != "81988887777" || joao.Email != "joao@example.com" || !joao.IsActive {
		t.Errorf("driver = %+v", joao)
	}

	res := s.ts.do(t, http.MethodGet, s.path("/members"), s.owner, nil)
	members := decode[struct {
		Members []memberResponse `json:"members"`
	}](t, res.body).Members
	if !slices.ContainsFunc(members, func(m memberResponse) bool { return m.Email == "joao@example.com" && m.Role == data.RoleDriver }) {
		t.Errorf("João should have become a driver member: %+v", members)
	}

	_, staffToken := s.ts.signUp(t, "staff@example.com")
	assertStatus(t, s.ts.addMember(t, s.owner, s.restaurant.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)
	_, driverToken := s.ts.signUp(t, "maria@example.com")
	s.ts.do(t, http.MethodPost, s.path("/drivers"), s.owner, map[string]any{"email": "maria@example.com", "phone": "81988887777"})

	tests := []struct {
		name   string
		token  string
		method string
		path   string
		body   any
		want   int
	}{
		{"already a driver", s.owner, http.MethodPost, s.path("/drivers"), map[string]any{"email": "joao@example.com", "phone": "81988887777"}, http.StatusConflict},
		{"staff member can't also be a driver", s.owner, http.MethodPost, s.path("/drivers"), map[string]any{"email": "staff@example.com", "phone": "81988887777"}, http.StatusUnprocessableEntity},
		{"unregistered email", s.owner, http.MethodPost, s.path("/drivers"), map[string]any{"email": "ghost@example.com", "phone": "81988887777"}, http.StatusUnprocessableEntity},
		{"phone required", s.owner, http.MethodPost, s.path("/drivers"), map[string]any{"email": "x@example.com"}, http.StatusUnprocessableEntity},
		{"staff can list", staffToken, http.MethodGet, s.path("/drivers?active=true"), nil, http.StatusOK},
		{"staff cannot create", staffToken, http.MethodPost, s.path("/drivers"), map[string]any{"email": "x@example.com", "phone": "81988887777"}, http.StatusForbidden},
		{"drivers cannot list drivers", driverToken, http.MethodGet, s.path("/drivers"), nil, http.StatusForbidden},
		{"bad active filter", s.owner, http.MethodGet, s.path("/drivers?active=yes"), nil, http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, s.ts.do(t, tt.method, tt.path, tt.token, tt.body), tt.want)
		})
	}

	t.Run("deactivated drivers drop out of the active list", func(t *testing.T) {
		res := s.ts.do(t, http.MethodPatch, s.path(fmt.Sprintf("/drivers/%d", joao.ID)), s.owner, map[string]any{"is_active": false})
		assertStatus(t, res, http.StatusOK)

		res = s.ts.do(t, http.MethodGet, s.path("/drivers?active=true"), s.owner, nil)
		drivers := decode[struct {
			Drivers []driverResponse `json:"drivers"`
		}](t, res.body).Drivers
		if len(drivers) != 1 || drivers[0].Email != "maria@example.com" {
			t.Errorf("active drivers = %+v", drivers)
		}
	})
}

func TestDeliveryByDriver(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	joao, joaoToken := s.newDriver(t, "joao@example.com", "João Silva")
	orderID, tracking := s.orderIn(t, "confirmed")

	res := s.assign(t, s.owner, orderID, joao.ID)
	assertStatus(t, res, http.StatusOK)
	dl := decode[struct {
		Delivery deliveryResponse `json:"delivery"`
	}](t, res.body).Delivery
	if dl.Status != data.DeliveryAssigned || dl.Driver == nil || dl.Driver.Name != "João Silva" || dl.PickedUpAt != nil {
		t.Errorf("delivery = %+v", dl)
	}

	if o := s.staffOrder(t, orderID); o.Delivery == nil || o.Delivery.ID != dl.ID {
		t.Errorf("order delivery = %+v", o.Delivery)
	}

	res = s.ts.do(t, http.MethodGet, "/v1/tracking/"+tracking, "", nil)
	if o := decode[struct {
		Order publicOrderResponse `json:"order"`
	}](t, res.body).Order; o.Driver == nil || *o.Driver != "João" {
		t.Errorf("customer should see the driver's first name only: %+v", o.Driver)
	}
	if strings.Contains(string(res.body), "Silva") || strings.Contains(string(res.body), "98888") {
		t.Errorf("tracking exposes driver details: %s", res.body)
	}

	mine := myDeliveries(t, s.ts, joaoToken, "?status=assigned,picked_up")
	if len(mine) != 1 || mine[0].ID != dl.ID {
		t.Fatalf("driver's deliveries = %+v", mine)
	}
	if mine[0].Order.Customer == nil || mine[0].Order.Customer.Phone != "+5581999990000" || mine[0].Order.Address == nil || mine[0].Restaurant.Name == "" {
		t.Errorf("the driver needs the customer and address while delivering: %+v", mine[0].Order)
	}

	myPath := fmt.Sprintf("/v1/me/deliveries/%d", dl.ID)

	t.Run("not before the kitchen is done", func(t *testing.T) {
		assertStatus(t, s.ts.do(t, http.MethodPatch, myPath, joaoToken, map[string]any{"status": "picked_up"}), http.StatusConflict)
	})
	t.Run("drivers can only set picked_up and delivered", func(t *testing.T) {
		assertStatus(t, s.ts.do(t, http.MethodPatch, myPath, joaoToken, map[string]any{"status": "cancelled"}), http.StatusUnprocessableEntity)
	})

	for _, status := range []string{"preparing", "ready_for_delivery"} {
		assertStatus(t, s.setStatus(t, s.owner, orderID, map[string]any{"status": status}), http.StatusOK)
	}

	res = s.ts.do(t, http.MethodPatch, myPath, joaoToken, map[string]any{"status": "picked_up"})
	assertStatus(t, res, http.StatusOK)
	if got := decode[struct {
		Delivery driverDeliveryResponse `json:"delivery"`
	}](t, res.body).Delivery; got.Status != data.DeliveryPickedUp || got.Order.Status != data.StatusOutForDelivery || got.PickedUpAt == nil {
		t.Errorf("after pickup = %+v", got)
	}

	t.Run("drivers can't be changed once the order is out", func(t *testing.T) {
		assertStatus(t, s.assign(t, s.owner, orderID, joao.ID), http.StatusConflict)
	})

	res = s.ts.do(t, http.MethodPatch, myPath, joaoToken, map[string]any{"status": "delivered"})
	assertStatus(t, res, http.StatusOK)
	done := decode[struct {
		Delivery driverDeliveryResponse `json:"delivery"`
	}](t, res.body).Delivery
	if done.Status != data.DeliveryDelivered || done.Order.Status != data.StatusDelivered {
		t.Errorf("after delivery = %+v", done)
	}
	if done.Order.Customer != nil || done.Order.Address != nil {
		t.Errorf("customer details should be hidden once delivered: %+v", done.Order)
	}

	o := s.staffOrder(t, orderID)
	last := o.StatusHistory[len(o.StatusHistory)-1]
	if last.ActorUserID == nil || *last.ActorUserID != joao.UserID || o.Delivery.Status != data.DeliveryDelivered {
		t.Errorf("last change = %+v, delivery = %+v", last, o.Delivery)
	}

	res = s.ts.do(t, http.MethodGet, "/v1/tracking/"+tracking, "", nil)
	if o := decode[struct {
		Order publicOrderResponse `json:"order"`
	}](t, res.body).Order; o.Status != data.StatusDelivered || o.Driver != nil {
		t.Errorf("tracking after delivery = %+v", o)
	}
}

func TestStaffCanCompleteDeliveries(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	joao, _ := s.newDriver(t, "joao@example.com", "João")
	orderID, _ := s.orderIn(t, "confirmed", "preparing", "ready_for_delivery")
	assertStatus(t, s.assign(t, s.owner, orderID, joao.ID), http.StatusOK)

	// The driver's phone died: staff mark it on their behalf.
	for _, status := range []string{"out_for_delivery", "delivered"} {
		assertStatus(t, s.setStatus(t, s.owner, orderID, map[string]any{"status": status}), http.StatusOK)
	}

	o := s.staffOrder(t, orderID)
	if o.Delivery.Status != data.DeliveryDelivered || o.Delivery.PickedUpAt == nil || o.Delivery.DeliveredAt == nil {
		t.Errorf("delivery = %+v", o.Delivery)
	}
}

func TestReassignAndUnassign(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	joao, joaoToken := s.newDriver(t, "joao@example.com", "João")
	maria, mariaToken := s.newDriver(t, "maria@example.com", "Maria")
	orderID, _ := s.orderIn(t, "confirmed")

	assertStatus(t, s.assign(t, s.owner, orderID, joao.ID), http.StatusOK)
	old := myDeliveries(t, s.ts, joaoToken, "")[0]

	assertStatus(t, s.assign(t, s.owner, orderID, maria.ID), http.StatusOK)
	if o := s.staffOrder(t, orderID); o.Delivery.Driver.ID != maria.ID {
		t.Errorf("driver = %+v; want Maria", o.Delivery.Driver)
	}

	mine := myDeliveries(t, s.ts, joaoToken, "")
	if len(mine) != 1 || mine[0].Status != data.DeliveryUnassigned || mine[0].Order.Customer != nil {
		t.Errorf("João's old delivery = %+v", mine)
	}
	if active := myDeliveries(t, s.ts, joaoToken, "?status=assigned,picked_up"); len(active) != 0 {
		t.Errorf("João still has active deliveries: %+v", active)
	}

	for _, status := range []string{"preparing", "ready_for_delivery"} {
		assertStatus(t, s.setStatus(t, s.owner, orderID, map[string]any{"status": status}), http.StatusOK)
	}
	res := s.ts.do(t, http.MethodPatch, fmt.Sprintf("/v1/me/deliveries/%d", old.ID), joaoToken, map[string]any{"status": "picked_up"})
	assertStatus(t, res, http.StatusConflict)

	t.Run("other drivers can't see or touch it", func(t *testing.T) {
		current := myDeliveries(t, s.ts, mariaToken, "")[0]
		path := fmt.Sprintf("/v1/me/deliveries/%d", current.ID)
		assertStatus(t, s.ts.do(t, http.MethodGet, path, joaoToken, nil), http.StatusNotFound)
		assertStatus(t, s.ts.do(t, http.MethodPatch, path, joaoToken, map[string]any{"status": "picked_up"}), http.StatusNotFound)
	})

	path := s.path(fmt.Sprintf("/orders/%d/driver", orderID))
	assertStatus(t, s.ts.do(t, http.MethodDelete, path, s.owner, nil), http.StatusNoContent)
	assertStatus(t, s.ts.do(t, http.MethodDelete, path, s.owner, nil), http.StatusNotFound)
	if o := s.staffOrder(t, orderID); o.Delivery != nil {
		t.Errorf("delivery after unassigning = %+v", o.Delivery)
	}
}

func TestAssignmentRules(t *testing.T) {
	t.Parallel()
	s := newShop(t)

	joao, _ := s.newDriver(t, "joao@example.com", "João")
	_, driverToken := s.newDriver(t, "maria@example.com", "Maria")

	_, stranger := s.ts.signUp(t, "stranger@example.com")
	strangers := s.ts.createRestaurant(t, stranger, "strangers")
	s.ts.signUp(t, "outsider@example.com")
	res := s.ts.do(t, http.MethodPost, fmt.Sprintf("/v1/restaurants/%d/drivers", strangers.ID), stranger,
		map[string]any{"email": "outsider@example.com", "phone": "81988887777"})
	assertStatus(t, res, http.StatusCreated)
	outsider := decode[struct {
		Driver driverResponse `json:"driver"`
	}](t, res.body).Driver

	confirmed, _ := s.orderIn(t, "confirmed")
	pending, _ := s.orderIn(t)

	pickup := s.deliveryOrder()
	pickup["fulfillment"] = "pickup"
	delete(pickup, "address")
	s.placeOrder(t, pickup)
	pickupID := s.newestOrderID(t)
	assertStatus(t, s.setStatus(t, s.owner, pickupID, map[string]any{"status": "confirmed"}), http.StatusOK)

	tests := []struct {
		name   string
		token  string
		path   string
		driver int64
		want   int
	}{
		{"pending orders wait for confirmation", s.owner, s.path(fmt.Sprintf("/orders/%d/driver", pending)), joao.ID, http.StatusConflict},
		{"pickup orders have no driver", s.owner, s.path(fmt.Sprintf("/orders/%d/driver", pickupID)), joao.ID, http.StatusConflict},
		{"another restaurant's driver", s.owner, s.path(fmt.Sprintf("/orders/%d/driver", confirmed)), outsider.ID, http.StatusUnprocessableEntity},
		{"missing driver", s.owner, s.path(fmt.Sprintf("/orders/%d/driver", confirmed)), 0, http.StatusUnprocessableEntity},
		{"drivers can't assign", driverToken, s.path(fmt.Sprintf("/orders/%d/driver", confirmed)), joao.ID, http.StatusForbidden},
		{"another restaurant can't assign", stranger, s.path(fmt.Sprintf("/orders/%d/driver", confirmed)), joao.ID, http.StatusNotFound},
		{"another restaurant, through its own", stranger, fmt.Sprintf("/v1/restaurants/%d/orders/%d/driver", strangers.ID, confirmed), outsider.ID, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := s.ts.do(t, http.MethodPut, tt.path, tt.token, map[string]any{"driver_id": tt.driver})
			assertStatus(t, res, tt.want)
		})
	}

	t.Run("busy drivers can't be removed", func(t *testing.T) {
		assertStatus(t, s.assign(t, s.owner, confirmed, joao.ID), http.StatusOK)
		assertStatus(t, s.ts.do(t, http.MethodDelete, s.path(fmt.Sprintf("/drivers/%d", joao.ID)), s.owner, nil), http.StatusConflict)
		assertStatus(t, s.ts.do(t, http.MethodDelete, s.path(fmt.Sprintf("/members/%d", joao.UserID)), s.owner, nil), http.StatusConflict)

		assertStatus(t, s.setStatus(t, s.owner, confirmed, map[string]any{"status": "cancelled", "reason": "test"}), http.StatusOK)
		assertStatus(t, s.ts.do(t, http.MethodDelete, s.path(fmt.Sprintf("/drivers/%d", joao.ID)), s.owner, nil), http.StatusNoContent)

		// The order keeps its (cancelled) delivery, without the driver.
		if o := s.staffOrder(t, confirmed); o.Delivery == nil || o.Delivery.Status != data.DeliveryCancelled || o.Delivery.Driver != nil {
			t.Errorf("delivery = %+v", o.Delivery)
		}
	})
}
