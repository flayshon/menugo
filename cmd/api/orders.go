package main

import (
	"errors"
	"net/http"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

// orderResponse is the restaurant's view of an order, with everything needed
// to prepare and deliver it.
type orderResponse struct {
	ID               int64                `json:"id"`
	Status           data.OrderStatus     `json:"status"`
	Fulfillment      data.Fulfillment     `json:"fulfillment"`
	Customer         orderCustomer        `json:"customer"`
	Address          *orderAddress        `json:"address"` // null for pickup
	DeliveryZoneID   *int64               `json:"delivery_zone_id"`
	Items            []orderItemResponse  `json:"items"`
	SubtotalCents    int64                `json:"subtotal_cents"`
	DeliveryFeeCents int64                `json:"delivery_fee_cents"`
	TotalCents       int64                `json:"total_cents"`
	Currency         string               `json:"currency"`
	Notes            string               `json:"notes"`
	StatusHistory    []orderEventResponse `json:"status_history,omitempty"` // only on single orders
	NextStatuses     []data.OrderStatus   `json:"next_statuses"`
	Version          int32                `json:"version"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

type orderCustomer struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Email string `json:"email"`
}

type orderAddress struct {
	Line       string `json:"line"`
	Details    string `json:"details"`
	City       string `json:"city"`
	PostalCode string `json:"postal_code"`
}

type orderItemResponse struct {
	ID             int64  `json:"id"`
	MenuItemID     *int64 `json:"menu_item_id"` // null if since deleted from the menu
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
	Notes          string `json:"notes"`
}

type orderEventResponse struct {
	From        *data.OrderStatus `json:"from"` // null when the order was placed
	To          data.OrderStatus  `json:"to"`
	ActorType   data.ActorType    `json:"actor_type"`
	ActorUserID *int64            `json:"actor_user_id"`
	Reason      string            `json:"reason"`
	At          time.Time         `json:"at"`
}

// nonZero returns a pointer to v, or nil if v is the zero value, for fields
// that are null in JSON when absent.
func nonZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

func newOrderResponse(o *data.Order) orderResponse {
	resp := orderResponse{
		ID:               o.ID,
		Status:           o.Status,
		Fulfillment:      o.Fulfillment,
		Customer:         orderCustomer{o.CustomerID, o.CustomerName, o.CustomerPhone, o.CustomerEmail},
		DeliveryZoneID:   nonZero(o.DeliveryZoneID),
		Items:            make([]orderItemResponse, 0, len(o.Items)),
		SubtotalCents:    o.SubtotalCents,
		DeliveryFeeCents: o.DeliveryFeeCents,
		TotalCents:       o.TotalCents,
		Currency:         o.Currency,
		Notes:            o.Notes,
		NextStatuses:     data.NextStatuses(o.Fulfillment, o.Status),
		Version:          o.Version,
		CreatedAt:        o.CreatedAt,
		UpdatedAt:        o.UpdatedAt,
	}
	if resp.NextStatuses == nil {
		resp.NextStatuses = []data.OrderStatus{}
	}

	if o.Fulfillment == data.FulfillmentDelivery {
		a := o.Address
		resp.Address = &orderAddress{a.Line, a.Details, a.City, a.PostalCode}
	}

	for _, i := range o.Items {
		resp.Items = append(resp.Items, orderItemResponse{
			ID:             i.ID,
			MenuItemID:     nonZero(i.MenuItemID),
			Name:           i.Name,
			Quantity:       i.Quantity,
			UnitPriceCents: i.UnitPriceCents,
			LineTotalCents: i.LineTotalCents,
			Notes:          i.Notes,
		})
	}

	for _, h := range o.History {
		resp.StatusHistory = append(resp.StatusHistory, orderEventResponse{
			From:        nonZero(h.From),
			To:          h.To,
			ActorType:   h.ActorType,
			ActorUserID: nonZero(h.ActorUserID),
			Reason:      h.Reason,
			At:          h.At,
		})
	}

	return resp
}

// listOrdersHandler lists the restaurant's orders. Query parameters:
//
//	status=pending,confirmed   only these statuses
//	fulfillment=delivery       only delivery (or pickup) orders
//	from, to                   placed in [from, to), RFC 3339
//	sort=created_at            oldest first (default -created_at, newest first)
//	page, page_size            pagination (default 1 and 20, max page_size 100)
func (app *application) listOrdersHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	qs := r.URL.Query()
	v := validator.New()

	var f data.OrderFilters
	for _, s := range app.readCSV(qs, "status") {
		f.Statuses = append(f.Statuses, data.OrderStatus(s))
	}
	f.Fulfillment = data.Fulfillment(app.readString(qs, "fulfillment", ""))
	f.From = app.readTime(qs, "from", v)
	f.To = app.readTime(qs, "to", v)
	f.Page = app.readInt(qs, "page", 1, v)
	f.PageSize = app.readInt(qs, "page_size", 20, v)

	sort := app.readString(qs, "sort", "-created_at")
	v.Check(validator.PermittedValue(sort, "created_at", "-created_at"), "sort", "must be created_at or -created_at")
	f.OldestFirst = sort == "created_at"

	if data.ValidateOrderFilters(v, f); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	orders, metadata, err := app.models.Orders.List(r.Context(), ms.RestaurantID, f)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	resp := make([]orderResponse, 0, len(orders))
	for _, o := range orders {
		resp = append(resp, newOrderResponse(o))
	}

	meta := struct {
		CurrentPage  int `json:"current_page,omitempty"`
		PageSize     int `json:"page_size,omitempty"`
		FirstPage    int `json:"first_page,omitempty"`
		LastPage     int `json:"last_page,omitempty"`
		TotalRecords int `json:"total_records"`
	}{metadata.CurrentPage, metadata.PageSize, metadata.FirstPage, metadata.LastPage, metadata.TotalRecords}

	err = app.writeJSON(w, http.StatusOK, envelope{"orders": resp, "metadata": meta}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// getOrder loads the order in the {orderID} path parameter from the
// membership's restaurant. If it can't, it writes the error response and
// returns nil.
func (app *application) getOrder(w http.ResponseWriter, r *http.Request) *data.Order {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "orderID")
	if err != nil {
		app.notFoundResponse(w, r)
		return nil
	}

	order, err := app.models.Orders.Get(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return nil
	}
	return order
}

func (app *application) showOrderHandler(w http.ResponseWriter, r *http.Request) {
	order := app.getOrder(w, r)
	if order == nil {
		return
	}

	err := app.writeJSON(w, http.StatusOK, envelope{"order": newOrderResponse(order)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// updateOrderHandler changes an order's status: {"status": "confirmed"}, or
// {"status": "cancelled", "reason": "..."}. Only moves allowed by the state
// machine succeed; anything else is a 409. The status is the only field of an
// order that can change.
func (app *application) updateOrderHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "orderID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	var input struct {
		Status data.OrderStatus `json:"status"`
		Reason string           `json:"reason"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()
	if data.ValidateStatusChange(v, input.Status, input.Reason); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	actor := data.Actor{Type: data.ActorUser, UserID: ms.UserID}

	err = app.models.Orders.Transition(r.Context(), ms.RestaurantID, id, input.Status, actor, input.Reason)
	if err != nil {
		var invalid *data.InvalidTransitionError
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		case errors.As(err, &invalid):
			app.conflictResponse(w, r, invalid.Error())
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	order, err := app.models.Orders.Get(r.Context(), ms.RestaurantID, id)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"order": newOrderResponse(order)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
