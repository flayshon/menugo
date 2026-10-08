package main

import (
	"errors"
	"net/http"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

type deliveryResponse struct {
	ID               int64               `json:"id"`
	Status           data.DeliveryStatus `json:"status"`
	Driver           *deliveryDriver     `json:"driver"` // null if the driver has since been removed
	AssignedByUserID *int64              `json:"assigned_by_user_id"`
	AssignedAt       time.Time           `json:"assigned_at"`
	PickedUpAt       *time.Time          `json:"picked_up_at"`
	DeliveredAt      *time.Time          `json:"delivered_at"`
	EndedAt          *time.Time          `json:"ended_at"`
}

type deliveryDriver struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Phone   string `json:"phone"`
	Vehicle string `json:"vehicle"`
}

func newDeliveryResponse(dl *data.Delivery) *deliveryResponse {
	if dl == nil {
		return nil
	}
	resp := &deliveryResponse{
		ID:               dl.ID,
		Status:           dl.Status,
		AssignedByUserID: nonZero(dl.AssignedByUserID),
		AssignedAt:       dl.AssignedAt,
		PickedUpAt:       nonZero(dl.PickedUpAt),
		DeliveredAt:      nonZero(dl.DeliveredAt),
		EndedAt:          nonZero(dl.EndedAt),
	}
	if dl.DriverID != 0 {
		resp.Driver = &deliveryDriver{dl.DriverID, dl.DriverName, dl.DriverPhone, dl.DriverVehicle}
	}
	return resp
}

