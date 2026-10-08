package data

import (
	"context"
	"errors"
	"strings"
	"testing"

	"menugo.flayshon.com/internal/testdb"
	"menugo.flayshon.com/internal/validator"
)

func TestValidateCategory(t *testing.T) {
	tests := []struct {
		name  string
		c     Category
		field string
	}{
		{"valid", Category{Name: "Pizzas", SortOrder: 10}, ""},
		{"blank name", Category{Name: " "}, "name"},
		{"long description", Category{Name: "x", Description: strings.Repeat("x", 501)}, "description"},
		{"negative sort order", Category{Name: "x", SortOrder: -1}, "sort_order"},
		{"huge sort order", Category{Name: "x", SortOrder: MaxSortOrder + 1}, "sort_order"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := validator.New()
			ValidateCategory(v, &tt.c)
			checkField(t, v, tt.field)
		})
	}
}

func TestValidateMenuItem(t *testing.T) {
	valid := MenuItem{CategoryID: 1, Name: "Margherita", PriceCents: 4500}

	tests := []struct {
		name   string
		modify func(*MenuItem)
		field  string
	}{
		{"valid", func(*MenuItem) {}, ""},
		{"free item", func(i *MenuItem) { i.PriceCents = 0 }, ""},
		{"https image", func(i *MenuItem) { i.ImageURL = "https://cdn.example.com/p.jpg" }, ""},
		{"no category", func(i *MenuItem) { i.CategoryID = 0 }, "category_id"},
		{"blank name", func(i *MenuItem) { i.Name = "" }, "name"},
		{"negative price", func(i *MenuItem) { i.PriceCents = -1 }, "price_cents"},
		{"price too high", func(i *MenuItem) { i.PriceCents = MaxPriceCents + 1 }, "price_cents"},
		{"relative image URL", func(i *MenuItem) { i.ImageURL = "/img/p.jpg" }, "image_url"},
		{"javascript image URL", func(i *MenuItem) { i.ImageURL = "javascript:alert(1)" }, "image_url"},
		{"negative sort order", func(i *MenuItem) { i.SortOrder = -5 }, "sort_order"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := valid
			tt.modify(&item)
			v := validator.New()
			ValidateMenuItem(v, &item)
			checkField(t, v, tt.field)
		})
	}
}

// checkField fails unless v has exactly an error for field, or no errors if
// field is empty.
func checkField(t *testing.T, v *validator.Validator, field string) {
	t.Helper()
	if field == "" {
		if !v.Valid() {
			t.Errorf("unexpected errors: %v", v.Errors)
		}
		return
	}
	if _, ok := v.Errors[field]; !ok || len(v.Errors) != 1 {
		t.Errorf("want only an error for %q, got %v", field, v.Errors)
	}
}

// newRestaurant creates a restaurant with an owner, for menu tests.
func newRestaurant(t *testing.T, m Models, slug string) *Restaurant {
	t.Helper()
	owner := insertUser(t, m, slug+"@example.com")
	r := &Restaurant{Name: slug, Slug: slug, Currency: "BRL"}
	if err := m.Restaurants.InsertWithOwner(context.Background(), r, owner.ID); err != nil {
		t.Fatal(err)
	}
	return r
}

