package main

import (
	"fmt"
	"net/http"
	"testing"

	"menugo.flayshon.com/internal/data"
)

func restaurantPath(id int64) string {
	return fmt.Sprintf("/v1/restaurants/%d", id)
}

func TestCreateRestaurant(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, token := ts.signUp(t, "alice@example.com")

	res := ts.do(t, http.MethodPost, "/v1/restaurants", token, map[string]string{
		"name": "Pizza Place", "slug": "pizza-place", "currency": "BRL", "city": "São Paulo",
	})
	assertStatus(t, res, http.StatusCreated)

	created := decode[struct {
		Restaurant restaurantResponse `json:"restaurant"`
	}](t, res.body).Restaurant
	if created.ID == 0 || created.Slug != "pizza-place" || created.City != "São Paulo" || created.Version != 1 {
		t.Errorf("restaurant = %+v", created)
	}
	if loc := res.header.Get("Location"); loc != restaurantPath(created.ID) {
		t.Errorf("Location = %q", loc)
	}

	t.Run("creator is the owner", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/restaurants", token, nil)
		assertStatus(t, res, http.StatusOK)
		list := decode[struct {
			Restaurants []struct {
				ID   int64     `json:"id"`
				Role data.Role `json:"role"`
			} `json:"restaurants"`
		}](t, res.body).Restaurants
		if len(list) != 1 || list[0].ID != created.ID || list[0].Role != data.RoleOwner {
			t.Errorf("restaurants = %+v", list)
		}
	})

	t.Run("duplicate slug", func(t *testing.T) {
		res := ts.do(t, http.MethodPost, "/v1/restaurants", token, map[string]string{
			"name": "Other", "slug": "pizza-place", "currency": "BRL",
		})
		assertStatus(t, res, http.StatusUnprocessableEntity)
		if _, ok := validationErrors(t, res.body)["slug"]; !ok {
			t.Errorf("expected a slug error: %s", res.body)
		}
	})

	t.Run("validation", func(t *testing.T) {
		res := ts.do(t, http.MethodPost, "/v1/restaurants", token, map[string]string{
			"name": "", "slug": "Bad Slug", "currency": "XXX",
		})
		assertStatus(t, res, http.StatusUnprocessableEntity)
		errs := validationErrors(t, res.body)
		for _, field := range []string{"name", "slug", "currency"} {
			if _, ok := errs[field]; !ok {
				t.Errorf("expected an error for %q: %v", field, errs)
			}
		}
	})

	t.Run("requires authentication", func(t *testing.T) {
		res := ts.do(t, http.MethodPost, "/v1/restaurants", "", map[string]string{
			"name": "Anon", "slug": "anon", "currency": "BRL",
		})
		assertStatus(t, res, http.StatusUnauthorized)
	})
}

func TestUpdateRestaurant(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, token := ts.signUp(t, "alice@example.com")
	r := ts.createRestaurant(t, token, "pizza")
	ts.createRestaurant(t, token, "taken")
	path := restaurantPath(r.ID)

	res := ts.do(t, http.MethodPatch, path, token, map[string]any{"name": "New Name", "version": 1})
	assertStatus(t, res, http.StatusOK)
	updated := decode[struct {
		Restaurant restaurantResponse `json:"restaurant"`
	}](t, res.body).Restaurant
	if updated.Name != "New Name" || updated.Slug != "pizza" || updated.Currency != "BRL" || updated.Version != 2 {
		t.Errorf("partial update changed the wrong fields: %+v", updated)
	}

	t.Run("stale version", func(t *testing.T) {
		res := ts.do(t, http.MethodPatch, path, token, map[string]any{"name": "Stale", "version": 1})
		assertStatus(t, res, http.StatusConflict)
	})

	t.Run("invalid value", func(t *testing.T) {
		res := ts.do(t, http.MethodPatch, path, token, map[string]any{"email": "not-an-email"})
		assertStatus(t, res, http.StatusUnprocessableEntity)
	})

	t.Run("slug in use", func(t *testing.T) {
		res := ts.do(t, http.MethodPatch, path, token, map[string]any{"slug": "taken"})
		assertStatus(t, res, http.StatusUnprocessableEntity)
	})

	t.Run("cannot change id", func(t *testing.T) {
		res := ts.do(t, http.MethodPatch, path, token, map[string]any{"id": 999})
		assertStatus(t, res, http.StatusBadRequest)
	})
}

func TestDeleteRestaurant(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, token := ts.signUp(t, "alice@example.com")
	r := ts.createRestaurant(t, token, "pizza")

	res := ts.do(t, http.MethodDelete, restaurantPath(r.ID), token, nil)
	assertStatus(t, res, http.StatusNoContent)

	res = ts.do(t, http.MethodGet, restaurantPath(r.ID), token, nil)
	assertStatus(t, res, http.StatusNotFound)
}

