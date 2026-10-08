package data

import (
	"context"
	"errors"
	"strings"
	"testing"

	"menugo.flayshon.com/internal/testdb"
	"menugo.flayshon.com/internal/validator"
)

func TestCanTransition(t *testing.T) {
	// Every step of both happy paths is allowed for restaurant users.
	paths := map[Fulfillment][]OrderStatus{
		FulfillmentDelivery: {StatusPending, StatusConfirmed, StatusPreparing, StatusReadyForDelivery, StatusOutForDelivery, StatusDelivered},
		FulfillmentPickup:   {StatusPending, StatusConfirmed, StatusPreparing, StatusReadyForPickup, StatusPickedUp},
	}
	for f, path := range paths {
		for i := 0; i < len(path)-1; i++ {
			if !CanTransition(f, path[i], path[i+1], ActorUser) {
				t.Errorf("%s: %s -> %s should be allowed", f, path[i], path[i+1])
			}
		}
	}

	tests := []struct {
		f        Fulfillment
		from, to OrderStatus
		actor    ActorType
		want     bool
	}{
		// Skipping steps and going backwards are forbidden.
		{FulfillmentDelivery, StatusPending, StatusPreparing, ActorUser, false},
		{FulfillmentDelivery, StatusPreparing, StatusConfirmed, ActorUser, false},
		{FulfillmentDelivery, StatusPending, StatusDelivered, ActorUser, false},
		// The two kinds of order don't mix.
		{FulfillmentDelivery, StatusPreparing, StatusReadyForPickup, ActorUser, false},
		{FulfillmentPickup, StatusPreparing, StatusReadyForDelivery, ActorUser, false},
		{FulfillmentPickup, StatusReadyForPickup, StatusDelivered, ActorUser, false},
		// The restaurant can cancel until the order leaves.
		{FulfillmentDelivery, StatusReadyForDelivery, StatusCancelled, ActorUser, true},
		{FulfillmentPickup, StatusReadyForPickup, StatusCancelled, ActorUser, true},
		{FulfillmentDelivery, StatusOutForDelivery, StatusCancelled, ActorUser, false},
		// Final statuses are final.
		{FulfillmentDelivery, StatusDelivered, StatusCancelled, ActorUser, false},
		{FulfillmentPickup, StatusPickedUp, StatusCancelled, ActorUser, false},
		{FulfillmentDelivery, StatusCancelled, StatusPending, ActorUser, false},
		{FulfillmentDelivery, StatusCancelled, StatusCancelled, ActorUser, false},
		// Customers can only cancel orders that are still pending.
		{FulfillmentDelivery, StatusPending, StatusCancelled, ActorCustomer, true},
		{FulfillmentPickup, StatusPending, StatusCancelled, ActorCustomer, true},
		{FulfillmentDelivery, StatusConfirmed, StatusCancelled, ActorCustomer, false},
		{FulfillmentDelivery, StatusPending, StatusConfirmed, ActorCustomer, false},
		// Unknown values are never allowed.
		{Fulfillment("drone"), StatusPending, StatusConfirmed, ActorUser, false},
		{FulfillmentDelivery, OrderStatus("lost"), StatusCancelled, ActorUser, false},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.f, tt.from, tt.to, tt.actor); got != tt.want {
			t.Errorf("CanTransition(%s, %s -> %s, %s) = %t; want %t", tt.f, tt.from, tt.to, tt.actor, got, tt.want)
		}
	}
}

func TestNextStatuses(t *testing.T) {
	got := NextStatuses(FulfillmentPickup, StatusPreparing)
	if len(got) != 2 || got[0] != StatusReadyForPickup || got[1] != StatusCancelled {
		t.Errorf("got %v", got)
	}
	got[0] = StatusDelivered // must not modify the rules
	if NextStatuses(FulfillmentPickup, StatusPreparing)[0] != StatusReadyForPickup {
		t.Error("NextStatuses returned the internal slice")
	}
	if len(NextStatuses(FulfillmentDelivery, StatusDelivered)) != 0 {
		t.Error("delivered should have no next statuses")
	}
}

