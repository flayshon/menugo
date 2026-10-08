package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

type restaurantResponse struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Phone       string    `json:"phone"`
	Email       string    `json:"email"`
	AddressLine string    `json:"address_line"`
	City        string    `json:"city"`
	PostalCode  string    `json:"postal_code"`
	Currency    string    `json:"currency"`
	IsPublished bool      `json:"is_published"`
	Version     int32     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func newRestaurantResponse(r *data.Restaurant) restaurantResponse {
	return restaurantResponse{
		ID:          r.ID,
		Slug:        r.Slug,
		Name:        r.Name,
		Description: r.Description,
		Phone:       r.Phone,
		Email:       r.Email,
		AddressLine: r.AddressLine,
		City:        r.City,
		PostalCode:  r.PostalCode,
		Currency:    r.Currency,
		IsPublished: r.IsPublished,
		Version:     r.Version,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// createRestaurantHandler creates a restaurant owned by the caller.
func (app *application) createRestaurantHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Phone       string `json:"phone"`
		Email       string `json:"email"`
		AddressLine string `json:"address_line"`
		City        string `json:"city"`
		PostalCode  string `json:"postal_code"`
		Currency    string `json:"currency"`
		IsPublished bool   `json:"is_published"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	restaurant := &data.Restaurant{
		Slug:        input.Slug,
		Name:        input.Name,
		Description: input.Description,
		Phone:       input.Phone,
		Email:       input.Email,
		AddressLine: input.AddressLine,
		City:        input.City,
		PostalCode:  input.PostalCode,
		Currency:    input.Currency,
		IsPublished: input.IsPublished,
	}

	v := validator.New()
	if data.ValidateRestaurant(v, restaurant); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	user := app.contextGetUser(r)

	err := app.models.Restaurants.InsertWithOwner(r.Context(), restaurant, user.ID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateSlug):
			v.AddError("slug", "is already in use")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/restaurants/%d", restaurant.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"restaurant": newRestaurantResponse(restaurant)}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// listRestaurantsHandler lists the restaurants the caller is a member of.
func (app *application) listRestaurantsHandler(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)

	restaurants, err := app.models.Restaurants.ListForUser(r.Context(), user.ID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	type restaurantWithRole struct {
		restaurantResponse
		Role data.Role `json:"role"`
	}

	resp := make([]restaurantWithRole, 0, len(restaurants))
	for _, rest := range restaurants {
		resp = append(resp, restaurantWithRole{newRestaurantResponse(&rest.Restaurant), rest.Role})
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"restaurants": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) showRestaurantHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	restaurant, err := app.models.Restaurants.Get(r.Context(), ms.RestaurantID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"restaurant": newRestaurantResponse(restaurant)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// updateRestaurantHandler applies a partial update. Fields left out of the
// body keep their values. If the body includes "version", the update is
// rejected with 409 unless it matches the current version, so clients can
// avoid overwriting changes they haven't seen.
func (app *application) updateRestaurantHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	restaurant, err := app.models.Restaurants.Get(r.Context(), ms.RestaurantID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	var input struct {
		Version     *int32  `json:"version"`
		Slug        *string `json:"slug"`
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Phone       *string `json:"phone"`
		Email       *string `json:"email"`
		AddressLine *string `json:"address_line"`
		City        *string `json:"city"`
		PostalCode  *string `json:"postal_code"`
		Currency    *string `json:"currency"`
		IsPublished *bool   `json:"is_published"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.Version != nil && *input.Version != restaurant.Version {
		app.editConflictResponse(w, r)
		return
	}

	setIfPresent(&restaurant.Slug, input.Slug)
	setIfPresent(&restaurant.Name, input.Name)
	setIfPresent(&restaurant.Description, input.Description)
	setIfPresent(&restaurant.Phone, input.Phone)
	setIfPresent(&restaurant.Email, input.Email)
	setIfPresent(&restaurant.AddressLine, input.AddressLine)
	setIfPresent(&restaurant.City, input.City)
	setIfPresent(&restaurant.PostalCode, input.PostalCode)
	setIfPresent(&restaurant.Currency, input.Currency)
	setIfPresent(&restaurant.IsPublished, input.IsPublished)

	v := validator.New()
	if data.ValidateRestaurant(v, restaurant); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err = app.models.Restaurants.Update(r.Context(), restaurant)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateSlug):
			v.AddError("slug", "is already in use")
			app.failedValidationResponse(w, r, v.Errors)
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"restaurant": newRestaurantResponse(restaurant)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// deleteRestaurantHandler deletes the restaurant: it disappears for its
// members and customers, but its orders are kept (see
// data.RestaurantModel.Delete).
func (app *application) deleteRestaurantHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	err := app.models.Restaurants.Delete(r.Context(), ms.RestaurantID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		case errors.Is(err, data.ErrRestaurantBusy):
			app.conflictResponse(w, r, "the restaurant has orders in progress; finish or cancel them first")
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// setIfPresent sets *dst to *src if src is not nil. It is used for partial
// updates, where a nil pointer means "field not sent".
func setIfPresent[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}
