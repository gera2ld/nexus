package nexus

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations"

	"nexus/internal/schema"
)

// newApp bootstraps a Nexus-configured PocketBase app in dir.
func newApp(t *testing.T, dir string) *core.BaseApp {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
	if err := Setup(app); err != nil {
		t.Fatal(err)
	}
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() { app.ResetBootstrapState() })
	return app
}

func createUser(t *testing.T, app core.App, email string) (*core.Record, error) {
	t.Helper()
	users, err := app.FindCollectionByNameOrId(schema.Users)
	if err != nil {
		t.Fatal(err)
	}
	u := core.NewRecord(users)
	u.SetEmail(email)
	u.SetPassword("correct horse battery staple")
	return u, app.Save(u)
}

func count(t *testing.T, app core.App, collection string) int {
	t.Helper()
	n, err := app.CountRecords(collection)
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

func TestSchemaApplied(t *testing.T) {
	app := newApp(t, t.TempDir())
	for _, name := range []string{schema.Users, schema.AppPasswords, schema.Calendars, schema.CalendarObjects,
		schema.Addressbooks, schema.Contacts, schema.DAVChanges} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatalf("collection %s missing: %v", name, err)
		}
		if name == schema.Users {
			if c.CreateRule != nil {
				t.Errorf("users.createRule = %q, want nil (no public sign-up)", *c.CreateRule)
			}
			continue
		}
		for rule, v := range map[string]*string{"list": c.ListRule, "view": c.ViewRule, "create": c.CreateRule, "update": c.UpdateRule, "delete": c.DeleteRule} {
			if v != nil {
				t.Errorf("%s.%sRule = %q, want nil (superuser only)", name, rule, *v)
			}
		}
	}
	if f := mustCollection(t, app, schema.CalendarObjects).Fields.GetByName("ics").(*core.TextField); f.Max < 1<<20 {
		t.Errorf("calendar_objects.ics max = %d, want >= 1 MiB", f.Max)
	}
}

func TestUserGetsDefaultCollections(t *testing.T) {
	app := newApp(t, t.TempDir())
	u, err := createUser(t, app, "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}

	cal, err := app.FindFirstRecordByFilter(schema.Calendars, "owner = {:u} && slug = {:s}",
		map[string]any{"u": u.Id, "s": DefaultCalendarSlug})
	if err != nil {
		t.Fatalf("default calendar: %v", err)
	}
	if got := cal.GetStringSlice("components"); len(got) != 2 {
		t.Errorf("components = %v, want VEVENT and VTODO", got)
	}
	if _, err := app.FindFirstRecordByFilter(schema.Addressbooks, "owner = {:u} && slug = {:s}",
		map[string]any{"u": u.Id, "s": DefaultAddressbookSlug}); err != nil {
		t.Fatalf("default address book: %v", err)
	}
}

func TestDefaultCollectionsAreAtomic(t *testing.T) {
	app := newApp(t, t.TempDir())
	app.OnRecordCreateExecute(schema.Addressbooks).BindFunc(func(e *core.RecordEvent) error {
		return errors.New("injected failure")
	})

	if _, err := createUser(t, app, "bob@example.com"); err == nil {
		t.Fatal("user creation succeeded despite failing default address book")
	}
	if n := count(t, app, schema.Users); n != 0 {
		t.Errorf("%d users persisted, want 0 (rolled back)", n)
	}
	if n := count(t, app, schema.Calendars); n != 0 {
		t.Errorf("%d calendars persisted, want 0 (rolled back)", n)
	}
}

func TestRestartKeepsDataAndRestoresSchema(t *testing.T) {
	dir := t.TempDir()
	app := newApp(t, dir)
	u, err := createUser(t, app, "cy@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate admin-UI schema edits: an extra field and an extra collection.
	cals := mustCollection(t, app, schema.Calendars)
	cals.Fields.Add(&core.TextField{Name: "ui_added"})
	if err := app.Save(cals); err != nil {
		t.Fatal(err)
	}
	if err := app.Save(core.NewBaseCollection("ui_scratch")); err != nil {
		t.Fatal(err)
	}
	app.ResetBootstrapState()

	// "Restart": a fresh app on the same data dir re-applies the snapshot.
	app2 := newApp(t, dir)
	if _, err := app2.FindRecordById(schema.Users, u.Id); err != nil {
		t.Errorf("user lost across restart: %v", err)
	}
	if n := count(t, app2, schema.Calendars); n != 1 {
		t.Errorf("%d calendars after restart, want 1", n)
	}
	if mustCollection(t, app2, schema.Calendars).Fields.GetByName("ui_added") != nil {
		t.Error("UI-added field survived restart; snapshot should remove it")
	}
	if _, err := app2.FindCollectionByNameOrId("ui_scratch"); err == nil {
		t.Error("UI-added collection survived restart; snapshot should remove it")
	}
	if _, err := app2.FindCollectionByNameOrId("_superusers"); err != nil {
		t.Errorf("system collection removed: %v", err)
	}
}

func TestDAVRoutesBypassPocketBaseMiddleware(t *testing.T) {
	app := newApp(t, t.TempDir())
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	// apis.Serve binds CORS outside NewRouter; mirror it so the test covers it.
	r.Bind(apis.CORS(apis.CORSConfig{AllowOrigins: []string{"*"}}))

	echo := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n, _ := io.Copy(io.Discard, req.Body)
		w.Header().Set("DAV", "1, 3, calendar-access, addressbook")
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprintf(w, "%s %s %d", req.Method, req.URL.Path, n)
	})
	RegisterRoutes(r, echo)
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}

	do := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.SetBasicAuth("ada@example.com", "app-password")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	for _, m := range []string{"OPTIONS", "PROPFIND", "PROPPATCH", "REPORT", "MKCOL", "MKCALENDAR", "PUT", "DELETE", "LOCK"} {
		rec := do(m, "/dav/abc123/calendars/", nil)
		if rec.Code != http.StatusMultiStatus || !strings.HasPrefix(rec.Body.String(), m+" ") {
			t.Errorf("%s: got %d %q; DAV handler not reached", m, rec.Code, rec.Body.String())
		}
	}
	if rec := do("OPTIONS", "/dav/x", nil); rec.Header().Get("DAV") == "" {
		t.Error("OPTIONS response lost the DAV header")
	}
	if rec := do("PUT", "/dav/x", make([]byte, 40<<20)); rec.Code != http.StatusMultiStatus {
		t.Errorf("40 MB PUT: got %d, want it to reach the handler", rec.Code)
	}
	if rec := do("PROPFIND", "/.well-known/caldav", nil); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/dav/" {
		t.Errorf("well-known: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}

	// Control: a normal PocketBase route still rejects the unknown token,
	// proving the unbind is what makes /dav work.
	r2, _ := apis.NewRouter(app)
	r2.GET("/control", func(e *core.RequestEvent) error { return e.String(200, "ok") })
	mux2, _ := r2.BuildMux()
	req := httptest.NewRequest("GET", "/control", nil)
	req.SetBasicAuth("ada@example.com", "app-password")
	rec := httptest.NewRecorder()
	mux2.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("control route: got %d, want 401 from pbRejectUnknownToken", rec.Code)
	}
}

func mustCollection(t *testing.T, app core.App, name string) *core.Collection {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