// assignDriverHandler assigns a driver to a delivery order, replacing any
// driver it had: PUT {"driver_id": 3}.
func (app *application) assignDriverHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	orderID, err := app.readIDParam(r, "orderID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	var input struct {
		DriverID int64 `json:"driver_id"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()
	if v.Check(input.DriverID > 0, "driver_id", "must be provided"); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	delivery, err := app.models.Deliveries.Assign(r.Context(), ms.RestaurantID, orderID, input.DriverID, ms.UserID)
	if err != nil {
		var notAssignable *data.NotAssignableError
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		case errors.As(err, &notAssignable):
			app.conflictResponse(w, r, notAssignable.Error())
		case errors.Is(err, data.ErrDriverUnavailable):
			v.AddError("driver_id", "must be an active driver of this restaurant")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"delivery": newDeliveryResponse(delivery)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// unassignDriverHandler takes the driver off an order.
func (app *application) unassignDriverHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	orderID, err := app.readIDParam(r, "orderID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.models.Deliveries.Unassign(r.Context(), ms.RestaurantID, orderID)
	if err != nil {
		var notAssignable *data.NotAssignableError
		switch {
		case errors.Is(err, data.ErrRecordNotFound), errors.Is(err, data.ErrNoActiveDelivery):
			app.notFoundResponse(w, r)
		case errors.As(err, &notAssignable):
			app.conflictResponse(w, r, notAssignable.Error())
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// driverDeliveryResponse is a delivery as the driver sees it. The customer's
// contact details and address are only included while the delivery is in
// progress: a driver has no need for them before or after.
type driverDeliveryResponse struct {
	ID          int64               `json:"id"`
	Status      data.DeliveryStatus `json:"status"`
	AssignedAt  time.Time           `json:"assigned_at"`
	PickedUpAt  *time.Time          `json:"picked_up_at"`
	DeliveredAt *time.Time          `json:"delivered_at"`
	EndedAt     *time.Time          `json:"ended_at"`
	Restaurant  struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Phone       string `json:"phone"`
		AddressLine string `json:"address_line"`
		City        string `json:"city"`
	} `json:"restaurant"`
	Order struct {
		ID       int64            `json:"id"`
		Status   data.OrderStatus `json:"status"`
		Customer *struct {
			Name  string `json:"name"`
			Phone string `json:"phone"`
		} `json:"customer"`
		Address    *orderAddress     `json:"address"`
		Items      []driverOrderItem `json:"items"`
		TotalCents int64             `json:"total_cents"`
		Currency   string            `json:"currency"`
		Notes      string            `json:"notes"`
	} `json:"order"`
}

type driverOrderItem struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
	Notes    string `json:"notes"`
}

func newDriverDeliveryResponse(dd *data.DriverDelivery) driverDeliveryResponse {
	var resp driverDeliveryResponse
	resp.ID = dd.ID
	resp.Status = dd.Status
	resp.AssignedAt = dd.AssignedAt
	resp.PickedUpAt = nonZero(dd.PickedUpAt)
	resp.DeliveredAt = nonZero(dd.DeliveredAt)
	resp.EndedAt = nonZero(dd.EndedAt)

	resp.Restaurant.ID = dd.Restaurant.ID
	resp.Restaurant.Name = dd.Restaurant.Name
	resp.Restaurant.Phone = dd.Restaurant.Phone
	resp.Restaurant.AddressLine = dd.Restaurant.AddressLine
	resp.Restaurant.City = dd.Restaurant.City

	o := dd.Order
	resp.Order.ID = o.ID
	resp.Order.Status = o.Status
	resp.Order.TotalCents = o.TotalCents
	resp.Order.Currency = o.Currency
	resp.Order.Notes = o.Notes
	resp.Order.Items = make([]driverOrderItem, 0, len(o.Items))
	for _, i := range o.Items {
		resp.Order.Items = append(resp.Order.Items, driverOrderItem{i.Name, i.Quantity, i.Notes})
	}

	if dd.Status == data.DeliveryAssigned || dd.Status == data.DeliveryPickedUp {
		resp.Order.Customer = &struct {
			Name  string `json:"name"`
			Phone string `json:"phone"`
		}{o.CustomerName, o.CustomerPhone}
		a := o.Address
		resp.Order.Address = &orderAddress{a.Line, a.Details, a.City, a.PostalCode}
	}

	return resp
}

// listMyDeliveriesHandler lists the deliveries assigned to the authenticated
// user, across every restaurant they drive for. ?status=assigned,picked_up
// lists only those in progress.
func (app *application) listMyDeliveriesHandler(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)

	qs := r.URL.Query()
	v := validator.New()

	var statuses []data.DeliveryStatus
	for _, s := range app.readCSV(qs, "status") {
		status := data.DeliveryStatus(s)
		v.Check(status.Valid(), "status", "must be one of assigned, picked_up, delivered, unassigned, cancelled")
		statuses = append(statuses, status)
	}
	p := data.Pagination{
		Page:     app.readInt(qs, "page", 1, v),
		PageSize: app.readInt(qs, "page_size", 20, v),
	}
	if data.ValidatePagination(v, p); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	deliveries, metadata, err := app.models.Deliveries.ListForDriver(r.Context(), user.ID, statuses, p)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	resp := make([]driverDeliveryResponse, 0, len(deliveries))
	for _, dd := range deliveries {
		resp = append(resp, newDriverDeliveryResponse(dd))
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"deliveries": resp, "metadata": newMetadataResponse(metadata)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// getMyDelivery loads the delivery in the {deliveryID} path parameter if it
// is assigned to the authenticated user. If it can't, it writes the error
// response and returns nil.
func (app *application) getMyDelivery(w http.ResponseWriter, r *http.Request, id int64) *data.DriverDelivery {
	user := app.contextGetUser(r)

	dd, err := app.models.Deliveries.GetForDriver(r.Context(), user.ID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return nil
	}

	app.contextGetRequestInfo(r).restaurantID = dd.RestaurantID
	return dd
}

func (app *application) showMyDeliveryHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r, "deliveryID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	dd := app.getMyDelivery(w, r, id)
	if dd == nil {
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"delivery": newDriverDeliveryResponse(dd)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// updateMyDeliveryHandler lets a driver report progress on their delivery:
// {"status": "picked_up"} when they leave with the order, {"status":
// "delivered"} when it's handed over. The order's status follows.
func (app *application) updateMyDeliveryHandler(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)

	id, err := app.readIDParam(r, "deliveryID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	var input struct {
		Status data.DeliveryStatus `json:"status"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if !data.DriverCanSet(input.Status) {
		app.failedValidationResponse(w, r, map[string]string{"status": "must be picked_up or delivered"})
		return
	}

	err = app.models.Deliveries.UpdateByDriver(r.Context(), user.ID, id, input.Status)
	if err != nil {
		var invalid *data.InvalidTransitionError
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		case errors.Is(err, data.ErrNoActiveDelivery):
			app.conflictResponse(w, r, "this delivery is no longer in progress")
		case errors.As(err, &invalid):
			app.conflictResponse(w, r, invalid.Error())
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	dd := app.getMyDelivery(w, r, id)
	if dd == nil {
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"delivery": newDriverDeliveryResponse(dd)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
