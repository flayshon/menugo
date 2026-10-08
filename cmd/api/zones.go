package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

type zoneResponse struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	FeeCents      int64     `json:"fee_cents"`
	MinOrderCents int64     `json:"min_order_cents"`
	IsActive      bool      `json:"is_active"`
	PostalCodes   []string  `json:"postal_codes"`
	Version       int32     `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func newZoneResponse(z *data.DeliveryZone) zoneResponse {
	codes := z.PostalCodes
	if codes == nil {
		codes = []string{}
	}
	return zoneResponse{
		ID:            z.ID,
		Name:          z.Name,
		FeeCents:      z.FeeCents,
		MinOrderCents: z.MinOrderCents,
		IsActive:      z.IsActive,
		PostalCodes:   codes,
		Version:       z.Version,
		CreatedAt:     z.CreatedAt,
		UpdatedAt:     z.UpdatedAt,
	}
}

// zoneSaveError writes the response for an error from inserting or updating
// a zone.
func (app *application) zoneSaveError(w http.ResponseWriter, r *http.Request, v *validator.Validator, err error) {
	var inUse *data.PostalCodeInUseError
	switch {
	case errors.Is(err, data.ErrDuplicateZoneName):
		v.AddError("name", "a delivery zone with this name already exists")
		app.failedValidationResponse(w, r, v.Errors)
	case errors.As(err, &inUse):
		v.AddError("postal_codes", inUse.Error())
		app.failedValidationResponse(w, r, v.Errors)
	case errors.Is(err, data.ErrEditConflict):
		app.editConflictResponse(w, r)
	default:
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) createZoneHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var input struct {
		Name          string   `json:"name"`
		FeeCents      *int64   `json:"fee_cents"`
		MinOrderCents int64    `json:"min_order_cents"`
		IsActive      *bool    `json:"is_active"`
		PostalCodes   []string `json:"postal_codes"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	zone := &data.DeliveryZone{
		RestaurantID:  ms.RestaurantID,
		Name:          input.Name,
		MinOrderCents: input.MinOrderCents,
		IsActive:      true,
	}
	setIfPresent(&zone.FeeCents, input.FeeCents)
	setIfPresent(&zone.IsActive, input.IsActive)
	zone.SetPostalCodes(input.PostalCodes)

	v := validator.New()
	// A missing fee must not silently become free delivery.
	v.Check(input.FeeCents != nil, "fee_cents", "must be provided")
	if data.ValidateDeliveryZone(v, zone); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	if err := app.models.Zones.Insert(r.Context(), zone); err != nil {
		app.zoneSaveError(w, r, v, err)
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/restaurants/%d/delivery-zones/%d", ms.RestaurantID, zone.ID))

	err := app.writeJSON(w, http.StatusCreated, envelope{"delivery_zone": newZoneResponse(zone)}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) listZonesHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	zones, err := app.models.Zones.List(r.Context(), ms.RestaurantID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	resp := make([]zoneResponse, 0, len(zones))
	for _, z := range zones {
		resp = append(resp, newZoneResponse(z))
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"delivery_zones": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// getZone loads the zone in the {zoneID} path parameter from the
// membership's restaurant. If it can't, it writes the error response and
// returns nil.
func (app *application) getZone(w http.ResponseWriter, r *http.Request) *data.DeliveryZone {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "zoneID")
	if err != nil {
		app.notFoundResponse(w, r)
		return nil
	}

	zone, err := app.models.Zones.Get(r.Context(), ms.RestaurantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return nil
	}
	return zone
}

func (app *application) showZoneHandler(w http.ResponseWriter, r *http.Request) {
	zone := app.getZone(w, r)
	if zone == nil {
		return
	}

	err := app.writeJSON(w, http.StatusOK, envelope{"delivery_zone": newZoneResponse(zone)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// updateZoneHandler applies a partial update. If postal_codes is sent, it
// replaces the whole list.
func (app *application) updateZoneHandler(w http.ResponseWriter, r *http.Request) {
	zone := app.getZone(w, r)
	if zone == nil {
		return
	}

	var input struct {
		Version       *int32   `json:"version"`
		Name          *string  `json:"name"`
		FeeCents      *int64   `json:"fee_cents"`
		MinOrderCents *int64   `json:"min_order_cents"`
		IsActive      *bool    `json:"is_active"`
		PostalCodes   []string `json:"postal_codes"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.Version != nil && *input.Version != zone.Version {
		app.editConflictResponse(w, r)
		return
	}

	setIfPresent(&zone.Name, input.Name)
	setIfPresent(&zone.FeeCents, input.FeeCents)
	setIfPresent(&zone.MinOrderCents, input.MinOrderCents)
	setIfPresent(&zone.IsActive, input.IsActive)
	if input.PostalCodes != nil {
		zone.SetPostalCodes(input.PostalCodes)
	}

	v := validator.New()
	if data.ValidateDeliveryZone(v, zone); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	if err := app.models.Zones.Update(r.Context(), zone); err != nil {
		app.zoneSaveError(w, r, v, err)
		return
	}

	err := app.writeJSON(w, http.StatusOK, envelope{"delivery_zone": newZoneResponse(zone)}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) deleteZoneHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	id, err := app.readIDParam(r, "zoneID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.models.Zones.Delete(r.Context(), ms.RestaurantID, id)
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
