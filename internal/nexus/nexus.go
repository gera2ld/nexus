// Package nexus wires Nexus into a PocketBase app: schema, hooks and routes.
package nexus

import (
	"net/http"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"nexus/internal/auth"
	"nexus/internal/davserver"
	"nexus/internal/schema"
	"nexus/internal/store"
)

// ChangeRetention is how long sync change-log rows are kept. Clients with an
// older sync token get 403 valid-sync-token and resync from scratch.
const ChangeRetention = 90 * 24 * time.Hour

// Options configure Setup.
type Options struct {
	// DevAccountPasswordAuth lets DAV clients sign in with the account
	// password. Temporary, until App Passwords exist (phase 2c).
	DevAccountPasswordAuth bool
}

// Setup registers everything Nexus adds to app. Call it before app.Start().
func Setup(app core.App, opts Options) {
	st := store.New(app)
	dav := auth.Disabled()
	if opts.DevAccountPasswordAuth {
		dav = auth.DevAccountPassword(app, davserver.New(st))
	}

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return schema.Apply(e.App)
	})

	app.OnRecordCreateExecute(schema.Users).BindFunc(createDefaultCollections)

	// dav_changes has no relation to cascade from; clean it up by hand.
	for _, c := range []string{schema.Calendars, schema.Addressbooks} {
		app.OnRecordAfterDeleteSuccess(c).BindFunc(func(e *core.RecordEvent) error {
			if err := store.DeleteChangesFor(e.App, e.Record.Id); err != nil {
				e.App.Logger().Error("nexus: delete change log", "collection", e.Record.Id, "error", err)
			}
			return e.Next()
		})
	}

	app.Cron().MustAdd("nexusPruneChanges", "17 3 * * *", func() {
		if err := st.PruneChanges(time.Now().Add(-ChangeRetention)); err != nil {
			app.Logger().Error("nexus: prune change log", "error", err)
		}
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		RegisterRoutes(se.Router, dav)
		return se.Next()
	})
}

// davUnbind lists PocketBase global middlewares that break DAV clients
// (see docs/ARCHITECTURE.md §3).
var davUnbind = []string{
	apis.DefaultRejectUnknownTokenMiddlewareId, // 401s every Basic-auth request
	apis.DefaultCorsMiddlewareId,               // answers every OPTIONS itself
	apis.DefaultBodyLimitMiddlewareId,          // 32 MB cap; Nexus sets its own
	apis.DefaultLoadAuthTokenMiddlewareId,      // parses Basic as a PB token
}

// RegisterRoutes mounts the DAV endpoints on a PocketBase router.
func RegisterRoutes(r *router.Router[*core.RequestEvent], dav http.Handler) {
	if dav == nil {
		dav = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nexus: DAV not implemented yet", http.StatusNotImplemented)
		})
	}

	for _, p := range []string{"/.well-known/caldav", "/.well-known/carddav"} {
		r.Any(p, func(e *core.RequestEvent) error {
			return e.Redirect(http.StatusMovedPermanently, "/dav/")
		}).Unbind(davUnbind...)
	}

	g := r.Group("/dav")
	g.Unbind(davUnbind...)
	g.Any("/{path...}", apis.WrapStdHandler(dav))
}
