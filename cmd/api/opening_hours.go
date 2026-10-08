package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

type openingIntervalJSON struct {
	Day      string `json:"day"`
	OpensAt  string `json:"opens_at"`
	ClosesAt string `json:"closes_at"`
}

func newOpeningHoursJSON(hours []data.OpeningInterval) []openingIntervalJSON {
	resp := make([]openingIntervalJSON, 0, len(hours))
	for _, h := range hours {
		resp = append(resp, openingIntervalJSON{
			Day:      strings.ToLower(h.Day.String()),
			OpensAt:  data.FormatClock(h.Opens),
			ClosesAt: data.FormatClock(h.Closes),
		})
	}
	return resp
}

// openingHoursResponse is a schedule plus whether the restaurant takes
// orders right now.
type openingHoursResponse struct {
	Timezone        string                `json:"timezone"`
	AcceptingOrders bool                  `json:"accepting_orders"`
	OpeningHours    []openingIntervalJSON `json:"opening_hours"`
	IsOpen          bool                  `json:"is_open"`
}

func newOpeningHoursResponse(r *data.Restaurant, hours []data.OpeningInterval) openingHoursResponse {
	return openingHoursResponse{
		Timezone:        r.Timezone,
		AcceptingOrders: r.AcceptingOrders,
		OpeningHours:    newOpeningHoursJSON(hours),
		IsOpen:          r.AcceptingOrders && data.IsOpen(hours, r.Location(), time.Now()),
	}
}

func (app *application) writeOpeningHours(w http.ResponseWriter, r *http.Request, restaurantID int64) {
	restaurant, err := app.models.Restaurants.Get(r.Context(), restaurantID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}
	hours, err := app.models.OpeningHours.Get(r.Context(), restaurantID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"opening_hours": newOpeningHoursResponse(restaurant, hours)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) showOpeningHoursHandler(w http.ResponseWriter, r *http.Request) {
	app.writeOpeningHours(w, r, app.contextGetMembership(r).RestaurantID)
}

// updateOpeningHoursHandler replaces the weekly schedule:
//
//	{"opening_hours": [{"day": "friday", "opens_at": "18:00", "closes_at": "02:00"}]}
//
// An empty list means always open.
func (app *application) updateOpeningHoursHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var input struct {
		OpeningHours *[]openingIntervalJSON `json:"opening_hours"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()
	if v.Check(input.OpeningHours != nil, "opening_hours", "must be provided"); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	hours := make([]data.OpeningInterval, 0, len(*input.OpeningHours))
	for i, in := range *input.OpeningHours {
		key := fmt.Sprintf("opening_hours[%d]", i)
		day, ok := data.ParseWeekday(in.Day)
		v.Check(ok, key+".day", "must be a lowercase day name, e.g. monday")
		opens, ok := data.ParseClock(in.OpensAt)
		v.Check(ok, key+".opens_at", "must be a time like 09:30")
		closes, ok := data.ParseClock(in.ClosesAt)
		v.Check(ok, key+".closes_at", "must be a time like 22:00")
		hours = append(hours, data.OpeningInterval{Day: day, Opens: opens, Closes: closes})
	}
	if data.ValidateOpeningHours(v, hours); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	if err := app.models.OpeningHours.Replace(r.Context(), ms.RestaurantID, hours); err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	app.writeOpeningHours(w, r, ms.RestaurantID)
}

// setAcceptingOrdersHandler pauses or resumes ordering:
// {"accepting_orders": false}. Staff may do this, like marking items sold out.
func (app *application) setAcceptingOrdersHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var input struct {
		AcceptingOrders *bool `json:"accepting_orders"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.AcceptingOrders == nil {
		app.failedValidationResponse(w, r, map[string]string{"accepting_orders": "must be provided"})
		return
	}

	err := app.models.Restaurants.SetAcceptingOrders(r.Context(), ms.RestaurantID, *input.AcceptingOrders)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	app.writeOpeningHours(w, r, ms.RestaurantID)
}
