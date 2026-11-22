// Package nexus wires Nexus into a PocketBase app: schema, hooks and routes.
package nexus

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"nexus/internal/account"
	"nexus/internal/auth"
	"nexus/internal/davserver"
	"nexus/internal/files"
	"nexus/internal/schema"
	"nexus/internal/storage"
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
// It fails only on bad configuration, such as a malformed NEXUS_ENC_KEY.
func Setup(app core.App) error {
	sealer, err := storage.NewSealer(os.Getenv(storage.KeyEnv))
	if err != nil {
		return err
	}
	mounts := storage.NewManager(app, sealer)
	mounts.Bind(app)
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		mounts.Close()
		return e.Next()
	})

	st := store.New(app)
	dav := newHandler(app, st, mounts)

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
	return nil
}

// newHandler is the authenticated handler for /dav and /files: App
// Password auth (one rate limiter for both) around the CalDAV/CardDAV server
// and the file server.
func newHandler(app core.App, st *store.Store, mounts *storage.Manager) http.Handler {
	cal := davserver.New(st)
	fh := files.New(mounts, app.Logger())
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, files.Prefix+"/") {
			fh.ServeHTTP(w, r)
			return
		}
		cal.ServeHTTP(w, r)
	})
	limiter := auth.NewLimiter(MaxFailedSignIns, FailedSignInWindow)
	return auth.NewAppPassword(app, limiter).Wrap(inner)
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

// RegisterRoutes mounts /dav and /files on a PocketBase router. dav must
// authenticate requests itself; RegisterRoutes only records the client
// address (honouring PocketBase's trusted-proxy settings) for rate limiting.
func RegisterRoutes(r *router.Router[*core.RequestEvent], dav http.Handler) {
	for _, p := range []string{"/.well-known/caldav", "/.well-known/carddav"} {
		r.Any(p, func(e *core.RequestEvent) error {
			return e.Redirect(http.StatusMovedPermanently, "/dav/")
		}).Unbind(davUnbind...)
	}

	r.Any(files.Prefix, func(e *core.RequestEvent) error {
		return e.Redirect(http.StatusMovedPermanently, files.Prefix+"/")
	}).Unbind(davUnbind...)

	var warnProxy sync.Once
	serve := func(e *core.RequestEvent) error {
		if e.Request.Header.Get("X-Forwarded-For") != "" && len(e.App.Settings().TrustedProxy.Headers) == 0 {
			warnProxy.Do(func() {
				e.App.Logger().Warn("nexus: requests arrive through a reverse proxy, but no trusted proxy " +
					"headers are set (Settings → Application → IP proxy headers). All clients share " +
					"the proxy's address, so one client's failed sign-ins block every client. See docs/DEPLOY.md.")
			})
		}
		ctx, meta := auth.WithLogMeta(auth.WithClientIP(e.Request.Context(), e.RealIP()))
		if d := e.Request.Header.Get("Depth"); d != "" {
			meta["depth"] = d
		}
		// PocketBase's request log entry for this request carries meta.
		e.Set(apis.RequestEventKeyLogMeta, meta)
		dav.ServeHTTP(e.Response, e.Request.WithContext(ctx))
		return nil
	}
	for _, prefix := range []string{"/dav", files.Prefix} {
		g := r.Group(prefix)
		g.Unbind(davUnbind...)
		g.Any("/{path...}", serve)
	}
}
