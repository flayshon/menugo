package main

import (
	"net/http"
)

// showMenuHandler returns the restaurant's whole menu, as staff see it:
// every category in order, each with its items in order, including hidden
// categories and sold-out items.
func (app *application) showMenuHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	categories, err := app.models.Categories.List(r.Context(), ms.RestaurantID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	items, err := app.models.MenuItems.List(r.Context(), ms.RestaurantID, 0)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	type menuCategory struct {
		categoryResponse
		Items []menuItemResponse `json:"items"`
	}

	itemsByCategory := make(map[int64][]menuItemResponse)
	for _, item := range items {
		itemsByCategory[item.CategoryID] = append(itemsByCategory[item.CategoryID], newMenuItemResponse(item))
	}

	menu := make([]menuCategory, 0, len(categories))
	for _, c := range categories {
		categoryItems := itemsByCategory[c.ID]
		if categoryItems == nil {
			categoryItems = []menuItemResponse{}
		}
		menu = append(menu, menuCategory{newCategoryResponse(c), categoryItems})
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"menu": envelope{"categories": menu}}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
