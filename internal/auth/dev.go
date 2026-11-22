// Package auth authenticates DAV requests.
package auth

import (
	"net/http"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"nexus/internal/principal"
	"nexus/internal/schema"
)

// DevAccountPassword accepts the account email and *account* password over
// HTTP Basic. It exists only until App Passwords land (phase 2c) and is off
// unless NEXUS_DEV_ACCOUNT_PASSWORD_AUTH=1. The production design never
// accepts account passwords on DAV endpoints (docs/ARCHITECTURE.md §6).
func DevAccountPassword(app core.App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email, pass, ok := r.BasicAuth()
		if ok {
			if u, err := app.FindAuthRecordByEmail(schema.Users, email); err == nil && u.ValidatePassword(pass) {
				next.ServeHTTP(w, r.WithContext(principal.With(r.Context(), u.Id)))
				return
			}
		}
		Challenge(w)
	})
}

// Disabled rejects every DAV request until real authentication exists.
func Disabled() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="nexus", charset="UTF-8"`)
		http.Error(w, "nexus: DAV sign-in is not available yet (App Passwords land in phase 2c)", http.StatusUnauthorized)
	})
}

// Challenge writes a 401 asking for Basic credentials.
func Challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="nexus", charset="UTF-8"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
