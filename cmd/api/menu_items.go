package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

type menuItemResponse struct {
	ID          int64     `json:"id"`
	CategoryID  int64     `json:"category_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PriceCents  int64     `json:"price_cents"`
	ImageURL    string    `json:"image_url"`
	IsAvailable bool      `json:"is_available"`
	SortOrder   int32     `json:"sort_order"`
	Version     int32     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func newMenuItemResponse(item *data.MenuItem) menuItemResponse {
	return menuItemResponse{
		ID:          item.ID,
		CategoryID:  item.CategoryID,
		Name:        item.Name,
		Description: item.Description,
		PriceCents:  item.PriceCents,
		ImageURL:    item.ImageURL,
		IsAvailable: item.IsAvailable,
		SortOrder:   item.SortOrder,
		Version:     item.Version,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}

const invalidCategoryMessage = "must be a category of this restaurant"

func (app *application) createMenuItemHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var input struct {
		CategoryID  int64  `json:"category_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		PriceCents  *int64 `json:"price_cents"`
		ImageURL    string `json:"image_url"`
		IsAvailable *bool  `json:"is_available"`
		SortOrder   int32  `json:"sort_order"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	item := &data.MenuItem{
		RestaurantID: ms.RestaurantID,
		CategoryID:   input.CategoryID,
		Name:         input.Name,
		Description:  input.Description,
		ImageURL:     input.ImageURL,
		IsAvailable:  true,
		SortOrder:    input.SortOrder,
	}
	setIfPresent(&item.PriceCents, input.PriceCents)
	setIfPresent(&item.IsAvailable, input.IsAvailable)

	v := validator.New()
	// A missing price must not silently become a free item.
	v.Check(input.PriceCents != nil, "price_cents", "must be provided")
	if data.ValidateMenuItem(v, item); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err := app.models.MenuItems.Insert(r.Context(), item)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrInvalidCategory):
			v.AddError("category_id", invalidCategoryMessage)
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/restaurants/%d/menu/items/%d", ms.RestaurantID, item.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"item": newMenuItemResponse(item)}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// listMenuItemsHandler lists the restaurant's items, optionally only those
// in one category (?category_id=N).
func (app *application) listMenuItemsHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var categoryID int64
	if s := r.URL.Query().Get("category_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 1 {
			app.failedValidationResponse(w, r, map[string]string{"category_id": "must be a positive integer"})
			return
		}
		categoryID = id
	}

	items, err := app.models.MenuItems.List(r.Context(), ms.RestaurantID, categoryID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	resp := make([]menuItemResponse, 0, len(items))
	for _, item := range items {
		resp = append(resp, newMenuItemResponse(item))
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"items": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// getMenuItem loads the item in the {itemID} path parameter from the
// membership's restaurant. If it can't, it writes the error response and
// returns nil.
func (app *application) getMenuItem(w http.ResponseWriter, r *http.Request) *data.MenuItem {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "itemID")
	if err != nil {
		app.notFoundResponse(w, r)
		return nil
	}

	item, err := app.models.MenuItems.Get(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return nil
	}
	return item
}

func (app *application) showMenuItemHandler(w http.ResponseWriter, r *http.Request) {
	item := app.getMenuItem(w, r)
	if item == nil {
		return
	}

	err := app.writeJSON(w, http.StatusOK, envelope{"item": newMenuItemResponse(item)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) updateMenuItemHandler(w http.ResponseWriter, r *http.Request) {
	item := app.getMenuItem(w, r)
	if item == nil {
		return
	}

	var input struct {
		Version     *int32  `json:"version"`
		CategoryID  *int64  `json:"category_id"`
		Name        *string `json:"name"`
		Description *string `json:"description"`
		PriceCents  *int64  `json:"price_cents"`
		ImageURL    *string `json:"image_url"`
		IsAvailable *bool   `json:"is_available"`
		SortOrder   *int32  `json:"sort_order"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.Version != nil && *input.Version != item.Version {
		app.editConflictResponse(w, r)
		return
	}

	setIfPresent(&item.CategoryID, input.CategoryID)
	setIfPresent(&item.Name, input.Name)
	setIfPresent(&item.Description, input.Description)
	setIfPresent(&item.PriceCents, input.PriceCents)
	setIfPresent(&item.ImageURL, input.ImageURL)
	setIfPresent(&item.IsAvailable, input.IsAvailable)
	setIfPresent(&item.SortOrder, input.SortOrder)

	v := validator.New()
	if data.ValidateMenuItem(v, item); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err := app.models.MenuItems.Update(r.Context(), item)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrInvalidCategory):
			v.AddError("category_id", invalidCategoryMessage)
			app.failedValidationResponse(w, r, v.Errors)
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"item": newMenuItemResponse(item)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// setMenuItemAvailabilityHandler marks an item as available or sold out.
// Unlike a full update, staff may do this.
func (app *application) setMenuItemAvailabilityHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "itemID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	var input struct {
		IsAvailable *bool `json:"is_available"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.IsAvailable == nil {
		app.failedValidationResponse(w, r, map[string]string{"is_available": "must be provided"})
		return
	}

	err = app.models.MenuItems.SetAvailability(r.Context(), ms.RestaurantID, id, *input.IsAvailable)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	item, err := app.models.MenuItems.Get(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"item": newMenuItemResponse(item)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) deleteMenuItemHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "itemID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.models.MenuItems.Delete(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
