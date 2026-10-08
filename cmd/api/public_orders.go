package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

// publicOrderResponse is what a customer sees of their order. It leaves out
// the customer's contact details and address: a tracking link may be shared,
// so it must not reveal them.
type publicOrderResponse struct {
	Status           data.OrderStatus   `json:"status"`
	Fulfillment      data.Fulfillment   `json:"fulfillment"`
	Restaurant       publicOrderVenue   `json:"restaurant"`
	Items            []publicOrderItem  `json:"items"`
	SubtotalCents    int64              `json:"subtotal_cents"`
	DeliveryFeeCents int64              `json:"delivery_fee_cents"`
	TotalCents       int64              `json:"total_cents"`
	Currency         string             `json:"currency"`
	Notes            string             `json:"notes"`
	PlacedAt         time.Time          `json:"placed_at"`
	StatusHistory    []publicOrderEvent `json:"status_history"`
	CanCancel        bool               `json:"can_cancel"`
}

type publicOrderVenue struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type publicOrderItem struct {
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
	Notes          string `json:"notes"`
}

type publicOrderEvent struct {
	Status data.OrderStatus `json:"status"`
	Reason string           `json:"reason,omitempty"`
	At     time.Time        `json:"at"`
}

func newPublicOrderResponse(o *data.Order, r *data.Restaurant) publicOrderResponse {
	items := make([]publicOrderItem, 0, len(o.Items))
	for _, i := range o.Items {
		items = append(items, publicOrderItem{i.Name, i.Quantity, i.UnitPriceCents, i.LineTotalCents, i.Notes})
	}

	history := make([]publicOrderEvent, 0, len(o.History))
	for _, h := range o.History {
		history = append(history, publicOrderEvent{h.To, h.Reason, h.At})
	}

	return publicOrderResponse{
		Status:           o.Status,
		Fulfillment:      o.Fulfillment,
		Restaurant:       publicOrderVenue{r.Slug, r.Name, r.Phone},
		Items:            items,
		SubtotalCents:    o.SubtotalCents,
		DeliveryFeeCents: o.DeliveryFeeCents,
		TotalCents:       o.TotalCents,
		Currency:         o.Currency,
		Notes:            o.Notes,
		PlacedAt:         o.CreatedAt,
		StatusHistory:    history,
		CanCancel:        data.CanTransition(o.Fulfillment, o.Status, data.StatusCancelled, data.ActorCustomer),
	}
}

const notDeliverableMessage = "the restaurant does not deliver to this postal code"

