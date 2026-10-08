package main

import (
	"errors"
	"net/http"
	"time"

	"menugo.flayshon.com/internal/data"
	"menugo.flayshon.com/internal/validator"
)

type memberResponse struct {
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      data.Role `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (app *application) listMembersHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	members, err := app.models.Memberships.ListMembers(r.Context(), ms.RestaurantID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	resp := make([]memberResponse, 0, len(members))
	for _, m := range members {
		resp = append(resp, memberResponse{m.UserID, m.Name, m.Email, m.Role, m.CreatedAt})
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"members": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// addMemberHandler gives an existing user a role in the restaurant. Callers
// can only grant roles below their own (see data.Role.CanManage).
func (app *application) addMemberHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	var input struct {
		Email string    `json:"email"`
		Role  data.Role `json:"role"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()
	data.ValidateEmail(v, input.Email)
	v.Check(input.Role != "", "role", "must be provided")
	v.Check(input.Role.Valid(), "role", "must be one of restaurant_owner, restaurant_admin, restaurant_staff, driver")
	if !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	if !ms.Role.CanManage(input.Role) {
		app.notPermittedResponse(w, r)
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

	member := &data.Membership{
		RestaurantID: ms.RestaurantID,
		UserID:       user.ID,
		Role:         input.Role,
	}

	err = app.models.Memberships.Insert(r.Context(), member)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateMembership):
			app.conflictResponse(w, r, "this user is already a member of the restaurant")
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	resp := memberResponse{user.ID, user.Name, user.Email, member.Role, member.CreatedAt}

	err = app.writeJSON(w, http.StatusCreated, envelope{"member": resp}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// removeMemberHandler revokes a user's access to the restaurant. Callers can
// only remove members whose role is below their own; owners can't be removed.
func (app *application) removeMemberHandler(w http.ResponseWriter, r *http.Request) {
	ms := app.contextGetMembership(r)

	userID, err := app.readIDParam(r, "userID")
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	target, err := app.models.Memberships.Get(r.Context(), ms.RestaurantID, userID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	if !ms.Role.CanManage(target.Role) {
		app.notPermittedResponse(w, r)
		return
	}

	// Passing the role we checked means a concurrent role change can't make
	// us delete a member we aren't allowed to.
	err = app.models.Memberships.Delete(r.Context(), ms.RestaurantID, userID, target.Role)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
