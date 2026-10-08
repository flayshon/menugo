package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

type categoryResponse struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SortOrder   int32     `json:"sort_order"`
	IsVisible   bool      `json:"is_visible"`
	Version     int32     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func newCategoryResponse(c *data.Category) categoryResponse {
	return categoryResponse{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		SortOrder:   c.SortOrder,
		IsVisible:   c.IsVisible,
		Version:     c.Version,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

func (app *application) createCategoryHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SortOrder   int32  `json:"sort_order"`
		IsVisible   *bool  `json:"is_visible"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	category := &data.Category{
		RestaurantID: ms.RestaurantID,
		Name:         input.Name,
		Description:  input.Description,
		SortOrder:    input.SortOrder,
		IsVisible:    true,
	}
	setIfPresent(&category.IsVisible, input.IsVisible)

	v := validator.New()
	if data.ValidateCategory(v, category); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err := app.models.Categories.Insert(r.Context(), category)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateCategoryName):
			v.AddError("name", "a category with this name already exists")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/restaurants/%d/menu/categories/%d", ms.RestaurantID, category.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"category": newCategoryResponse(category)}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) listCategoriesHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	categories, err := app.models.Categories.List(r.Context(), ms.RestaurantID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	resp := make([]categoryResponse, 0, len(categories))
	for _, c := range categories {
		resp = append(resp, newCategoryResponse(c))
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"categories": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// getCategory loads the category in the {categoryID} path parameter from
// the membership's restaurant. If it can't, it writes the error response and
// returns nil.
func (app *application) getCategory(w http.ResponseWriter, r *http.Request) *data.Category {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "categoryID")
	if err != nil {
		app.notFoundResponse(w, r)
		return nil
	}

	category, err := app.models.Categories.Get(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return nil
	}
	return category
}

func (app *application) showCategoryHandler(w http.ResponseWriter, r *http.Request) {
	category := app.getCategory(w, r)
	if category == nil {
		return
	}

	err := app.writeJSON(w, http.StatusOK, envelope{"category": newCategoryResponse(category)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) updateCategoryHandler(w http.ResponseWriter, r *http.Request) {
	category := app.getCategory(w, r)
	if category == nil {
		return
	}

	var input struct {
		Version     *int32  `json:"version"`
		Name        *string `json:"name"`
		Description *string `json:"description"`
		SortOrder   *int32  `json:"sort_order"`
		IsVisible   *bool   `json:"is_visible"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.Version != nil && *input.Version != category.Version {
		app.editConflictResponse(w, r)
		return
	}

	setIfPresent(&category.Name, input.Name)
	setIfPresent(&category.Description, input.Description)
	setIfPresent(&category.SortOrder, input.SortOrder)
	setIfPresent(&category.IsVisible, input.IsVisible)

	v := validator.New()
	if data.ValidateCategory(v, category); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err := app.models.Categories.Update(r.Context(), category)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateCategoryName):
			v.AddError("name", "a category with this name already exists")
			app.failedValidationResponse(w, r, v.Errors)
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"category": newCategoryResponse(category)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// deleteCategoryHandler deletes an empty category. Categories with items
// give a 409: delete or move the items first.
func (app *application) deleteCategoryHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "categoryID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.models.Categories.Delete(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		case errors.Is(err, data.ErrCategoryNotEmpty):
			app.conflictResponse(w, r, "the category still has items; delete or move them first")
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