// deliveryQuoteHandler tells a customer whether a restaurant delivers to a
// postal code (?postal_code=...), and for how much, before they order.
func (app *application) deliveryQuoteHandler(w http.ResponseWriter, r *http.Request) {
	restaurant := app.getPublishedRestaurant(w, r)
	if restaurant == nil {
		return
	}

	postalCode := data.NormalizePostalCode(r.URL.Query().Get("postal_code"))
	if postalCode == "" {
		app.failedValidationResponse(w, r, map[string]string{"postal_code": "must be provided"})
		return
	}

	zone, err := app.models.Zones.FindForPostalCode(r.Context(), restaurant.ID, postalCode)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrNotDeliverable):
			app.failedValidationResponse(w, r, map[string]string{"postal_code": notDeliverableMessage})
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	quote := struct {
		PostalCode    string `json:"postal_code"`
		FeeCents      int64  `json:"fee_cents"`
		MinOrderCents int64  `json:"min_order_cents"`
		Currency      string `json:"currency"`
	}{postalCode, zone.FeeCents, zone.MinOrderCents, restaurant.Currency}

	err = app.writeJSON(w, http.StatusOK, envelope{"delivery_quote": quote}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// createOrderHandler places an order with a published restaurant. Prices,
// the delivery fee and the total are always computed by the server.
func (app *application) createOrderHandler(w http.ResponseWriter, r *http.Request) {
	restaurant := app.getPublishedRestaurant(w, r)
	if restaurant == nil {
		return
	}

	var input struct {
		Fulfillment data.Fulfillment `json:"fulfillment"`
		Customer    struct {
			Name  string `json:"name"`
			Phone string `json:"phone"`
			Email string `json:"email"`
		} `json:"customer"`
		Address *struct {
			Line       string `json:"line"`
			Details    string `json:"details"`
			City       string `json:"city"`
			PostalCode string `json:"postal_code"`
		} `json:"address"`
		Items []struct {
			ItemID   int64  `json:"item_id"`
			Quantity int    `json:"quantity"`
			Notes    string `json:"notes"`
		} `json:"items"`
		Notes              string `json:"notes"`
		ExpectedTotalCents *int64 `json:"expected_total_cents"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	req := &data.OrderRequest{
		Fulfillment:        input.Fulfillment,
		CustomerName:       input.Customer.Name,
		CustomerPhone:      data.NormalizePhone(input.Customer.Phone),
		CustomerEmail:      input.Customer.Email,
		Notes:              input.Notes,
		ExpectedTotalCents: input.ExpectedTotalCents,
	}
	if input.Address != nil {
		req.Address = data.Address{
			Line:       input.Address.Line,
			Details:    input.Address.Details,
			City:       input.Address.City,
			PostalCode: data.NormalizePostalCode(input.Address.PostalCode),
		}
	}
	for _, item := range input.Items {
		req.Lines = append(req.Lines, data.OrderLine{MenuItemID: item.ItemID, Quantity: item.Quantity, Notes: item.Notes})
	}

	v := validator.New()
	if data.ValidateOrderRequest(v, req); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	order, token, err := app.models.Orders.Create(r.Context(), restaurant, req)
	if err != nil {
		var (
			unavailable *data.ItemUnavailableError
			belowMin    *data.BelowMinimumOrderError
			mismatch    *data.TotalMismatchError
		)
		switch {
		case errors.As(err, &unavailable):
			for i, line := range req.Lines {
				if line.MenuItemID == unavailable.MenuItemID {
					v.AddError(fmt.Sprintf("items[%d].item_id", i), "is not available")
				}
			}
			app.failedValidationResponse(w, r, v.Errors)
		case errors.Is(err, data.ErrNotDeliverable):
			v.AddError("address.postal_code", notDeliverableMessage)
			app.failedValidationResponse(w, r, v.Errors)
		case errors.As(err, &belowMin):
			v.AddError("items", fmt.Sprintf("the subtotal must be at least %d for delivery to this address", belowMin.MinOrderCents))
			app.failedValidationResponse(w, r, v.Errors)
		case errors.As(err, &mismatch):
			// Tell the client the real total so it can show it and ask
			// the customer to confirm.
			env := envelope{
				"error":       "the order total has changed; please review the order",
				"total_cents": mismatch.TotalCents,
			}
			if err := app.writeJSON(w, http.StatusConflict, env, nil); err != nil {
				app.serverErrorResponse(w, r, err)
			}
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	headers := make(http.Header)
	headers.Set("Location", "/v1/tracking/"+token)

	env := envelope{
		"order":          newPublicOrderResponse(order, restaurant),
		"tracking_token": token,
	}
	if err := app.writeJSON(w, http.StatusCreated, env, headers); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// getTrackedOrder loads the order and restaurant for the {token} path
// parameter. If it can't, it writes the error response and returns nils.
func (app *application) getTrackedOrder(w http.ResponseWriter, r *http.Request) (*data.Order, *data.Restaurant) {
	order, err := app.models.Orders.GetByTrackingToken(r.Context(), r.PathValue("token"))
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return nil, nil
	}

	app.contextGetRequestInfo(r).restaurantID = order.RestaurantID

	// A placed order stays trackable even if the restaurant has since been
	// unpublished.
	restaurant, err := app.models.Restaurants.Get(r.Context(), order.RestaurantID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return nil, nil
	}
	return order, restaurant
}

func (app *application) showTrackedOrderHandler(w http.ResponseWriter, r *http.Request) {
	order, restaurant := app.getTrackedOrder(w, r)
	if order == nil {
		return
	}

	err := app.writeJSON(w, http.StatusOK, envelope{"order": newPublicOrderResponse(order, restaurant)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// cancelTrackedOrderHandler lets a customer cancel their order while the
// restaurant hasn't confirmed it yet.
func (app *application) cancelTrackedOrderHandler(w http.ResponseWriter, r *http.Request) {
	order, restaurant := app.getTrackedOrder(w, r)
	if order == nil {
		return
	}

	err := app.models.Orders.Transition(r.Context(), order.RestaurantID, order.ID, data.StatusCancelled, data.Actor{Type: data.ActorCustomer}, "")
	if err != nil {
		var invalid *data.InvalidTransitionError
		switch {
		case errors.As(err, &invalid):
			app.conflictResponse(w, r, "the order can no longer be cancelled; please contact the restaurant")
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	order, err = app.models.Orders.Get(r.Context(), order.RestaurantID, order.ID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"order": newPublicOrderResponse(order, restaurant)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