func TestPriceLines(t *testing.T) {
	menu := map[int64]orderableItem{
		1: {ID: 1, Name: "Pizza", PriceCents: 4500, IsAvailable: true, CategoryVisible: true},
		2: {ID: 2, Name: "Soda", PriceCents: 600, IsAvailable: true, CategoryVisible: true},
		3: {ID: 3, Name: "Sold out", PriceCents: 100, IsAvailable: false, CategoryVisible: true},
		4: {ID: 4, Name: "Hidden", PriceCents: 100, IsAvailable: true, CategoryVisible: false},
	}

	items, subtotal, err := priceLines([]OrderLine{
		{MenuItemID: 1, Quantity: 2, Notes: "no onions"},
		{MenuItemID: 2, Quantity: 3},
		{MenuItemID: 1, Quantity: 1},
	}, menu)
	if err != nil {
		t.Fatal(err)
	}
	if subtotal != 2*4500+3*600+4500 {
		t.Errorf("subtotal = %d", subtotal)
	}
	if len(items) != 3 || items[0].LineTotalCents != 9000 || items[0].Name != "Pizza" || items[0].Notes != "no onions" {
		t.Errorf("items = %+v", items)
	}

	for _, id := range []int64{3, 4, 99} {
		_, _, err := priceLines([]OrderLine{{MenuItemID: 1, Quantity: 1}, {MenuItemID: id, Quantity: 1}}, menu)
		var unavailable *ItemUnavailableError
		if !errors.As(err, &unavailable) || unavailable.MenuItemID != id {
			t.Errorf("item %d: err = %v; want ItemUnavailableError", id, err)
		}
	}
}

