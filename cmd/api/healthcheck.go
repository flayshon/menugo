package main

import (
	"context"
	"net/http"
	"time"
)

// healthcheckHandler reports whether the API and its database are available.
func (app *application) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status, code := "available", http.StatusOK
	if err := app.db.PingContext(ctx); err != nil {
		app.logError(r, err)
		status, code = "unavailable", http.StatusServiceUnavailable
	}

	env := envelope{
		"status": status,
		"system_info": map[string]string{
			"environment": app.config.env,
			"version":     version,
		},
	}

	if err := app.writeJSON(w, code, env, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
