// Package nexus wires Nexus into a PocketBase app: schema, hooks and routes.
package nexus

import (
	"net/http"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"nexus/internal/schema"
)

// Setup registers everything Nexus adds to app. Call it before app.Start().
// dav serves /dav/*; nil means "not implemented yet" (501).
func Setup(app core.App, dav http.Handler) {
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return schema.Apply(e.App)
	})

	app.OnRecordCreateExecute(schema.Users).BindFunc(createDefaultCollections)

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
