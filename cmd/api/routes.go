package main

import (
	"net/http"

	"menugo.flayshon.com/internal/data"
)

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/healthcheck", app.healthcheckHandler)

	// Public, for customers: no authentication, callable from any website.
	mux.HandleFunc("GET /v1/menus/{slug}", app.allowAnyOrigin(app.showPublicMenuHandler))
	mux.HandleFunc("GET /v1/menus/{slug}/delivery-quote", app.allowAnyOrigin(app.deliveryQuoteHandler))
	mux.HandleFunc("POST /v1/menus/{slug}/orders", app.allowAnyOrigin(app.limitByIP(app.limiters.publicWrite, app.createOrderHandler)))
	mux.HandleFunc("OPTIONS /v1/menus/{slug}/orders", app.publicPreflightHandler)
	mux.HandleFunc("GET /v1/tracking/{token}", app.allowAnyOrigin(app.showTrackedOrderHandler))
	mux.HandleFunc("POST /v1/tracking/{token}/cancel", app.allowAnyOrigin(app.limitByIP(app.limiters.publicWrite, app.cancelTrackedOrderHandler)))
	mux.HandleFunc("OPTIONS /v1/tracking/{token}/cancel", app.publicPreflightHandler)
	mux.HandleFunc("GET /v1/tracking/{token}/events", app.allowAnyOrigin(app.trackingEventsHandler))

	mux.HandleFunc("POST /v1/users", app.limitByIP(app.limiters.auth, app.registerUserHandler))
	mux.HandleFunc("GET /v1/users/me", app.requireAuthenticatedUser(app.showCurrentUserHandler))

	mux.HandleFunc("POST /v1/tokens/authentication", app.limitByIP(app.limiters.auth, app.createAuthenticationTokenHandler))
	mux.HandleFunc("DELETE /v1/tokens/authentication", app.requireAuthenticatedUser(app.deleteAuthenticationTokenHandler))

	mux.HandleFunc("POST /v1/restaurants", app.requireAuthenticatedUser(app.createRestaurantHandler))
	mux.HandleFunc("GET /v1/restaurants", app.requireAuthenticatedUser(app.listRestaurantsHandler))
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}", app.requireRestaurantRole(data.AnyRole, app.showRestaurantHandler))
	mux.HandleFunc("PATCH /v1/restaurants/{restaurantID}", app.requireRestaurantRole(data.ManagerRoles, app.updateRestaurantHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}", app.requireRestaurantRole(data.OwnerRoles, app.deleteRestaurantHandler))

	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/members", app.requireRestaurantRole(data.ManagerRoles, app.listMembersHandler))
	mux.HandleFunc("POST /v1/restaurants/{restaurantID}/members", app.requireRestaurantRole(data.ManagerRoles, app.addMemberHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}/members/{userID}", app.requireRestaurantRole(data.ManagerRoles, app.removeMemberHandler))

	// Menu management. Owners and admins edit the menu; staff can read it
	// and mark items as sold out. Drivers have no access.
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/menu", app.requireRestaurantRole(data.StaffRoles, app.showMenuHandler))

	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/menu/categories", app.requireRestaurantRole(data.StaffRoles, app.listCategoriesHandler))
	mux.HandleFunc("POST /v1/restaurants/{restaurantID}/menu/categories", app.requireRestaurantRole(data.ManagerRoles, app.createCategoryHandler))
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/menu/categories/{categoryID}", app.requireRestaurantRole(data.StaffRoles, app.showCategoryHandler))
	mux.HandleFunc("PATCH /v1/restaurants/{restaurantID}/menu/categories/{categoryID}", app.requireRestaurantRole(data.ManagerRoles, app.updateCategoryHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}/menu/categories/{categoryID}", app.requireRestaurantRole(data.ManagerRoles, app.deleteCategoryHandler))

	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/menu/items", app.requireRestaurantRole(data.StaffRoles, app.listMenuItemsHandler))
	mux.HandleFunc("POST /v1/restaurants/{restaurantID}/menu/items", app.requireRestaurantRole(data.ManagerRoles, app.createMenuItemHandler))
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/menu/items/{itemID}", app.requireRestaurantRole(data.StaffRoles, app.showMenuItemHandler))
	mux.HandleFunc("PATCH /v1/restaurants/{restaurantID}/menu/items/{itemID}", app.requireRestaurantRole(data.ManagerRoles, app.updateMenuItemHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}/menu/items/{itemID}", app.requireRestaurantRole(data.ManagerRoles, app.deleteMenuItemHandler))
	mux.HandleFunc("PUT /v1/restaurants/{restaurantID}/menu/items/{itemID}/availability", app.requireRestaurantRole(data.StaffRoles, app.setMenuItemAvailabilityHandler))

	// Delivery zones. Staff can see them (to answer customers); owners and
	// admins manage them.
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/delivery-zones", app.requireRestaurantRole(data.StaffRoles, app.listZonesHandler))
	mux.HandleFunc("POST /v1/restaurants/{restaurantID}/delivery-zones", app.requireRestaurantRole(data.ManagerRoles, app.createZoneHandler))
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/delivery-zones/{zoneID}", app.requireRestaurantRole(data.StaffRoles, app.showZoneHandler))
	mux.HandleFunc("PATCH /v1/restaurants/{restaurantID}/delivery-zones/{zoneID}", app.requireRestaurantRole(data.ManagerRoles, app.updateZoneHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}/delivery-zones/{zoneID}", app.requireRestaurantRole(data.ManagerRoles, app.deleteZoneHandler))

	// Orders, as the restaurant manages them. Drivers get their own
	// endpoints for deliveries.
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/orders", app.requireRestaurantRole(data.StaffRoles, app.listOrdersHandler))
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/orders/{orderID}", app.requireRestaurantRole(data.StaffRoles, app.showOrderHandler))
	mux.HandleFunc("PATCH /v1/restaurants/{restaurantID}/orders/{orderID}", app.requireRestaurantRole(data.StaffRoles, app.updateOrderHandler))

	// Drivers. Staff see them (to dispatch); owners and admins manage them.
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/drivers", app.requireRestaurantRole(data.StaffRoles, app.listDriversHandler))
	mux.HandleFunc("POST /v1/restaurants/{restaurantID}/drivers", app.requireRestaurantRole(data.ManagerRoles, app.createDriverHandler))
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/drivers/{driverID}", app.requireRestaurantRole(data.StaffRoles, app.showDriverHandler))
	mux.HandleFunc("PATCH /v1/restaurants/{restaurantID}/drivers/{driverID}", app.requireRestaurantRole(data.ManagerRoles, app.updateDriverHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}/drivers/{driverID}", app.requireRestaurantRole(data.ManagerRoles, app.deleteDriverHandler))

	// Assigning drivers to orders.
	mux.HandleFunc("PUT /v1/restaurants/{restaurantID}/orders/{orderID}/driver", app.requireRestaurantRole(data.StaffRoles, app.assignDriverHandler))
	mux.HandleFunc("DELETE /v1/restaurants/{restaurantID}/orders/{orderID}/driver", app.requireRestaurantRole(data.StaffRoles, app.unassignDriverHandler))

	// Drivers' own deliveries, across all the restaurants they drive for.
	mux.HandleFunc("GET /v1/me/deliveries", app.requireAuthenticatedUser(app.listMyDeliveriesHandler))
	mux.HandleFunc("GET /v1/me/deliveries/{deliveryID}", app.requireAuthenticatedUser(app.showMyDeliveryHandler))
	mux.HandleFunc("PATCH /v1/me/deliveries/{deliveryID}", app.requireAuthenticatedUser(app.updateMyDeliveryHandler))

	// Opening hours and pausing orders.
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/opening-hours", app.requireRestaurantRole(data.StaffRoles, app.showOpeningHoursHandler))
	mux.HandleFunc("PUT /v1/restaurants/{restaurantID}/opening-hours", app.requireRestaurantRole(data.ManagerRoles, app.updateOpeningHoursHandler))
	mux.HandleFunc("PUT /v1/restaurants/{restaurantID}/accepting-orders", app.requireRestaurantRole(data.StaffRoles, app.setAcceptingOrdersHandler))

	// Real-time events (server-sent events).
	mux.HandleFunc("GET /v1/restaurants/{restaurantID}/events", app.requireRestaurantRole(data.StaffRoles, app.restaurantEventsHandler))
	mux.HandleFunc("GET /v1/me/events", app.requireAuthenticatedUser(app.myEventsHandler))

	return app.logRequest(app.matchRoute(mux, app.recoverPanic(app.rateLimit(app.secureHeaders(app.authenticate(app.jsonUnmatched(mux)))))))
}
