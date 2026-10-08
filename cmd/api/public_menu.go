package main

import (
	"errors"
	"net/http"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

// publicMenuMaxAge is how long browsers and proxies may cache a public menu,
// in seconds. Short, so price and sold-out changes show up quickly.
const publicMenuMaxAge = "60"

// The public menu has its own response types, so that only fields meant for
// customers are ever exposed: no internal IDs of the restaurant, versions,
// timestamps, sort orders or the restaurant's email.
type publicRestaurant struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Phone       string `json:"phone"`
	AddressLine string `json:"address_line"`
	City        string `json:"city"`
	PostalCode  string `json:"postal_code"`
	Currency    string `json:"currency"`
}

type publicMenuItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PriceCents  int64  `json:"price_cents"`
	ImageURL    string `json:"image_url"`
	IsAvailable bool   `json:"is_available"`
}

type publicCategory struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Items       []publicMenuItem `json:"items"`
}

// showPublicMenuHandler serves a published restaurant's menu to anyone, by
// slug. Hidden categories, and categories with no items, are left out. Sold
// out items are included with "is_available": false, so customers see them
// but can't order them.
func (app *application) showPublicMenuHandler(w http.ResponseWriter, r *http.Request) {
	restaurant := app.getPublishedRestaurant(w, r)
	if restaurant == nil {
		return
	}

	categories, err := app.models.Categories.List(r.Context(), restaurant.ID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	items, err := app.models.MenuItems.List(r.Context(), restaurant.ID, 0)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	itemsByCategory := make(map[int64][]publicMenuItem)
	for _, item := range items {
		itemsByCategory[item.CategoryID] = append(itemsByCategory[item.CategoryID], publicMenuItem{
			ID:          item.ID,
			Name:        item.Name,
			Description: item.Description,
			PriceCents:  item.PriceCents,
			ImageURL:    item.ImageURL,
			IsAvailable: item.IsAvailable,
		})
	}

	menu := []publicCategory{}
	for _, c := range categories {
		if !c.IsVisible || len(itemsByCategory[c.ID]) == 0 {
			continue
		}
		menu = append(menu, publicCategory{
			ID:          c.ID,
			Name:        c.Name,
			Description: c.Description,
			Items:       itemsByCategory[c.ID],
		})
	}

	env := envelope{
		"restaurant": publicRestaurant{
			Slug:        restaurant.Slug,
			Name:        restaurant.Name,
			Description: restaurant.Description,
			Phone:       restaurant.Phone,
			AddressLine: restaurant.AddressLine,
			City:        restaurant.City,
			PostalCode:  restaurant.PostalCode,
			Currency:    restaurant.Currency,
		},
		"categories": menu,
	}

	// The menu never depends on who's asking, so caches may keep it briefly.
	headers := make(http.Header)
	headers.Set("Cache-Control", "public, max-age="+publicMenuMaxAge)

	err = app.writeJSON(w, http.StatusOK, envelope{"menu": env}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// getPublishedRestaurant loads the published restaurant in the {slug} path
// parameter. If it can't, it writes a 404 (or 500) and returns nil.
func (app *application) getPublishedRestaurant(w http.ResponseWriter, r *http.Request) *data.Restaurant {
	slug := r.PathValue("slug")
	if !validator.MaxChars(slug, 63) || !validator.Matches(slug, validator.SlugRX) {
		app.notFoundResponse(w, r)
		return nil
	}

	restaurant, err := app.models.Restaurants.GetPublishedBySlug(r.Context(), slug)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return nil
	}

	app.contextGetRequestInfo(r).restaurantID = restaurant.ID
	return restaurant
}