func TestNormalizePhoneAndPostalCode(t *testing.T) {
	phones := map[string]string{
		"+55 (81) 99999-0000": "+5581999990000",
		"81 9999.0000":        "8199990000",
		"55+81":               "5581",
		" +1 555 0100 ":       "+15550100",
	}
	for in, want := range phones {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q; want %q", in, got, want)
		}
	}

	codes := map[string]string{
		"50030-230": "50030230",
		"sw1a 1aa":  "SW1A1AA",
		"５００":       "", // full-width digits are not accepted
		"50.030":    "50030",
	}
	for in, want := range codes {
		if got := NormalizePostalCode(in); got != want {
			t.Errorf("NormalizePostalCode(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestValidateOrderRequest(t *testing.T) {
	delivery := func() OrderRequest {
		return OrderRequest{
			Fulfillment:   FulfillmentDelivery,
			CustomerName:  "Ana",
			CustomerPhone: "+5581999990000",
			Address:       Address{Line: "Rua A, 1", City: "Recife", PostalCode: "50030230"},
			Lines:         []OrderLine{{MenuItemID: 1, Quantity: 1}},
		}
	}

	tests := []struct {
		name   string
		modify func(*OrderRequest)
		field  string
	}{
		{"valid delivery", func(*OrderRequest) {}, ""},
		{"valid pickup", func(r *OrderRequest) { r.Fulfillment = FulfillmentPickup; r.Address = Address{} }, ""},
		{"unknown fulfillment", func(r *OrderRequest) { r.Fulfillment = "drone" }, "fulfillment"},
		{"missing name", func(r *OrderRequest) { r.CustomerName = "" }, "customer.name"},
		{"short phone", func(r *OrderRequest) { r.CustomerPhone = "1234" }, "customer.phone"},
		{"bad email", func(r *OrderRequest) { r.CustomerEmail = "nope" }, "customer.email"},
		{"delivery without address", func(r *OrderRequest) { r.Address.Line = "" }, "address.line"},
		{"delivery without city", func(r *OrderRequest) { r.Address.City = "" }, "address.city"},
		{"delivery without postal code", func(r *OrderRequest) { r.Address.PostalCode = "" }, "address.postal_code"},
		{"pickup with address", func(r *OrderRequest) { r.Fulfillment = FulfillmentPickup }, "address"},
		{"no items", func(r *OrderRequest) { r.Lines = nil }, "items"},
		{"too many lines", func(r *OrderRequest) {
			r.Lines = make([]OrderLine, maxOrderLines+1)
			for i := range r.Lines {
				r.Lines[i] = OrderLine{MenuItemID: 1, Quantity: 1}
			}
		}, "items"},
		{"zero quantity", func(r *OrderRequest) { r.Lines[0].Quantity = 0 }, "items[0].quantity"},
		{"huge quantity", func(r *OrderRequest) { r.Lines[0].Quantity = 100 }, "items[0].quantity"},
		{"missing item", func(r *OrderRequest) { r.Lines[0].MenuItemID = 0 }, "items[0].item_id"},
		{"long notes", func(r *OrderRequest) { r.Notes = strings.Repeat("x", 501) }, "notes"},
		{"negative expected total", func(r *OrderRequest) { n := int64(-1); r.ExpectedTotalCents = &n }, "expected_total_cents"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := delivery()
			tt.modify(&req)
			v := validator.New()
			ValidateOrderRequest(v, &req)
			checkField(t, v, tt.field)
		})
	}
}

func TestValidateDeliveryZone(t *testing.T) {
	valid := func() DeliveryZone {
		z := DeliveryZone{Name: "Centro", FeeCents: 500}
		z.SetPostalCodes([]string{"50030-230", "5003"})
		return z
	}

	z := valid()
	if strings.Join(z.PostalCodes, ",") != "5003,50030230" {
		t.Errorf("postal codes = %v", z.PostalCodes)
	}

	tests := []struct {
		name   string
		modify func(*DeliveryZone)
		field  string
	}{
		{"valid", func(*DeliveryZone) {}, ""},
		{"free delivery", func(z *DeliveryZone) { z.FeeCents = 0 }, ""},
		{"no name", func(z *DeliveryZone) { z.Name = "" }, "name"},
		{"negative fee", func(z *DeliveryZone) { z.FeeCents = -1 }, "fee_cents"},
		{"negative minimum", func(z *DeliveryZone) { z.MinOrderCents = -1 }, "min_order_cents"},
		{"no postal codes", func(z *DeliveryZone) { z.SetPostalCodes(nil) }, "postal_codes"},
		{"too short", func(z *DeliveryZone) { z.SetPostalCodes([]string{"5"}) }, "postal_codes"},
		{"only punctuation", func(z *DeliveryZone) { z.SetPostalCodes([]string{"--"}) }, "postal_codes"},
		{"too long", func(z *DeliveryZone) { z.SetPostalCodes([]string{"12345678901"}) }, "postal_codes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			z := valid()
			tt.modify(&z)
			v := validator.New()
			ValidateDeliveryZone(v, &z)
			checkField(t, v, tt.field)
		})
	}
}

func newZone(t *testing.T, m Models, restaurantID int64, name string, fee, min int64, codes ...string) *DeliveryZone {
	t.Helper()
	z := &DeliveryZone{RestaurantID: restaurantID, Name: name, FeeCents: fee, MinOrderCents: min, IsActive: true}
	z.SetPostalCodes(codes)
	if err := m.Zones.Insert(context.Background(), z); err != nil {
		t.Fatal(err)
	}
	return z
}

