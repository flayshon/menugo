package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"menugo.flayshon.com/internal/data"
)

func menuPath(restaurantID int64, rest string) string {
	return fmt.Sprintf("/v1/restaurants/%d/menu%s", restaurantID, rest)
}

func (ts *testServer) createCategory(t *testing.T, token string, restaurantID int64, name string, sortOrder int) categoryResponse {
	t.Helper()
	res := ts.do(t, http.MethodPost, menuPath(restaurantID, "/categories"), token, map[string]any{
		"name": name, "sort_order": sortOrder,
	})
	assertStatus(t, res, http.StatusCreated)
	return decode[struct {
		Category categoryResponse `json:"category"`
	}](t, res.body).Category
}

func (ts *testServer) createMenuItem(t *testing.T, token string, restaurantID, categoryID int64, name string, priceCents int64) menuItemResponse {
	t.Helper()
	res := ts.do(t, http.MethodPost, menuPath(restaurantID, "/items"), token, map[string]any{
		"category_id": categoryID, "name": name, "price_cents": priceCents,
	})
	assertStatus(t, res, http.StatusCreated)
	return decode[struct {
		Item menuItemResponse `json:"item"`
	}](t, res.body).Item
}

func TestMenuManagement(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, token := ts.signUp(t, "owner@example.com")
	r := ts.createRestaurant(t, token, "pizza")

	drinks := ts.createCategory(t, token, r.ID, "Drinks", 20)
	pizzas := ts.createCategory(t, token, r.ID, "Pizzas", 10)
	ts.createCategory(t, token, r.ID, "Desserts", 30)

	if !pizzas.IsVisible {
		t.Error("categories should be visible by default")
	}

	res := ts.do(t, http.MethodPost, menuPath(r.ID, "/items"), token, map[string]any{
		"category_id": pizzas.ID, "name": "Margherita", "price_cents": 4500,
		"description": "Tomato, mozzarella, basil", "image_url": "https://cdn.example.com/m.jpg", "sort_order": 1,
	})
	assertStatus(t, res, http.StatusCreated)
	margherita := decode[struct {
		Item menuItemResponse `json:"item"`
	}](t, res.body).Item
	if margherita.PriceCents != 4500 || !margherita.IsAvailable || margherita.CategoryID != pizzas.ID {
		t.Errorf("item = %+v", margherita)
	}
	if loc := res.header.Get("Location"); loc != menuPath(r.ID, fmt.Sprintf("/items/%d", margherita.ID)) {
		t.Errorf("Location = %q", loc)
	}

	ts.createMenuItem(t, token, r.ID, pizzas.ID, "Calabresa", 4200) // sort_order 0: listed first
	ts.createMenuItem(t, token, r.ID, drinks.ID, "Guaraná", 700)

	t.Run("full menu is grouped and ordered", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, menuPath(r.ID, ""), token, nil)
		assertStatus(t, res, http.StatusOK)

		menu := decode[struct {
			Menu struct {
				Categories []struct {
					Name  string             `json:"name"`
					Items []menuItemResponse `json:"items"`
				} `json:"categories"`
			} `json:"menu"`
		}](t, res.body).Menu

		var got []string
		for _, c := range menu.Categories {
			var names []string
			for _, i := range c.Items {
				names = append(names, i.Name)
			}
			got = append(got, c.Name+"["+strings.Join(names, ",")+"]")
		}
		want := "Pizzas[Calabresa,Margherita] Drinks[Guaraná] Desserts[]"
		if strings.Join(got, " ") != want {
			t.Errorf("menu = %s; want %s", strings.Join(got, " "), want)
		}
		// Empty categories have "items": [], not null.
		if !strings.Contains(string(res.body), `"items": []`) {
			t.Errorf("empty category should have an empty items array: %s", res.body)
		}
	})

	t.Run("list items by category", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, menuPath(r.ID, fmt.Sprintf("/items?category_id=%d", drinks.ID)), token, nil)
		assertStatus(t, res, http.StatusOK)
		items := decode[struct {
			Items []menuItemResponse `json:"items"`
		}](t, res.body).Items
		if len(items) != 1 || items[0].Name != "Guaraná" {
			t.Errorf("items = %+v", items)
		}

		res = ts.do(t, http.MethodGet, menuPath(r.ID, "/items?category_id=abc"), token, nil)
		assertStatus(t, res, http.StatusUnprocessableEntity)
	})

	t.Run("create item validation", func(t *testing.T) {
		tests := []struct {
			name  string
			body  map[string]any
			field string
		}{
			{"missing price", map[string]any{"category_id": pizzas.ID, "name": "Free?"}, "price_cents"},
			{"negative price", map[string]any{"category_id": pizzas.ID, "name": "x", "price_cents": -1}, "price_cents"},
			{"missing category", map[string]any{"name": "x", "price_cents": 1}, "category_id"},
			{"unknown category", map[string]any{"category_id": 999999, "name": "x", "price_cents": 1}, "category_id"},
			{"bad image URL", map[string]any{"category_id": pizzas.ID, "name": "x", "price_cents": 1, "image_url": "ftp://x/y"}, "image_url"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := ts.do(t, http.MethodPost, menuPath(r.ID, "/items"), token, tt.body)
				assertStatus(t, res, http.StatusUnprocessableEntity)
				if _, ok := validationErrors(t, res.body)[tt.field]; !ok {
					t.Errorf("expected an error for %q: %s", tt.field, res.body)
				}
			})
		}

		// Prices are integers: a decimal is a type error, not a rounded value.
		res := ts.do(t, http.MethodPost, menuPath(r.ID, "/items"), token, `{"category_id": 1, "name": "x", "price_cents": 45.5}`)
		assertStatus(t, res, http.StatusBadRequest)
	})

	t.Run("update item", func(t *testing.T) {
		path := menuPath(r.ID, fmt.Sprintf("/items/%d", margherita.ID))

		res := ts.do(t, http.MethodPatch, path, token, map[string]any{"price_cents": 4900, "category_id": drinks.ID, "version": margherita.Version})
		assertStatus(t, res, http.StatusOK)
		updated := decode[struct {
			Item menuItemResponse `json:"item"`
		}](t, res.body).Item
		if updated.PriceCents != 4900 || updated.CategoryID != drinks.ID || updated.Name != "Margherita" || updated.Version != margherita.Version+1 {
			t.Errorf("item = %+v", updated)
		}

		res = ts.do(t, http.MethodPatch, path, token, map[string]any{"name": "Stale", "version": margherita.Version})
		assertStatus(t, res, http.StatusConflict)

		res = ts.do(t, http.MethodPatch, path, token, map[string]any{"category_id": 999999})
		assertStatus(t, res, http.StatusUnprocessableEntity)
	})

	t.Run("update category", func(t *testing.T) {
		path := menuPath(r.ID, fmt.Sprintf("/categories/%d", drinks.ID))

		res := ts.do(t, http.MethodPatch, path, token, map[string]any{"is_visible": false})
		assertStatus(t, res, http.StatusOK)
		c := decode[struct {
			Category categoryResponse `json:"category"`
		}](t, res.body).Category
		if c.IsVisible || c.Name != "Drinks" {
			t.Errorf("category = %+v", c)
		}

		res = ts.do(t, http.MethodPatch, path, token, map[string]any{"name": "pizzas"})
		assertStatus(t, res, http.StatusUnprocessableEntity)
	})

	t.Run("delete category only when empty", func(t *testing.T) {
		res := ts.do(t, http.MethodDelete, menuPath(r.ID, fmt.Sprintf("/categories/%d", pizzas.ID)), token, nil)
		assertStatus(t, res, http.StatusConflict)

		empty := ts.createCategory(t, token, r.ID, "Empty", 0)
		res = ts.do(t, http.MethodDelete, menuPath(r.ID, fmt.Sprintf("/categories/%d", empty.ID)), token, nil)
		assertStatus(t, res, http.StatusNoContent)

		res = ts.do(t, http.MethodGet, menuPath(r.ID, fmt.Sprintf("/categories/%d", empty.ID)), token, nil)
		assertStatus(t, res, http.StatusNotFound)
	})

	t.Run("delete item", func(t *testing.T) {
		path := menuPath(r.ID, fmt.Sprintf("/items/%d", margherita.ID))
		assertStatus(t, ts.do(t, http.MethodDelete, path, token, nil), http.StatusNoContent)
		assertStatus(t, ts.do(t, http.MethodDelete, path, token, nil), http.StatusNotFound)
	})
}

