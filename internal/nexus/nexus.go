// Package nexus wires Nexus into a PocketBase app: schema, hooks and routes.
package nexus

import (
	"net/http"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"nexus/internal/account"
	"nexus/internal/auth"
	"nexus/internal/davserver"
	"nexus/internal/schema"
	"nexus/internal/store"
)

// ChangeRetention is how long sync change-log rows are kept. Clients with an
// older sync token get 403 valid-sync-token and resync from scratch.
const ChangeRetention = 90 * 24 * time.Hour

// Failed DAV sign-ins allowed per client address before it is blocked for
// the rest of the window (docs/ARCHITECTURE.md §6).
const (
	MaxFailedSignIns   = 10
	FailedSignInWindow = 5 * time.Minute
)

// Setup registers everything Nexus adds to app. Call it before app.Start().
func Setup(app core.App) {
	st := store.New(app)
	dav := newDAV(app, st)

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
		mount(se.Router, dav)
		return se.Next()
	})
}

// newDAV is the authenticated DAV handler: App Password auth around the
// CalDAV/CardDAV server.
func newDAV(app core.App, st *store.Store) http.Handler {
	limiter := auth.NewLimiter(MaxFailedSignIns, FailedSignInWindow)
	return auth.NewAppPassword(app, limiter).Wrap(davserver.New(st))
}

// mount registers every Nexus route.
func mount(r *router.Router[*core.RequestEvent], dav http.Handler) {
	RegisterRoutes(r, dav)
	account.Register(r)
}

// davUnbind lists PocketBase global middlewares that break DAV clients
// (see docs/ARCHITECTURE.md §3).
var davUnbind = []string{
	apis.DefaultRejectUnknownTokenMiddlewareId, // 401s every Basic-auth request
	apis.DefaultCorsMiddlewareId,               // answers every OPTIONS itself
	apis.DefaultBodyLimitMiddlewareId,          // 32 MB cap; Nexus sets its own
	apis.DefaultLoadAuthTokenMiddlewareId,      // parses Basic as a PB token
}

// RegisterRoutes mounts the DAV endpoints on a PocketBase router. dav must
// authenticate requests itself; RegisterRoutes only records the client
// address (honouring PocketBase's trusted-proxy settings) for rate limiting.
func RegisterRoutes(r *router.Router[*core.RequestEvent], dav http.Handler) {

	for _, p := range []string{"/.well-known/caldav", "/.well-known/carddav"} {
		r.Any(p, func(e *core.RequestEvent) error {
			return e.Redirect(http.StatusMovedPermanently, "/dav/")
		}).Unbind(davUnbind...)
	}

	g := r.Group("/dav")
	g.Unbind(davUnbind...)
	g.Any("/{path...}", func(e *core.RequestEvent) error {
		r := e.Request.WithContext(auth.WithClientIP(e.Request.Context(), e.RealIP()))
		dav.ServeHTTP(e.Response, r)
		return nil
	})
}