func TestDeliveryZones(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	r := newRestaurant(t, m, "pizza")
	other := newRestaurant(t, m, "other")

	city := newZone(t, m, r.ID, "City", 1000, 0, "50")
	centre := newZone(t, m, r.ID, "Centre", 500, 3000, "50030", "50040-000")
	newZone(t, m, other.ID, "Theirs", 1, 0, "50030") // same entry, other restaurant: fine

	t.Run("longest matching entry wins", func(t *testing.T) {
		tests := map[string]int64{
			"50030230": centre.ID, // matches "50" and "50030"
			"50040000": centre.ID, // exact entry
			"50040001": city.ID,   // only "50"
			"51000000": 0,
		}
		for code, want := range tests {
			z, err := m.Zones.FindForPostalCode(ctx, r.ID, code)
			if want == 0 {
				if !errors.Is(err, ErrNotDeliverable) {
					t.Errorf("%s: err = %v; want ErrNotDeliverable", code, err)
				}
				continue
			}
			if err != nil || z.ID != want {
				t.Errorf("%s: zone = %v, %v; want %d", code, z, err, want)
			}
		}
	})

	t.Run("postal code entries are unique per restaurant", func(t *testing.T) {
		z := &DeliveryZone{RestaurantID: r.ID, Name: "Overlap", FeeCents: 1}
		z.SetPostalCodes([]string{"60", "50030"})
		var inUse *PostalCodeInUseError
		if err := m.Zones.Insert(ctx, z); !errors.As(err, &inUse) || inUse.PostalCode != "50030" {
			t.Errorf("err = %v; want PostalCodeInUseError for 50030", err)
		}
		// The failed insert must not leave a zone behind.
		if _, err := m.Zones.FindForPostalCode(ctx, r.ID, "60000000"); !errors.Is(err, ErrNotDeliverable) {
			t.Errorf("partial zone was saved: %v", err)
		}
	})

	t.Run("update replaces postal codes", func(t *testing.T) {
		z, err := m.Zones.Get(ctx, r.ID, centre.ID)
		if err != nil {
			t.Fatal(err)
		}
		z.SetPostalCodes([]string{"50030", "50050"})
		if err := m.Zones.Update(ctx, z); err != nil {
			t.Fatal(err)
		}
		got, _ := m.Zones.Get(ctx, r.ID, centre.ID)
		if strings.Join(got.PostalCodes, ",") != "50030,50050" || got.Version != 2 {
			t.Errorf("zone = %+v", got)
		}
		if z, _ := m.Zones.FindForPostalCode(ctx, r.ID, "50040000"); z == nil || z.ID != city.ID {
			t.Errorf("removed entry still matches: %+v", z)
		}
	})

	t.Run("inactive zone stops delivery for its codes", func(t *testing.T) {
		z, _ := m.Zones.Get(ctx, r.ID, centre.ID)
		z.IsActive = false
		if err := m.Zones.Update(ctx, z); err != nil {
			t.Fatal(err)
		}
		// "50" (City) also matches, but the longer, inactive entry decides.
		if _, err := m.Zones.FindForPostalCode(ctx, r.ID, "50030230"); !errors.Is(err, ErrNotDeliverable) {
			t.Errorf("err = %v; want ErrNotDeliverable", err)
		}
	})

	t.Run("list and tenant scoping", func(t *testing.T) {
		zones, err := m.Zones.List(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(zones) != 2 || zones[0].Name != "Centre" || len(zones[1].PostalCodes) != 1 {
			t.Errorf("zones = %+v", zones)
		}
		if _, err := m.Zones.Get(ctx, other.ID, centre.ID); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("err = %v; want ErrRecordNotFound", err)
		}
		if err := m.Zones.Delete(ctx, other.ID, centre.ID); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("err = %v; want ErrRecordNotFound", err)
		}
	})
}

