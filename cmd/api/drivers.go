package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

type driverResponse struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Vehicle   string    `json:"vehicle"`
	IsActive  bool      `json:"is_active"`
	Version   int32     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newDriverResponse(d *data.Driver) driverResponse {
	return driverResponse{d.ID, d.UserID, d.Email, d.Name, d.Phone, d.Vehicle, d.IsActive, d.Version, d.CreatedAt, d.UpdatedAt}
}

// createDriverHandler makes a registered user a driver of the restaurant:
// it gives them the driver role (if they aren't a member yet) and a driver
// profile, in one step.
func (app *application) createDriverHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var input struct {
		Email    string  `json:"email"`
		Name     *string `json:"name"`
		Phone    string  `json:"phone"`
		Vehicle  string  `json:"vehicle"`
		IsActive *bool   `json:"is_active"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()
	if data.ValidateEmail(v, input.Email); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	user, err := app.models.Users.GetByEmail(r.Context(), input.Email)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			v.AddError("email", "no user is registered with this email address")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	driver := &data.Driver{
		RestaurantID: ms.RestaurantID,
		UserID:       user.ID,
		Email:        user.Email,
		Name:         user.Name, // unless another name is given
		Phone:        data.NormalizePhone(input.Phone),
		Vehicle:      input.Vehicle,
		IsActive:     true,
	}
	setIfPresent(&driver.Name, input.Name)
	setIfPresent(&driver.IsActive, input.IsActive)

	if data.ValidateDriver(v, driver); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err = app.models.Drivers.Create(r.Context(), driver)
	if err != nil {
		var roleConflict *data.MemberRoleConflictError
		switch {
		case errors.Is(err, data.ErrDuplicateDriver):
			app.conflictResponse(w, r, "this user is already a driver of the restaurant")
		case errors.As(err, &roleConflict):
			v.AddError("email", roleConflict.Error())
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/restaurants/%d/drivers/%d", ms.RestaurantID, driver.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"driver": newDriverResponse(driver)}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// listDriversHandler lists the restaurant's drivers; ?active=true lists only
// those who can take deliveries.
func (app *application) listDriversHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	active := app.readString(r.URL.Query(), "active", "false")
	if !validator.PermittedValue(active, "true", "false") {
		app.failedValidationResponse(w, r, map[string]string{"active": "must be true or false"})
		return
	}

	drivers, err := app.models.Drivers.List(r.Context(), ms.RestaurantID, active == "true")
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	resp := make([]driverResponse, 0, len(drivers))
	for _, d := range drivers {
		resp = append(resp, newDriverResponse(d))
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"drivers": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// getDriver loads the driver in the {driverID} path parameter from the
// membership's restaurant. If it can't, it writes the error response and
// returns nil.
func (app *application) getDriver(w http.ResponseWriter, r *http.Request) *data.Driver {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "driverID")
	if err != nil {
		app.notFoundResponse(w, r)
		return nil
	}

	driver, err := app.models.Drivers.Get(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return nil
	}
	return driver
}

func (app *application) showDriverHandler(w http.ResponseWriter, r *http.Request) {
	driver := app.getDriver(w, r)
	if driver == nil {
		return
	}

	err := app.writeJSON(w, http.StatusOK, envelope{"driver": newDriverResponse(driver)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) updateDriverHandler(w http.ResponseWriter, r *http.Request) {
	driver := app.getDriver(w, r)
	if driver == nil {
		return
	}

	var input struct {
		Version  *int32  `json:"version"`
		Name     *string `json:"name"`
		Phone    *string `json:"phone"`
		Vehicle  *string `json:"vehicle"`
		IsActive *bool   `json:"is_active"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.Version != nil && *input.Version != driver.Version {
		app.editConflictResponse(w, r)
		return
	}

	setIfPresent(&driver.Name, input.Name)
	if input.Phone != nil {
		driver.Phone = data.NormalizePhone(*input.Phone)
	}
	setIfPresent(&driver.Vehicle, input.Vehicle)
	setIfPresent(&driver.IsActive, input.IsActive)

	v := validator.New()
	if data.ValidateDriver(v, driver); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err := app.models.Drivers.Update(r.Context(), driver)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"driver": newDriverResponse(driver)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

const driverBusyMessage = "the driver has deliveries in progress; reassign or finish them first"

// deleteDriverHandler removes a driver from the restaurant, including their
// driver role. To stop assigning someone temporarily, deactivate them.
func (app *application) deleteDriverHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "driverID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.models.Drivers.Delete(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		case errors.Is(err, data.ErrDriverBusy):
			app.conflictResponse(w, r, driverBusyMessage)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