func TestMenuRoles(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, owner := ts.signUp(t, "owner@example.com")
	_, staff := ts.signUp(t, "staff@example.com")
	_, driver := ts.signUp(t, "driver@example.com")

	r := ts.createRestaurant(t, owner, "pizza")
	assertStatus(t, ts.addMember(t, owner, r.ID, "staff@example.com", data.RoleStaff), http.StatusCreated)
	assertStatus(t, ts.addMember(t, owner, r.ID, "driver@example.com", data.RoleDriver), http.StatusCreated)

	c := ts.createCategory(t, owner, r.ID, "Pizzas", 0)
	item := ts.createMenuItem(t, owner, r.ID, c.ID, "Margherita", 4500)
	itemPath := menuPath(r.ID, fmt.Sprintf("/items/%d", item.ID))

	tests := []struct {
		name   string
		token  string
		method string
		path   string
		body   any
		want   int
	}{
		{"staff can read the menu", staff, http.MethodGet, menuPath(r.ID, ""), nil, http.StatusOK},
		{"staff can read an item", staff, http.MethodGet, itemPath, nil, http.StatusOK},
		{"staff cannot create categories", staff, http.MethodPost, menuPath(r.ID, "/categories"), map[string]any{"name": "x"}, http.StatusForbidden},
		{"staff cannot edit items", staff, http.MethodPatch, itemPath, map[string]any{"price_cents": 1}, http.StatusForbidden},
		{"staff cannot delete items", staff, http.MethodDelete, itemPath, nil, http.StatusForbidden},
		{"staff can mark sold out", staff, http.MethodPut, itemPath + "/availability", map[string]any{"is_available": false}, http.StatusOK},
		{"availability is required", staff, http.MethodPut, itemPath + "/availability", map[string]any{}, http.StatusUnprocessableEntity},
		{"driver cannot read the menu", driver, http.MethodGet, menuPath(r.ID, ""), nil, http.StatusForbidden},
		{"driver cannot mark sold out", driver, http.MethodPut, itemPath + "/availability", map[string]any{"is_available": true}, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, ts.do(t, tt.method, tt.path, tt.token, tt.body), tt.want)
		})
	}

	res := ts.do(t, http.MethodGet, itemPath, owner, nil)
	got := decode[struct {
		Item menuItemResponse `json:"item"`
	}](t, res.body).Item
	if got.IsAvailable || got.PriceCents != 4500 {
		t.Errorf("item = %+v; want sold out at the original price", got)
	}
}