// TestTenantIsolation checks that knowing another restaurant's ID gives a
// user no access to it, and no way to tell it exists.
func TestTenantIsolation(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, alice := ts.signUp(t, "alice@example.com")
	_, bob := ts.signUp(t, "bob@example.com")

	alicesRestaurant := ts.createRestaurant(t, alice, "alices")
	bobsRestaurant := ts.createRestaurant(t, bob, "bobs")

	notFound := ts.do(t, http.MethodGet, restaurantPath(999999), alice, nil)
	assertStatus(t, notFound, http.StatusNotFound)

	bobsPath := restaurantPath(bobsRestaurant.ID)
	requests := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, bobsPath, nil},
		{http.MethodPatch, bobsPath, map[string]string{"name": "Hijacked"}},
		{http.MethodDelete, bobsPath, nil},
		{http.MethodGet, bobsPath + "/members", nil},
		{http.MethodPost, bobsPath + "/members", map[string]string{"email": "alice@example.com", "role": "restaurant_admin"}},
		{http.MethodDelete, fmt.Sprintf("%s/members/%d", bobsPath, 1), nil},
	}

	for _, req := range requests {
		t.Run(req.method+" "+req.path, func(t *testing.T) {
			res := ts.do(t, req.method, req.path, alice, req.body)
			assertStatus(t, res, http.StatusNotFound)
			if string(res.body) != string(notFound.body) {
				t.Errorf("response differs from a missing restaurant's:\n%s\n%s", res.body, notFound.body)
			}
		})
	}

	t.Run("bob's restaurant is unchanged", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, bobsPath, bob, nil)
		assertStatus(t, res, http.StatusOK)
		got := decode[struct {
			Restaurant restaurantResponse `json:"restaurant"`
		}](t, res.body).Restaurant
		if got.Name != bobsRestaurant.Name || got.Version != 1 {
			t.Errorf("restaurant = %+v", got)
		}
	})

	t.Run("lists only show own restaurants", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/restaurants", alice, nil)
		list := decode[struct {
			Restaurants []restaurantResponse `json:"restaurants"`
		}](t, res.body).Restaurants
		if len(list) != 1 || list[0].ID != alicesRestaurant.ID {
			t.Errorf("alice sees %+v", list)
		}
	})
}

func TestRestaurantRoles(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, owner := ts.signUp(t, "owner@example.com")
	adminID, admin := ts.signUp(t, "admin@example.com")
	staffID, staff := ts.signUp(t, "staff@example.com")
	_, driver := ts.signUp(t, "driver@example.com")
	ts.signUp(t, "newcomer@example.com")

	r := ts.createRestaurant(t, owner, "pizza")
	path := restaurantPath(r.ID)

	assertStatus(t, ts.addMember(t, owner, r.ID, "admin@example.com", data.RoleAdmin), http.StatusCreated)
	assertStatus(t, ts.addMember(t, owner, r.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)
	assertStatus(t, ts.addMember(t, owner, r.ID, "driver@example.com", data.RoleDriver), http.StatusCreated)

	tests := []struct {
		name   string
		token  string
		method string
		path   string
		body   any
		want   int
	}{
		{"staff can view", staff, http.MethodGet, path, nil, http.StatusOK},
		{"driver can view", driver, http.MethodGet, path, nil, http.StatusOK},
		{"staff cannot update", staff, http.MethodPatch, path, map[string]string{"name": "x"}, http.StatusForbidden},
		{"staff cannot list members", staff, http.MethodGet, path + "/members", nil, http.StatusForbidden},
		{"driver cannot delete", driver, http.MethodDelete, path, nil, http.StatusForbidden},
		{"admin can update", admin, http.MethodPatch, path, map[string]string{"name": "Admin was here"}, http.StatusOK},
		{"admin cannot delete", admin, http.MethodDelete, path, nil, http.StatusForbidden},
		{"admin can list members", admin, http.MethodGet, path + "/members", nil, http.StatusOK},
		{"staff cannot add members", staff, http.MethodPost, path + "/members", map[string]string{"email": "newcomer@example.com", "role": "driver"}, http.StatusForbidden},
		{"admin cannot grant admin", admin, http.MethodPost, path + "/members", map[string]string{"email": "newcomer@example.com", "role": "restaurant_admin"}, http.StatusForbidden},
		{"owner cannot grant owner", owner, http.MethodPost, path + "/members", map[string]string{"email": "newcomer@example.com", "role": "restaurant_owner"}, http.StatusForbidden},
		{"unknown role", owner, http.MethodPost, path + "/members", map[string]string{"email": "newcomer@example.com", "role": "chef"}, http.StatusUnprocessableEntity},
		{"unregistered email", owner, http.MethodPost, path + "/members", map[string]string{"email": "ghost@example.com", "role": "driver"}, http.StatusUnprocessableEntity},
		{"already a member", owner, http.MethodPost, path + "/members", map[string]string{"email": "staff@example.com", "role": "driver"}, http.StatusConflict},
		{"admin cannot remove admin", admin, http.MethodDelete, fmt.Sprintf("%s/members/%d", path, adminID), nil, http.StatusForbidden},
		{"non-member user ID", owner, http.MethodDelete, fmt.Sprintf("%s/members/%d", path, 999999), nil, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ts.do(t, tt.method, tt.path, tt.token, tt.body)
			assertStatus(t, res, tt.want)
		})
	}

	t.Run("admin can add staff", func(t *testing.T) {
		assertStatus(t, ts.addMember(t, admin, r.ID, "newcomer@example.com", data.RoleStaff), http.StatusCreated)
	})

	t.Run("owner cannot be removed", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, path+"/members", owner, nil)
		members := decode[struct {
			Members []memberResponse `json:"members"`
		}](t, res.body).Members
		if len(members) != 5 || members[0].Role != data.RoleOwner {
			t.Fatalf("members = %+v", members)
		}

		res = ts.do(t, http.MethodDelete, fmt.Sprintf("%s/members/%d", path, members[0].UserID), admin, nil)
		assertStatus(t, res, http.StatusForbidden)
	})

	t.Run("removed member loses access", func(t *testing.T) {
		res := ts.do(t, http.MethodDelete, fmt.Sprintf("%s/members/%d", path, staffID), admin, nil)
		assertStatus(t, res, http.StatusNoContent)

		res = ts.do(t, http.MethodGet, path, staff, nil)
		assertStatus(t, res, http.StatusNotFound)
	})
}

