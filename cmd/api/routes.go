package main

import (
	"net/http"

	"menugo.flayshon.com/internal/data"
)

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/healthcheck", app.healthcheckHandler)

	mux.HandleFunc("POST /v1/users", app.registerUserHandler)
	mux.HandleFunc("GET /v1/users/me", app.requireAuthenticatedUser(app.showCurrentUserHandler))

	mux.HandleFunc("POST /v1/tokens/authentication", app.createAuthenticationTokenHandler)
	mux.HandleFunc("DELETE /v1/tokens/authentication", app.requireAuthenticatedUser(app.deleteAuthenticationTokenHandler))

	mux.HandleFunc("POST /v1/restaurants", app.requireAuthenticatedUser(app.createRestaurantHandler))
	mux.HandleFunc("GET /v1/restaurants", app.requireAuthenticatedUser(app.listRestaurantsHandler))
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}", app.requireRestaurantRole(data.AnyRole, app.showRestaurantHandler))
	mux.HandleFunc("PATCH /v1/restaurants/{restaurantID}", app.requireRestaurantRole(data.ManagerRoles, app.updateRestaurantHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}", app.requireRestaurantRole(data.OwnerRoles, app.deleteRestaurantHandler))

	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/members", app.requireRestaurantRole(data.ManagerRoles, app.listMembersHandler))
	mux.HandleFunc("POST /v1/restaurants/{restaurantID}/members", app.requireRestaurantRole(data.ManagerRoles, app.addMemberHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}/members/{userID}", app.requireRestaurantRole(data.ManagerRoles, app.removeMemberHandler))

	return app.logRequest(app.recoverPanic(app.secureHeaders(app.authenticate(app.jsonUnmatched(mux)))))
}