// TestMenuTenantIsolation checks that menu IDs from another restaurant can't
// be read, changed or referenced, even through the user's own restaurant.
func TestMenuTenantIsolation(t *testing.T) {
	t.Parallel()
	app := newTestApplicationWithDB(t)
	ts := newTestServer(t, app.routes())

	_, alice := ts.signUp(t, "alice@example.com")
	_, bob := ts.signUp(t, "bob@example.com")

	alices := ts.createRestaurant(t, alice, "alices")
	alicesCategory := ts.createCategory(t, alice, alices.ID, "Mine", 0)
	alicesItem := ts.createMenuItem(t, alice, alices.ID, alicesCategory.ID, "Mine", 100)

	bobs := ts.createRestaurant(t, bob, "bobs")
	bobsCategory := ts.createCategory(t, bob, bobs.ID, "Secret", 0)
	bobsItem := ts.createMenuItem(t, bob, bobs.ID, bobsCategory.ID, "Secret recipe", 9900)

	t.Run("bob's restaurant is invisible to alice", func(t *testing.T) {
		for _, path := range []string{
			menuPath(bobs.ID, ""),
			menuPath(bobs.ID, "/items"),
			menuPath(bobs.ID, fmt.Sprintf("/items/%d", bobsItem.ID)),
			menuPath(bobs.ID, fmt.Sprintf("/categories/%d", bobsCategory.ID)),
		} {
			assertStatus(t, ts.do(t, http.MethodGet, path, alice, nil), http.StatusNotFound)
		}
	})

	// Alice uses her own restaurant in the URL but Bob's IDs.
	crossTenant := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{"read bob's item", http.MethodGet, menuPath(alices.ID, fmt.Sprintf("/items/%d", bobsItem.ID)), nil, http.StatusNotFound},
		{"edit bob's item", http.MethodPatch, menuPath(alices.ID, fmt.Sprintf("/items/%d", bobsItem.ID)), map[string]any{"price_cents": 1}, http.StatusNotFound},
		{"sell out bob's item", http.MethodPut, menuPath(alices.ID, fmt.Sprintf("/items/%d/availability", bobsItem.ID)), map[string]any{"is_available": false}, http.StatusNotFound},
		{"delete bob's item", http.MethodDelete, menuPath(alices.ID, fmt.Sprintf("/items/%d", bobsItem.ID)), nil, http.StatusNotFound},
		{"read bob's category", http.MethodGet, menuPath(alices.ID, fmt.Sprintf("/categories/%d", bobsCategory.ID)), nil, http.StatusNotFound},
		{"edit bob's category", http.MethodPatch, menuPath(alices.ID, fmt.Sprintf("/categories/%d", bobsCategory.ID)), map[string]any{"name": "Hijacked"}, http.StatusNotFound},
		{"delete bob's category", http.MethodDelete, menuPath(alices.ID, fmt.Sprintf("/categories/%d", bobsCategory.ID)), nil, http.StatusNotFound},
		{"create item in bob's category", http.MethodPost, menuPath(alices.ID, "/items"), map[string]any{"category_id": bobsCategory.ID, "name": "x", "price_cents": 1}, http.StatusUnprocessableEntity},
		{"move item into bob's category", http.MethodPatch, menuPath(alices.ID, fmt.Sprintf("/items/%d", alicesItem.ID)), map[string]any{"category_id": bobsCategory.ID}, http.StatusUnprocessableEntity},
		{"list bob's category's items", http.MethodGet, menuPath(alices.ID, fmt.Sprintf("/items?category_id=%d", bobsCategory.ID)), nil, http.StatusOK},
	}

	for _, tt := range crossTenant {
		t.Run(tt.name, func(t *testing.T) {
			res := ts.do(t, tt.method, tt.path, alice, tt.body)
			assertStatus(t, res, tt.want)
			if strings.Contains(string(res.body), "Secret") {
				t.Errorf("response leaks bob's data: %s", res.body)
			}
		})
	}

	t.Run("bob's menu is unchanged", func(t *testing.T) {
		res := ts.do(t, http.MethodGet, menuPath(bobs.ID, fmt.Sprintf("/items/%d", bobsItem.ID)), bob, nil)
		assertStatus(t, res, http.StatusOK)
		got := decode[struct {
			Item menuItemResponse `json:"item"`
		}](t, res.body).Item
		if got.PriceCents != 9900 || !got.IsAvailable || got.Version != 1 {
			t.Errorf("bob's item = %+v", got)
		}
	})
}
