package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"menugo.flayshon.com/internal/data"
)

func TestPublicMenu(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, owner := ts.signUp(t, "owner@example.com")
	_, staff := ts.signUp(t, "staff@example.com")

	res := ts.do(t, http.MethodPost, "/v1/restaurants", owner, map[string]string{
		"name": "Pizza Place", "slug": "pizza-place", "currency": "BRL",
		"email": "private@example.com", "city": "Recife",
	})
	assertStatus(t, res, http.StatusCreated)
	r := decode[struct {
		Restaurant restaurantResponse `json:"restaurant"`
	}](t, res.body).Restaurant
	if r.IsPublished {
		t.Fatal("restaurants should start unpublished")
	}
	assertStatus(t, ts.addMember(t, owner, r.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)

	pizzas := ts.createCategory(t, owner, r.ID, "Pizzas", 1)
	drinks := ts.createCategory(t, owner, r.ID, "Drinks", 2)
	secret := ts.createCategory(t, owner, r.ID, "Staff meals", 3)
	ts.createCategory(t, owner, r.ID, "Coming soon", 4) // visible but empty

	ts.createMenuItem(t, owner, r.ID, pizzas.ID, "Margherita", 4500)
	soda := ts.createMenuItem(t, owner, r.ID, drinks.ID, "Soda", 600)
	ts.createMenuItem(t, owner, r.ID, secret.ID, "Leftovers", 0)

	assertStatus(t, ts.do(t, http.MethodPatch, menuPath(r.ID, fmt.Sprintf("/categories/%d", secret.ID)), owner,
		map[string]any{"is_visible": false}), http.StatusOK)
	assertStatus(t, ts.do(t, http.MethodPut, menuPath(r.ID, fmt.Sprintf("/items/%d/availability", soda.ID)), owner,
		map[string]any{"is_available": false}), http.StatusOK)

	unknown := ts.do(t, http.MethodGet, "/v1/menus/no-such-place", "", nil)
	assertStatus(t, unknown, http.StatusNotFound)

	t.Run("unpublished menu looks like a missing one", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/menus/pizza-place", "", nil)
		assertStatus(t, res, http.StatusNotFound)
		if string(res.body) != string(unknown.body) {
			t.Errorf("responses differ:\n%s\n%s", res.body, unknown.body)
		}
	})

	t.Run("staff cannot publish", func(t *testing.T) {
		res := ts.do(t, http.MethodPatch, restaurantPath(r.ID), staff, map[string]any{"is_published": true})
		assertStatus(t, res, http.StatusForbidden)
	})

	res = ts.do(t, http.MethodPatch, restaurantPath(r.ID), owner, map[string]any{"is_published": true})
	assertStatus(t, res, http.StatusOK)

	t.Run("published menu is public", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, "/v1/menus/pizza-place", "", nil)
		assertStatus(t, res, http.StatusOK)

		if got := res.header.Get("Cache-Control"); got != "public, max-age=60" {
			t.Errorf("Cache-Control = %q", got)
		}
		if got := res.header.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Access-Control-Allow-Origin = %q", got)
		}

		menu := decode[struct {
			Menu struct {
				Restaurant publicRestaurant `json:"restaurant"`
				Categories []publicCategory `json:"categories"`
			} `json:"menu"`
		}](t, res.body).Menu

		if menu.Restaurant.Name != "Pizza Place" || menu.Restaurant.City != "Recife" || menu.Restaurant.Currency != "BRL" {
			t.Errorf("restaurant = %+v", menu.Restaurant)
		}

		var got []string
		for _, c := range menu.Categories {
			for _, i := range c.Items {
				got = append(got, fmt.Sprintf("%s/%s/%d/%t", c.Name, i.Name, i.PriceCents, i.IsAvailable))
			}
		}
		want := "Pizzas/Margherita/4500/true Drinks/Soda/600/false"
		if strings.Join(got, " ") != want {
			t.Errorf("menu = %s; want %s", strings.Join(got, " "), want)
		}

		body := string(res.body)
		for _, leak := range []string{"Staff meals", "Leftovers", "Coming soon", "private@example.com", `"version"`, `"created_at"`, `"sort_order"`, `"is_visible"`} {
			if strings.Contains(body, leak) {
				t.Errorf("public menu exposes %s:\n%s", leak, body)
			}
		}
	})

	t.Run("invalid slugs are not found", func(t *testing.T) {
		for _, slug := range []string{"Pizza-Place", "pizza_place", strings.Repeat("a", 64)} {
			assertStatus(t, ts.do(t, http.MethodGet, "/v1/menus/"+slug, "", nil), http.StatusNotFound)
		}
	})

	t.Run("unpublishing hides it again", func(t *testing.T) {
		res := ts.do(t, http.MethodPatch, restaurantPath(r.ID), owner, map[string]any{"is_published": false})
		assertStatus(t, res, http.StatusOK)
		assertStatus(t, ts.do(t, http.MethodGet, "/v1/menus/pizza-place", "", nil), http.StatusNotFound)
	})
}

func TestPublicMenuOnlyShowsItsOwnRestaurant(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, alice := ts.signUp(t, "alice@example.com")
	_, bob := ts.signUp(t, "bob@example.com")

	for _, s := range []struct {
		token, slug, item string
	}{{alice, "alices", "Alice's pizza"}, {bob, "bobs", "Bob's burger"}} {
		r := ts.createRestaurant(t, s.token, s.slug)
		c := ts.createCategory(t, s.token, r.ID, "Food", 0)
		ts.createMenuItem(t, s.token, r.ID, c.ID, s.item, 1000)
		assertStatus(t, ts.do(t, http.MethodPatch, restaurantPath(r.ID), s.token, map[string]any{"is_published": true}), http.StatusOK)
	}

	res := ts.do(t, http.MethodGet, "/v1/menus/alices", "", nil)
	assertStatus(t, res, http.StatusOK)
	if !strings.Contains(string(res.body), "Alice's pizza") || strings.Contains(string(res.body), "Bob's burger") {
		t.Errorf("alice's menu = %s", res.body)
	}
}