// orderFixture is a published restaurant with a small menu and one zone.
type orderFixture struct {
	m          Models
	restaurant *Restaurant
	pizza      *MenuItem // 4500
	soda       *MenuItem // 600
	zone       *DeliveryZone
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()
	m := NewModels(testdb.New(t))
	r := newRestaurant(t, m, "pizza")
	c := newCategory(t, m, r.ID, "Menu", 0)

	f := &orderFixture{m: m, restaurant: r}
	f.pizza = newMenuItem(t, m, r.ID, c.ID, "Pizza", 0)
	f.pizza.PriceCents = 4500
	f.soda = newMenuItem(t, m, r.ID, c.ID, "Soda", 1)
	f.soda.PriceCents = 600
	for _, item := range []*MenuItem{f.pizza, f.soda} {
		if err := m.MenuItems.Update(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	f.zone = newZone(t, m, r.ID, "Centre", 800, 5000, "50030")
	return f
}

func (f *orderFixture) request() *OrderRequest {
	return &OrderRequest{
		Fulfillment:   FulfillmentDelivery,
		CustomerName:  "Ana",
		CustomerPhone: "+5581999990000",
		Address:       Address{Line: "Rua A, 1", City: "Recife", PostalCode: "50030230"},
		Lines:         []OrderLine{{MenuItemID: f.pizza.ID, Quantity: 1}, {MenuItemID: f.soda.ID, Quantity: 2, Notes: "cold"}},
	}
}

func TestCreateOrder(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()

	order, token, err := f.m.Orders.Create(ctx, f.restaurant, f.request())
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 26 {
		t.Errorf("token = %q", token)
	}
	if order.SubtotalCents != 5700 || order.DeliveryFeeCents != 800 || order.TotalCents != 6500 {
		t.Errorf("amounts = %d + %d = %d", order.SubtotalCents, order.DeliveryFeeCents, order.TotalCents)
	}
	if order.Status != StatusPending || order.Currency != "BRL" || order.DeliveryZoneID != f.zone.ID {
		t.Errorf("order = %+v", order)
	}

	got, err := f.m.Orders.GetByTrackingToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != order.ID || len(got.Items) != 2 || got.Items[1].Notes != "cold" || got.Items[1].LineTotalCents != 1200 {
		t.Errorf("stored order = %+v", got)
	}
	if len(got.History) != 1 || got.History[0].To != StatusPending || got.History[0].ActorType != ActorCustomer {
		t.Errorf("history = %+v", got.History)
	}
	if !got.CreatedAt.Equal(order.CreatedAt) {
		t.Errorf("created_at = %v; want %v", got.CreatedAt, order.CreatedAt)
	}

	if _, err := f.m.Orders.GetByTrackingToken(ctx, strings.Repeat("A", 26)); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("unknown token: err = %v", err)
	}

	t.Run("menu changes don't change placed orders", func(t *testing.T) {
		f.pizza.PriceCents = 9999
		f.pizza.Name = "Renamed"
		if err := f.m.MenuItems.Update(ctx, f.pizza); err != nil {
			t.Fatal(err)
		}
		got, _ := f.m.Orders.Get(ctx, f.restaurant.ID, order.ID)
		if got.Items[0].Name != "Pizza" || got.Items[0].UnitPriceCents != 4500 || got.TotalCents != 6500 {
			t.Errorf("order changed: %+v", got.Items[0])
		}
	})

	t.Run("pickup has no fee or zone", func(t *testing.T) {
		req := f.request()
		req.Fulfillment = FulfillmentPickup
		req.Address = Address{}
		order, _, err := f.m.Orders.Create(ctx, f.restaurant, req)
		if err != nil {
			t.Fatal(err)
		}
		if order.DeliveryFeeCents != 0 || order.DeliveryZoneID != 0 || order.TotalCents != order.SubtotalCents {
			t.Errorf("order = %+v", order)
		}
	})

	t.Run("same phone, same customer, details untouched", func(t *testing.T) {
		req := f.request()
		req.CustomerName = "Someone else"
		second, _, err := f.m.Orders.Create(ctx, f.restaurant, req)
		if err != nil {
			t.Fatal(err)
		}
		if second.CustomerID != order.CustomerID {
			t.Errorf("customer IDs %d and %d; want the same", second.CustomerID, order.CustomerID)
		}
		var name string
		f.m.Orders.DB.QueryRow("SELECT name FROM customers WHERE id = ?", order.CustomerID).Scan(&name)
		if name != "Ana" {
			t.Errorf("customer name = %q; want it unchanged", name)
		}
		if second.CustomerName != "Someone else" {
			t.Errorf("the order should keep the name it was placed with")
		}
	})
}

func TestCreateOrderRejections(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()

	other := newRestaurant(t, f.m, "other")
	otherCategory := newCategory(t, f.m, other.ID, "Theirs", 0)
	otherItem := newMenuItem(t, f.m, other.ID, otherCategory.ID, "Theirs", 0)

	hidden := newCategory(t, f.m, f.restaurant.ID, "Hidden", 1)
	hidden.IsVisible = false
	if err := f.m.Categories.Update(ctx, hidden); err != nil {
		t.Fatal(err)
	}
	hiddenItem := newMenuItem(t, f.m, f.restaurant.ID, hidden.ID, "Secret", 0)

	soldOut := newMenuItem(t, f.m, f.restaurant.ID, f.pizza.CategoryID, "Sold out", 2)
	if err := f.m.MenuItems.SetAvailability(ctx, f.restaurant.ID, soldOut.ID, false); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]int64{"other restaurant's item": otherItem.ID, "hidden category": hiddenItem.ID, "sold out": soldOut.ID, "missing": 999999} {
		t.Run(name, func(t *testing.T) {
			req := f.request()
			req.Lines = append(req.Lines, OrderLine{MenuItemID: id, Quantity: 1})
			var unavailable *ItemUnavailableError
			if _, _, err := f.m.Orders.Create(ctx, f.restaurant, req); !errors.As(err, &unavailable) || unavailable.MenuItemID != id {
				t.Errorf("err = %v; want ItemUnavailableError for %d", err, id)
			}
		})
	}

	t.Run("outside delivery zones", func(t *testing.T) {
		req := f.request()
		req.Address.PostalCode = "99999999"
		if _, _, err := f.m.Orders.Create(ctx, f.restaurant, req); !errors.Is(err, ErrNotDeliverable) {
			t.Errorf("err = %v; want ErrNotDeliverable", err)
		}
	})

	t.Run("below the zone's minimum", func(t *testing.T) {
		req := f.request()
		req.Lines = []OrderLine{{MenuItemID: f.soda.ID, Quantity: 1}}
		var below *BelowMinimumOrderError
		if _, _, err := f.m.Orders.Create(ctx, f.restaurant, req); !errors.As(err, &below) || below.MinOrderCents != 5000 {
			t.Errorf("err = %v; want BelowMinimumOrderError", err)
		}
	})

	t.Run("total changed", func(t *testing.T) {
		req := f.request()
		stale := int64(6000)
		req.ExpectedTotalCents = &stale
		var mismatch *TotalMismatchError
		if _, _, err := f.m.Orders.Create(ctx, f.restaurant, req); !errors.As(err, &mismatch) || mismatch.TotalCents != 6500 {
			t.Errorf("err = %v; want TotalMismatchError with 6500", err)
		}
	})

	var orders, customers int
	f.m.Orders.DB.QueryRow("SELECT (SELECT COUNT(*) FROM orders), (SELECT COUNT(*) FROM customers)").Scan(&orders, &customers)
	if orders != 0 || customers != 0 {
		t.Errorf("rejected orders left %d orders and %d customers behind", orders, customers)
	}
}