func newCategory(t *testing.T, m Models, restaurantID int64, name string, sortOrder int32) *Category {
	t.Helper()
	c := &Category{RestaurantID: restaurantID, Name: name, SortOrder: sortOrder, IsVisible: true}
	if err := m.Categories.Insert(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func newMenuItem(t *testing.T, m Models, restaurantID, categoryID int64, name string, sortOrder int32) *MenuItem {
	t.Helper()
	item := &MenuItem{RestaurantID: restaurantID, CategoryID: categoryID, Name: name, PriceCents: 1000, IsAvailable: true, SortOrder: sortOrder}
	if err := m.MenuItems.Insert(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestMenuItemCannotUseAnotherRestaurantsCategory(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	alice := newRestaurant(t, m, "alice")
	bob := newRestaurant(t, m, "bob")
	bobsCategory := newCategory(t, m, bob.ID, "Pizzas", 0)
	alicesCategory := newCategory(t, m, alice.ID, "Pizzas", 0)

	item := &MenuItem{RestaurantID: alice.ID, CategoryID: bobsCategory.ID, Name: "Sneaky", PriceCents: 100}
	if err := m.MenuItems.Insert(ctx, item); !errors.Is(err, ErrInvalidCategory) {
		t.Errorf("insert: err = %v; want ErrInvalidCategory", err)
	}

	item = newMenuItem(t, m, alice.ID, alicesCategory.ID, "Fine", 0)
	item.CategoryID = bobsCategory.ID
	if err := m.MenuItems.Update(ctx, item); !errors.Is(err, ErrInvalidCategory) {
		t.Errorf("update: err = %v; want ErrInvalidCategory", err)
	}

	item.CategoryID = 999999
	if err := m.MenuItems.Update(ctx, item); !errors.Is(err, ErrInvalidCategory) {
		t.Errorf("update to missing category: err = %v; want ErrInvalidCategory", err)
	}

	// Reads are scoped too.
	if _, err := m.Categories.Get(ctx, alice.ID, bobsCategory.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("get category: err = %v; want ErrRecordNotFound", err)
	}
	if _, err := m.MenuItems.Get(ctx, bob.ID, item.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("get item: err = %v; want ErrRecordNotFound", err)
	}
	if err := m.MenuItems.SetAvailability(ctx, bob.ID, item.ID, false); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("set availability: err = %v; want ErrRecordNotFound", err)
	}
	if err := m.MenuItems.Delete(ctx, bob.ID, item.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("delete item: err = %v; want ErrRecordNotFound", err)
	}
	if err := m.Categories.Delete(ctx, alice.ID, bobsCategory.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("delete category: err = %v; want ErrRecordNotFound", err)
	}
}

func TestCategoryRules(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	r := newRestaurant(t, m, "pizza")
	other := newRestaurant(t, m, "other")
	pizzas := newCategory(t, m, r.ID, "Pizzas", 0)

	// Names are unique per restaurant, ignoring case, but not across them.
	err := m.Categories.Insert(ctx, &Category{RestaurantID: r.ID, Name: "PIZZAS"})
	if !errors.Is(err, ErrDuplicateCategoryName) {
		t.Errorf("err = %v; want ErrDuplicateCategoryName", err)
	}
	newCategory(t, m, other.ID, "Pizzas", 0)

	newMenuItem(t, m, r.ID, pizzas.ID, "Margherita", 0)
	if err := m.Categories.Delete(ctx, r.ID, pizzas.ID); !errors.Is(err, ErrCategoryNotEmpty) {
		t.Errorf("err = %v; want ErrCategoryNotEmpty", err)
	}

	empty := newCategory(t, m, r.ID, "Empty", 1)
	if err := m.Categories.Delete(ctx, r.ID, empty.ID); err != nil {
		t.Errorf("deleting empty category: %v", err)
	}
}

func TestMenuOrderingAndFiltering(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	r := newRestaurant(t, m, "pizza")
	drinks := newCategory(t, m, r.ID, "Drinks", 20)
	pizzas := newCategory(t, m, r.ID, "Pizzas", 10)

	newMenuItem(t, m, r.ID, pizzas.ID, "Pepperoni", 2)
	newMenuItem(t, m, r.ID, drinks.ID, "Soda", 0)
	newMenuItem(t, m, r.ID, pizzas.ID, "Margherita", 1)

	categories, err := m.Categories.List(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 2 || categories[0].Name != "Pizzas" {
		t.Errorf("categories not in sort order: %v, %v", categories[0].Name, categories[1].Name)
	}

	items, err := m.MenuItems.List(ctx, r.ID, pizzas.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range items {
		names = append(names, i.Name)
	}
	if strings.Join(names, ",") != "Margherita,Pepperoni" {
		t.Errorf("pizzas = %v", names)
	}

	all, err := m.MenuItems.List(ctx, r.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("all items = %d; want 3", len(all))
	}
}

func TestMenuItemUpdateAndAvailability(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	r := newRestaurant(t, m, "pizza")
	c := newCategory(t, m, r.ID, "Pizzas", 0)
	item := newMenuItem(t, m, r.ID, c.ID, "Margherita", 0)

	stale := *item

	item.PriceCents = 5000
	if err := m.MenuItems.Update(ctx, item); err != nil {
		t.Fatal(err)
	}

	stale.Name = "Lost update"
	if err := m.MenuItems.Update(ctx, &stale); !errors.Is(err, ErrEditConflict) {
		t.Errorf("err = %v; want ErrEditConflict", err)
	}

	if err := m.MenuItems.SetAvailability(ctx, r.ID, item.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := m.MenuItems.Get(ctx, r.ID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsAvailable || got.PriceCents != 5000 || got.Name != "Margherita" || got.Version != 3 {
		t.Errorf("item = %+v", got)
	}
}

func TestDeletingRestaurantDeletesMenu(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	r := newRestaurant(t, m, "pizza")
	c := newCategory(t, m, r.ID, "Pizzas", 0)
	newMenuItem(t, m, r.ID, c.ID, "Margherita", 0)

	if err := m.Restaurants.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	err := m.Restaurants.DB.QueryRow(
		"SELECT (SELECT COUNT(*) FROM menu_categories) + (SELECT COUNT(*) FROM menu_items)").Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d menu rows left after deleting the restaurant", n)
	}
}
