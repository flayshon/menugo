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

func TestRestoreRestaurant(t *testing.T) {
	t.Parallel()
	f := newOrderFixture(t)
	ctx := context.Background()
	r := f.restaurant

	owner, err := f.m.Users.GetByEmail(ctx, "pizza@example.com")
	if err != nil {
		t.Fatal(err)
	}
	admin := insertUser(t, f.m, "admin@example.com")
	if err := f.m.Memberships.Insert(ctx, &Membership{RestaurantID: r.ID, UserID: admin.ID, Role: RoleAdmin}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.m.Restaurants.Restore(ctx, r.ID, owner.ID, ""); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("restoring a live restaurant: err = %v; want ErrRecordNotFound", err)
	}

	if err := f.m.Restaurants.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}

	deleted, err := f.m.Restaurants.ListDeletedForOwner(ctx, owner.ID)
	if err != nil || len(deleted) != 1 || deleted[0].ID != r.ID || deleted[0].DeletedAt.IsZero() {
		t.Fatalf("deleted = %+v, %v", deleted, err)
	}
	if list, _ := f.m.Restaurants.ListDeletedForOwner(ctx, admin.ID); len(list) != 0 {
		t.Errorf("only owners see deleted restaurants: %+v", list)
	}
	if _, err := f.m.Restaurants.Restore(ctx, r.ID, admin.ID, ""); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("admin restoring: err = %v; want ErrRecordNotFound", err)
	}

	// Someone takes the slug in the meantime.
	taker := &Restaurant{Name: "Taker", Slug: "pizza", Currency: "BRL", Timezone: "UTC"}
	if err := f.m.Restaurants.InsertWithOwner(ctx, taker, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Restaurants.Restore(ctx, r.ID, owner.ID, ""); !errors.Is(err, ErrDuplicateSlug) {
		t.Errorf("slug taken: err = %v; want ErrDuplicateSlug", err)
	}

	restored, err := f.m.Restaurants.Restore(ctx, r.ID, owner.ID, "pizza-again")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Slug != "pizza-again" || !restored.DeletedAt.IsZero() || restored.IsPublished {
		t.Errorf("restored = %+v; want the new slug, live and unpublished", restored)
	}

	// Members are back, with their roles.
	if ms, err := f.m.Memberships.Get(ctx, r.ID, admin.ID); err != nil || ms.Role != RoleAdmin {
		t.Errorf("admin membership = %+v, %v", ms, err)
	}
	if items, _ := f.m.MenuItems.List(ctx, r.ID, 0); len(items) != 2 {
		t.Errorf("menu items = %d; want 2", len(items))
	}
}