func TestOrderTransition(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()

	owner := insertUser(t, f.m, "staff@example.com")
	order, _, err := f.m.Orders.Create(ctx, f.restaurant, f.request())
	if err != nil {
		t.Fatal(err)
	}

	staff := Actor{Type: ActorUser, UserID: owner.ID}

	if err := f.m.Orders.Transition(ctx, f.restaurant.ID, order.ID, StatusConfirmed, staff, ""); err != nil {
		t.Fatal(err)
	}

	var invalid *InvalidTransitionError
	err = f.m.Orders.Transition(ctx, f.restaurant.ID, order.ID, StatusCancelled, Actor{Type: ActorCustomer}, "")
	if !errors.As(err, &invalid) || invalid.From != StatusConfirmed {
		t.Errorf("customer cancel after confirmation: err = %v", err)
	}

	err = f.m.Orders.Transition(ctx, f.restaurant.ID, order.ID, StatusDelivered, staff, "")
	if !errors.As(err, &invalid) {
		t.Errorf("skipping steps: err = %v", err)
	}

	other := newRestaurant(t, f.m, "other")
	if err := f.m.Orders.Transition(ctx, other.ID, order.ID, StatusPreparing, staff, ""); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("other restaurant: err = %v; want ErrRecordNotFound", err)
	}

	got, err := f.m.Orders.Get(ctx, f.restaurant.ID, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusConfirmed || got.Version != 2 {
		t.Errorf("order = %s v%d", got.Status, got.Version)
	}
	if len(got.History) != 2 {
		t.Fatalf("history = %+v", got.History)
	}
	h := got.History[1]
	if h.From != StatusPending || h.To != StatusConfirmed || h.ActorType != ActorUser || h.ActorUserID != owner.ID {
		t.Errorf("history entry = %+v", h)
	}
}
