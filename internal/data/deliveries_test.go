package data

import (
	"context"
	"errors"
	"testing"

	"menugo.flayshon.com/internal/testdb"
	"menugo.flayshon.com/internal/validator"
)

func TestValidateDriver(t *testing.T) {
	tests := []struct {
		d     Driver
		field string
	}{
		{Driver{Name: "João", Phone: "+5581988887777", Vehicle: "Honda CG 160"}, ""},
		{Driver{Name: "", Phone: "+5581988887777"}, "name"},
		{Driver{Name: "João", Phone: ""}, "phone"},
		{Driver{Name: "João", Phone: "12"}, "phone"},
	}
	for _, tt := range tests {
		v := validator.New()
		ValidateDriver(v, &tt.d)
		checkField(t, v, tt.field)
	}
}

func TestDriverCanSet(t *testing.T) {
	for s, want := range map[DeliveryStatus]bool{
		DeliveryPickedUp: true, DeliveryDelivered: true,
		DeliveryAssigned: false, DeliveryUnassigned: false, DeliveryCancelled: false,
	} {
		if DriverCanSet(s) != want {
			t.Errorf("DriverCanSet(%s) = %t; want %t", s, !want, want)
		}
	}
}

// newDriver makes a new user a driver of restaurantID.
func newDriver(t *testing.T, m Models, restaurantID int64, email string) *Driver {
	t.Helper()
	u := insertUser(t, m, email)
	d := &Driver{RestaurantID: restaurantID, UserID: u.ID, Name: "Driver " + email, Phone: "+5581988887777", IsActive: true}
	if err := m.Drivers.Create(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCreateDriver(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	r := newRestaurant(t, m, "drivers")
	d := newDriver(t, m, r.ID, "joao@example.com")

	ms, err := m.Memberships.Get(ctx, r.ID, d.UserID)
	if err != nil || ms.Role != RoleDriver {
		t.Fatalf("membership = %+v, %v; want the driver role", ms, err)
	}

	got, err := m.Drivers.Get(ctx, r.ID, d.ID)
	if err != nil || got.Email != "joao@example.com" || !got.IsActive {
		t.Errorf("driver = %+v, %v", got, err)
	}

	again := &Driver{RestaurantID: r.ID, UserID: d.UserID, Name: "x", Phone: "+5581988887777"}
	if err := m.Drivers.Create(ctx, again); !errors.Is(err, ErrDuplicateDriver) {
		t.Errorf("duplicate: err = %v", err)
	}

	staff := insertUser(t, m, "staff@example.com")
	if err := m.Memberships.Insert(ctx, &Membership{RestaurantID: r.ID, UserID: staff.ID, Role: RoleStaff}); err != nil {
		t.Fatal(err)
	}
	var conflict *MemberRoleConflictError
	err = m.Drivers.Create(ctx, &Driver{RestaurantID: r.ID, UserID: staff.ID, Name: "x", Phone: "+5581988887777"})
	if !errors.As(err, &conflict) || conflict.Role != RoleStaff {
		t.Errorf("staff member: err = %v", err)
	}

	// A member who already has the driver role just gets a profile.
	member := insertUser(t, m, "member@example.com")
	if err := m.Memberships.Insert(ctx, &Membership{RestaurantID: r.ID, UserID: member.ID, Role: RoleDriver}); err != nil {
		t.Fatal(err)
	}
	if err := m.Drivers.Create(ctx, &Driver{RestaurantID: r.ID, UserID: member.ID, Name: "x", Phone: "+5581988887777"}); err != nil {
		t.Errorf("existing driver member: %v", err)
	}
}

// confirmedOrder places a delivery order and confirms it.
func confirmedOrder(t *testing.T, f *orderFixture) *Order {
	t.Helper()
	ctx := context.Background()
	o, _, err := f.m.Orders.Create(ctx, f.restaurant, f.request())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.m.Orders.Transition(ctx, f.restaurant.ID, o.ID, StatusConfirmed, Actor{Type: ActorUser}, ""); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestAssignDriver(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()
	rid := f.restaurant.ID

	joao := newDriver(t, f.m, rid, "joao@example.com")
	maria := newDriver(t, f.m, rid, "maria@example.com")

	t.Run("not before confirmation, and never for pickup", func(t *testing.T) {
		pending, _, err := f.m.Orders.Create(ctx, f.restaurant, f.request())
		if err != nil {
			t.Fatal(err)
		}
		var notAssignable *NotAssignableError
		if _, err := f.m.Deliveries.Assign(ctx, rid, pending.ID, joao.ID, 0); !errors.As(err, &notAssignable) {
			t.Errorf("pending: err = %v", err)
		}

		req := f.request()
		req.Fulfillment, req.Address = FulfillmentPickup, Address{}
		pickup, _, err := f.m.Orders.Create(ctx, f.restaurant, req)
		if err != nil {
			t.Fatal(err)
		}
		f.m.Orders.Transition(ctx, rid, pickup.ID, StatusConfirmed, Actor{Type: ActorUser}, "")
		if _, err := f.m.Deliveries.Assign(ctx, rid, pickup.ID, joao.ID, 0); !errors.As(err, &notAssignable) {
			t.Errorf("pickup: err = %v", err)
		}
	})

	order := confirmedOrder(t, f)

	t.Run("only active drivers of the restaurant", func(t *testing.T) {
		other := newRestaurant(t, f.m, "other")
		outsider := newDriver(t, f.m, other.ID, "outsider@example.com")
		if _, err := f.m.Deliveries.Assign(ctx, rid, order.ID, outsider.ID, 0); !errors.Is(err, ErrDriverUnavailable) {
			t.Errorf("other restaurant's driver: err = %v", err)
		}

		inactive := newDriver(t, f.m, rid, "inactive@example.com")
		inactive.IsActive = false
		if err := f.m.Drivers.Update(ctx, inactive); err != nil {
			t.Fatal(err)
		}
		if _, err := f.m.Deliveries.Assign(ctx, rid, order.ID, inactive.ID, 0); !errors.Is(err, ErrDriverUnavailable) {
			t.Errorf("inactive driver: err = %v", err)
		}
	})

	dl, err := f.m.Deliveries.Assign(ctx, rid, order.ID, joao.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dl.Status != DeliveryAssigned || dl.DriverID != joao.ID || dl.DriverName != joao.Name {
		t.Errorf("delivery = %+v", dl)
	}

	again, err := f.m.Deliveries.Assign(ctx, rid, order.ID, joao.ID, 0)
	if err != nil || again.ID != dl.ID {
		t.Errorf("assigning the same driver again: %+v, %v; want the same delivery", again, err)
	}

	reassigned, err := f.m.Deliveries.Assign(ctx, rid, order.ID, maria.ID, 0)
	if err != nil || reassigned.DriverID != maria.ID || reassigned.ID == dl.ID {
		t.Fatalf("reassigned = %+v, %v", reassigned, err)
	}

	var statuses []DeliveryStatus
	rows, _ := f.m.Orders.DB.Query("SELECT status FROM deliveries WHERE order_id = ? ORDER BY id", order.ID)
	for rows.Next() {
		var s DeliveryStatus
		rows.Scan(&s)
		statuses = append(statuses, s)
	}
	rows.Close()
	if len(statuses) != 2 || statuses[0] != DeliveryUnassigned || statuses[1] != DeliveryAssigned {
		t.Errorf("deliveries = %v; want [unassigned assigned]", statuses)
	}

	t.Run("the schema allows one active delivery per order", func(t *testing.T) {
		_, err := f.m.Orders.DB.Exec(
			"INSERT INTO deliveries (restaurant_id, order_id, driver_id, status, assigned_at) VALUES (?, ?, ?, 'assigned', NOW())",
			rid, order.ID, joao.ID)
		if !isDuplicateKey(err, "deliveries_one_active_per_order_uk") {
			t.Errorf("err = %v; want a duplicate key error", err)
		}
	})

	if err := f.m.Deliveries.Unassign(ctx, rid, order.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Deliveries.Unassign(ctx, rid, order.ID); !errors.Is(err, ErrNoActiveDelivery) {
		t.Errorf("second unassign: err = %v", err)
	}
	if _, err := f.m.Deliveries.Current(ctx, rid, order.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("current after unassigning: err = %v", err)
	}
}

func TestDeliveryFollowsOrder(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()
	rid := f.restaurant.ID
	staff := Actor{Type: ActorUser}

	joao := newDriver(t, f.m, rid, "joao@example.com")

	t.Run("staff move the order; the delivery follows", func(t *testing.T) {
		order := confirmedOrder(t, f)
		if _, err := f.m.Deliveries.Assign(ctx, rid, order.ID, joao.ID, 0); err != nil {
			t.Fatal(err)
		}
		for _, s := range []OrderStatus{StatusPreparing, StatusReadyForDelivery, StatusOutForDelivery} {
			if err := f.m.Orders.Transition(ctx, rid, order.ID, s, staff, ""); err != nil {
				t.Fatal(err)
			}
		}
		dl, _ := f.m.Deliveries.Current(ctx, rid, order.ID)
		if dl.Status != DeliveryPickedUp || dl.PickedUpAt.IsZero() {
			t.Errorf("after out_for_delivery: %+v", dl)
		}

		// Too late to change drivers.
		var notAssignable *NotAssignableError
		if _, err := f.m.Deliveries.Assign(ctx, rid, order.ID, joao.ID, 0); !errors.As(err, &notAssignable) {
			t.Errorf("assign while out: err = %v", err)
		}

		if err := f.m.Orders.Transition(ctx, rid, order.ID, StatusDelivered, staff, ""); err != nil {
			t.Fatal(err)
		}
		dl, _ = f.m.Deliveries.Current(ctx, rid, order.ID)
		if dl.Status != DeliveryDelivered || dl.DeliveredAt.IsZero() {
			t.Errorf("after delivered: %+v", dl)
		}
	})

	t.Run("cancelling the order cancels the delivery", func(t *testing.T) {
		order := confirmedOrder(t, f)
		if _, err := f.m.Deliveries.Assign(ctx, rid, order.ID, joao.ID, 0); err != nil {
			t.Fatal(err)
		}
		if err := f.m.Orders.Transition(ctx, rid, order.ID, StatusCancelled, staff, ""); err != nil {
			t.Fatal(err)
		}
		dl, _ := f.m.Deliveries.Current(ctx, rid, order.ID)
		if dl.Status != DeliveryCancelled || dl.EndedAt.IsZero() {
			t.Errorf("delivery = %+v", dl)
		}
	})

	t.Run("orders without a driver work as before", func(t *testing.T) {
		order := confirmedOrder(t, f)
		for _, s := range []OrderStatus{StatusPreparing, StatusReadyForDelivery, StatusOutForDelivery, StatusDelivered} {
			if err := f.m.Orders.Transition(ctx, rid, order.ID, s, staff, ""); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestDriverUpdatesDelivery(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()
	rid := f.restaurant.ID
	staff := Actor{Type: ActorUser}

	joao := newDriver(t, f.m, rid, "joao@example.com")
	maria := newDriver(t, f.m, rid, "maria@example.com")

	order := confirmedOrder(t, f)
	dl, err := f.m.Deliveries.Assign(ctx, rid, order.ID, joao.ID, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.m.Deliveries.UpdateByDriver(ctx, maria.UserID, dl.ID, DeliveryPickedUp); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("another driver: err = %v", err)
	}

	// The kitchen hasn't finished: the order can't go out yet.
	var invalid *InvalidTransitionError
	if err := f.m.Deliveries.UpdateByDriver(ctx, joao.UserID, dl.ID, DeliveryPickedUp); !errors.As(err, &invalid) {
		t.Errorf("picking up a confirmed order: err = %v", err)
	}

	f.m.Orders.Transition(ctx, rid, order.ID, StatusPreparing, staff, "")
	f.m.Orders.Transition(ctx, rid, order.ID, StatusReadyForDelivery, staff, "")

	if err := f.m.Deliveries.UpdateByDriver(ctx, joao.UserID, dl.ID, DeliveryPickedUp); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Deliveries.UpdateByDriver(ctx, joao.UserID, dl.ID, DeliveryDelivered); err != nil {
		t.Fatal(err)
	}

	got, err := f.m.Orders.Get(ctx, rid, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDelivered || got.Delivery == nil || got.Delivery.Status != DeliveryDelivered {
		t.Errorf("order = %s, delivery = %+v", got.Status, got.Delivery)
	}
	if last := got.History[len(got.History)-1]; last.ActorUserID != joao.UserID {
		t.Errorf("last change by %d; want the driver %d", last.ActorUserID, joao.UserID)
	}

	if err := f.m.Deliveries.UpdateByDriver(ctx, joao.UserID, dl.ID, DeliveryDelivered); !errors.Is(err, ErrNoActiveDelivery) {
		t.Errorf("finished delivery: err = %v", err)
	}

	t.Run("a reassigned driver can't touch the order", func(t *testing.T) {
		order := confirmedOrder(t, f)
		old, _ := f.m.Deliveries.Assign(ctx, rid, order.ID, joao.ID, 0)
		f.m.Deliveries.Assign(ctx, rid, order.ID, maria.ID, 0)
		f.m.Orders.Transition(ctx, rid, order.ID, StatusPreparing, staff, "")
		f.m.Orders.Transition(ctx, rid, order.ID, StatusReadyForDelivery, staff, "")

		if err := f.m.Deliveries.UpdateByDriver(ctx, joao.UserID, old.ID, DeliveryPickedUp); !errors.Is(err, ErrNoActiveDelivery) {
			t.Errorf("err = %v; want ErrNoActiveDelivery", err)
		}
	})
}

func TestBusyDriversCannotBeRemoved(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()
	rid := f.restaurant.ID

	joao := newDriver(t, f.m, rid, "joao@example.com")
	order := confirmedOrder(t, f)
	dl, err := f.m.Deliveries.Assign(ctx, rid, order.ID, joao.ID, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.m.Drivers.Delete(ctx, rid, joao.ID); !errors.Is(err, ErrDriverBusy) {
		t.Errorf("delete driver: err = %v", err)
	}
	if err := f.m.Memberships.Delete(ctx, rid, joao.UserID, RoleDriver); !errors.Is(err, ErrDriverBusy) {
		t.Errorf("remove member: err = %v", err)
	}

	if err := f.m.Orders.Transition(ctx, rid, order.ID, StatusCancelled, Actor{Type: ActorUser}, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Drivers.Delete(ctx, rid, joao.ID); err != nil {
		t.Fatalf("delete idle driver: %v", err)
	}
	if _, err := f.m.Memberships.Get(ctx, rid, joao.UserID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("membership should be gone: %v", err)
	}

	// The finished delivery stays on record, without its driver.
	got, err := f.m.Deliveries.Current(ctx, rid, order.ID)
	if err != nil || got.ID != dl.ID || got.DriverID != 0 || got.Status != DeliveryCancelled {
		t.Errorf("delivery = %+v, %v", got, err)
	}
}

func TestListDeliveriesForDriver(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()

	joao := newDriver(t, f.m, f.restaurant.ID, "joao@example.com")

	// João also drives for a second restaurant.
	other := newRestaurant(t, f.m, "second")
	otherCategory := newCategory(t, f.m, other.ID, "Menu", 0)
	otherItem := newMenuItem(t, f.m, other.ID, otherCategory.ID, "Burger", 0)
	otherItem.PriceCents = 9000
	f.m.MenuItems.Update(ctx, otherItem)
	newZone(t, f.m, other.ID, "All", 500, 0, "50")
	otherDriver := &Driver{RestaurantID: other.ID, UserID: joao.UserID, Name: "João", Phone: "+5581988887777", IsActive: true}
	if err := f.m.Drivers.Create(ctx, otherDriver); err != nil {
		t.Fatal(err)
	}

	first := confirmedOrder(t, f)
	if _, err := f.m.Deliveries.Assign(ctx, f.restaurant.ID, first.ID, joao.ID, 0); err != nil {
		t.Fatal(err)
	}

	req := f.request()
	req.Lines = []OrderLine{{MenuItemID: otherItem.ID, Quantity: 1}}
	otherOrder, _, err := f.m.Orders.Create(ctx, other, req)
	if err != nil {
		t.Fatal(err)
	}
	f.m.Orders.Transition(ctx, other.ID, otherOrder.ID, StatusConfirmed, Actor{Type: ActorUser}, "")
	if _, err := f.m.Deliveries.Assign(ctx, other.ID, otherOrder.ID, otherDriver.ID, 0); err != nil {
		t.Fatal(err)
	}

	list, meta, err := f.m.Deliveries.ListForDriver(ctx, joao.UserID, []DeliveryStatus{DeliveryAssigned}, Pagination{1, 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || meta.TotalRecords != 2 {
		t.Fatalf("deliveries = %d (%+v)", len(list), meta)
	}
	if list[0].Restaurant.Slug != "second" || list[0].Order.ID != otherOrder.ID || len(list[0].Order.Items) != 1 {
		t.Errorf("newest = %+v", list[0])
	}
	if list[1].Order.Address.Line != "Rua A, 1" || list[1].Order.CustomerPhone == "" {
		t.Errorf("drivers need the address and phone: %+v", list[1].Order)
	}

	stranger := insertUser(t, f.m, "stranger@example.com")
	list, _, err = f.m.Deliveries.ListForDriver(ctx, stranger.ID, nil, Pagination{1, 20})
	if err != nil || len(list) != 0 {
		t.Errorf("stranger's deliveries = %v, %v", list, err)
	}
	if _, err := f.m.Deliveries.GetForDriver(ctx, stranger.ID, anyDeliveryID(t, f, joao.UserID)); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("stranger reading João's delivery: err = %v", err)
	}
}

// anyDeliveryID returns the ID of one of userID's deliveries.
func anyDeliveryID(t *testing.T, f *orderFixture, userID int64) int64 {
	t.Helper()
	list, _, err := f.m.Deliveries.ListForDriver(context.Background(), userID, nil, Pagination{1, 1})
	if err != nil || len(list) == 0 {
		t.Fatalf("no deliveries: %v", err)
	}
	return list[0].ID
}