func TestRestoreRestaurantAPI(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, owner := ts.signUp(t, "owner@example.com")
	_, staff := ts.signUp(t, "staff@example.com")
	r := ts.createRestaurant(t, owner, "pizza")
	assertStatus(t, ts.addMember(t, owner, r.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)

	restorePath := restaurantPath(r.ID) + "/restore"
	assertStatus(t, ts.do(t, http.MethodPost, restorePath, owner, nil), http.StatusNotFound) // not deleted

	assertStatus(t, ts.do(t, http.MethodDelete, restaurantPath(r.ID), owner, nil), http.StatusNoContent)
	assertStatus(t, ts.do(t, http.MethodGet, restaurantPath(r.ID), staff, nil), http.StatusNotFound)

	res := ts.do(t, http.MethodGet, "/v1/restaurants/deleted", owner, nil)
	assertStatus(t, res, http.StatusOK)
	deleted := decode[struct {
		Restaurants []restaurantResponse `json:"restaurants"`
	}](t, res.body).Restaurants
	if len(deleted) != 1 || deleted[0].ID != r.ID || deleted[0].DeletedAt == nil {
		t.Fatalf("deleted = %+v", deleted)
	}

	res = ts.do(t, http.MethodGet, "/v1/restaurants/deleted", staff, nil)
	if list := decode[struct {
		Restaurants []restaurantResponse `json:"restaurants"`
	}](t, res.body).Restaurants; len(list) != 0 {
		t.Errorf("staff see deleted restaurants: %+v", list)
	}
	assertStatus(t, ts.do(t, http.MethodPost, restorePath, staff, nil), http.StatusNotFound)

	ts.createRestaurant(t, owner, "pizza") // takes the old slug

	res = ts.do(t, http.MethodPost, restorePath, owner, nil)
	assertStatus(t, res, http.StatusUnprocessableEntity)
	if _, ok := validationErrors(t, res.body)["slug"]; !ok {
		t.Errorf("expected a slug error: %s", res.body)
	}
	assertStatus(t, ts.do(t, http.MethodPost, restorePath, owner, map[string]string{"slug": "Bad Slug"}), http.StatusUnprocessableEntity)

	res = ts.do(t, http.MethodPost, restorePath, owner, map[string]string{"slug": "pizza-again"})
	assertStatus(t, res, http.StatusOK)
	restored := decode[struct {
		Restaurant restaurantResponse `json:"restaurant"`
	}](t, res.body).Restaurant
	if restored.Slug != "pizza-again" || restored.DeletedAt != nil || restored.IsPublished {
		t.Errorf("restored = %+v", restored)
	}

	// The staff member is back in.
	assertStatus(t, ts.do(t, http.MethodGet, restaurantPath(r.ID), staff, nil), http.StatusOK)
}
