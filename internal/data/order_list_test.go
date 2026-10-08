package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"menugo.flayshon.com/internal/validator"
)

func TestCalculateMetadata(t *testing.T) {
	tests := []struct {
		total    int
		p        Pagination
		lastPage int
	}{
		{0, Pagination{1, 20}, 0},
		{1, Pagination{1, 20}, 1},
		{20, Pagination{1, 20}, 1},
		{21, Pagination{2, 20}, 2},
		{5, Pagination{3, 2}, 3},
	}
	for _, tt := range tests {
		m := calculateMetadata(tt.total, tt.p)
		if m.LastPage != tt.lastPage || m.TotalRecords != tt.total {
			t.Errorf("calculateMetadata(%d, %+v) = %+v; want last page %d", tt.total, tt.p, m, tt.lastPage)
		}
	}
}

func TestValidateOrderFilters(t *testing.T) {
	now := time.Now()
	valid := OrderFilters{Pagination: Pagination{Page: 1, PageSize: 20}}

	tests := []struct {
		name   string
		modify func(*OrderFilters)
		field  string
	}{
		{"valid", func(*OrderFilters) {}, ""},
		{"statuses", func(f *OrderFilters) { f.Statuses = []OrderStatus{StatusPending, StatusConfirmed} }, ""},
		{"bad status", func(f *OrderFilters) { f.Statuses = []OrderStatus{StatusPending, "lost"} }, "status"},
		{"bad fulfillment", func(f *OrderFilters) { f.Fulfillment = "drone" }, "fulfillment"},
		{"reversed range", func(f *OrderFilters) { f.From = now; f.To = now.Add(-time.Hour) }, "to"},
		{"page zero", func(f *OrderFilters) { f.Page = 0 }, "page"},
		{"page size too big", func(f *OrderFilters) { f.PageSize = 101 }, "page_size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := valid
			tt.modify(&f)
			v := validator.New()
			ValidateOrderFilters(v, f)
			checkField(t, v, tt.field)
		})
	}
}

func TestValidateStatusChange(t *testing.T) {
	tests := []struct {
		to     OrderStatus
		reason string
		field  string
	}{
		{StatusConfirmed, "", ""},
		{StatusCancelled, "out of dough", ""},
		{"", "", "status"},
		{"lost", "", "status"},
		{StatusConfirmed, "because", "reason"},
		{StatusCancelled, strings.Repeat("x", 256), "reason"},
	}
	for _, tt := range tests {
		v := validator.New()
		ValidateStatusChange(v, tt.to, tt.reason)
		checkField(t, v, tt.field)
	}
}

func TestListOrders(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()
	staff := Actor{Type: ActorUser}

	// Five orders: delivery, pickup, delivery (confirmed), pickup (cancelled), delivery.
	var ids []int64
	for i := range 5 {
		req := f.request()
		if i%2 == 1 {
			req.Fulfillment = FulfillmentPickup
			req.Address = Address{}
		}
		o, _, err := f.m.Orders.Create(ctx, f.restaurant, req)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, o.ID)
	}
	if err := f.m.Orders.Transition(ctx, f.restaurant.ID, ids[2], StatusConfirmed, staff, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Orders.Transition(ctx, f.restaurant.ID, ids[3], StatusCancelled, staff, "out of dough"); err != nil {
		t.Fatal(err)
	}

	// Another restaurant's order must never show up.
	other := newRestaurant(t, f.m, "other")
	otherCategory := newCategory(t, f.m, other.ID, "Menu", 0)
	otherItem := newMenuItem(t, f.m, other.ID, otherCategory.ID, "Theirs", 0)
	otherReq := f.request()
	otherReq.Fulfillment, otherReq.Address = FulfillmentPickup, Address{}
	otherReq.Lines = []OrderLine{{MenuItemID: otherItem.ID, Quantity: 1}}
	if _, _, err := f.m.Orders.Create(ctx, other, otherReq); err != nil {
		t.Fatal(err)
	}

	list := func(filters OrderFilters) ([]int64, Metadata) {
		t.Helper()
		if filters.Pagination == (Pagination{}) {
			filters.Pagination = Pagination{Page: 1, PageSize: 20}
		}
		orders, meta, err := f.m.Orders.List(ctx, f.restaurant.ID, filters)
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for _, o := range orders {
			got = append(got, o.ID)
			if len(o.Items) != 2 {
				t.Errorf("order %d has %d items; want 2", o.ID, len(o.Items))
			}
		}
		return got, meta
	}

	equal := func(got []int64, want ...int64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	if got, meta := list(OrderFilters{}); !equal(got, ids[4], ids[3], ids[2], ids[1], ids[0]) || meta.TotalRecords != 5 {
		t.Errorf("all, newest first = %v (%+v)", got, meta)
	}
	if got, _ := list(OrderFilters{OldestFirst: true}); !equal(got, ids...) {
		t.Errorf("oldest first = %v", got)
	}
	if got, _ := list(OrderFilters{Statuses: []OrderStatus{StatusConfirmed, StatusCancelled}}); !equal(got, ids[3], ids[2]) {
		t.Errorf("confirmed or cancelled = %v", got)
	}
	if got, _ := list(OrderFilters{Fulfillment: FulfillmentPickup}); !equal(got, ids[3], ids[1]) {
		t.Errorf("pickup = %v", got)
	}

	got, meta := list(OrderFilters{OldestFirst: true, Pagination: Pagination{Page: 3, PageSize: 2}})
	if !equal(got, ids[4]) || meta.LastPage != 3 || meta.TotalRecords != 5 || meta.CurrentPage != 3 {
		t.Errorf("page 3 of 2 = %v (%+v)", got, meta)
	}

	got, meta = list(OrderFilters{Pagination: Pagination{Page: 9, PageSize: 2}})
	if len(got) != 0 || meta.TotalRecords != 0 {
		t.Errorf("page past the end = %v (%+v)", got, meta)
	}

	third, err := f.m.Orders.Get(ctx, f.restaurant.ID, ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := list(OrderFilters{From: third.CreatedAt, OldestFirst: true}); !equal(got, ids[2], ids[3], ids[4]) {
		t.Errorf("from the third order = %v", got)
	}
	if got, _ := list(OrderFilters{To: third.CreatedAt, OldestFirst: true}); !equal(got, ids[0], ids[1]) {
		t.Errorf("before the third order = %v", got)
	}

	cancelled, err := f.m.Orders.Get(ctx, f.restaurant.ID, ids[3])
	if err != nil {
		t.Fatal(err)
	}
	if h := cancelled.History[len(cancelled.History)-1]; h.Reason != "out of dough" || h.To != StatusCancelled {
		t.Errorf("history = %+v", h)
	}
}

func TestConcurrentTransitionsOnlyOneWins(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()

	order, _, err := f.m.Orders.Create(ctx, f.restaurant, f.request())
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 10
	errs := make(chan error, attempts)
	for range attempts {
		go func() {
			errs <- f.m.Orders.Transition(ctx, f.restaurant.ID, order.ID, StatusConfirmed, Actor{Type: ActorUser}, "")
		}()
	}

	succeeded := 0
	for range attempts {
		if err := <-errs; err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Errorf("%d of %d concurrent confirmations succeeded; want exactly 1", succeeded, attempts)
	}

	got, err := f.m.Orders.Get(ctx, f.restaurant.ID, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.History) != 2 || got.Version != 2 {
		t.Errorf("history has %d entries, version %d; want 2 and 2", len(got.History), got.Version)
	}
}
