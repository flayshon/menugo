package data

import (
	"context"
	"errors"
	"testing"
)

func TestSoftDeleteRestaurant(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()
	r := f.restaurant

	order, token, err := f.m.Orders.Create(ctx, r, f.request())
	if err != nil {
		t.Fatal(err)
	}

	if err := f.m.Restaurants.Delete(ctx, r.ID); !errors.Is(err, ErrRestaurantBusy) {
		t.Fatalf("deleting with a pending order: err = %v; want ErrRestaurantBusy", err)
	}

	if err := f.m.Orders.Transition(ctx, r.ID, order.ID, StatusCancelled, Actor{Type: ActorUser}, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Restaurants.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Restaurants.Delete(ctx, r.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("second delete: err = %v; want ErrRecordNotFound", err)
	}

	owner, err := f.m.Users.GetByEmail(ctx, "pizza@example.com")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("hidden from everyone", func(t *testing.T) {
		if _, err := f.m.Memberships.Get(ctx, r.ID, owner.ID); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("membership: err = %v; want ErrRecordNotFound", err)
		}
		if list, _ := f.m.Restaurants.ListForUser(ctx, owner.ID); len(list) != 0 {
			t.Errorf("still listed: %+v", list)
		}
		if _, err := f.m.Restaurants.GetPublishedBySlug(ctx, "pizza"); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("public menu: err = %v", err)
		}
		if _, _, err := f.m.Orders.Create(ctx, r, f.request()); !errors.Is(err, ErrRecordNotFound) {
			t.Errorf("ordering from a stale copy: err = %v; want ErrRecordNotFound", err)
		}
	})

	t.Run("data is kept", func(t *testing.T) {
		got, err := f.m.Orders.GetByTrackingToken(ctx, token)
		if err != nil || got.ID != order.ID {
			t.Errorf("tracking: %v, %v", got, err)
		}
		items, err := f.m.MenuItems.List(ctx, r.ID, 0)
		if err != nil || len(items) != 2 {
			t.Errorf("menu items = %d, %v; want 2", len(items), err)
		}
		deleted, err := f.m.Restaurants.Get(ctx, r.ID)
		if err != nil || deleted.IsPublished {
			t.Errorf("restaurant = %+v, %v; want it unpublished", deleted, err)
		}
	})

	t.Run("the slug can be reused", func(t *testing.T) {
		again := &Restaurant{Name: "New pizza", Slug: "pizza", Currency: "BRL"}
		if err := f.m.Restaurants.InsertWithOwner(ctx, again, owner.ID); err != nil {
			t.Fatal(err)
		}
		dup := &Restaurant{Name: "Taken", Slug: "pizza", Currency: "BRL"}
		if err := f.m.Restaurants.InsertWithOwner(ctx, dup, owner.ID); !errors.Is(err, ErrDuplicateSlug) {
			t.Errorf("err = %v; want ErrDuplicateSlug", err)
		}
	})
}
