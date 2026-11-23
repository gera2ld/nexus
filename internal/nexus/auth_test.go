package nexus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"nexus/internal/account"
	"nexus/internal/auth"
	"nexus/internal/schema"
	"nexus/internal/storage"
	"nexus/internal/store"
)

// server is a Nexus app behind the real PocketBase router and middleware.
type server struct {
	t   *testing.T
	app core.App
	mux http.Handler
	// logMeta is what the last request attached to PocketBase's request log.
	logMeta auth.LogMeta
}

func newServer(t *testing.T) *server {
	t.Helper()
	app := newApp(t, t.TempDir())
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	r.Bind(apis.CORS(apis.CORSConfig{AllowOrigins: []string{"*"}})) // as apis.Serve does
	s := &server{t: t, app: app}
	r.BindFunc(func(e *core.RequestEvent) error { // sees what the activity logger sees
		err := e.Next()
		s.logMeta, _ = e.Get(apis.RequestEventKeyLogMeta).(auth.LogMeta)
		return err
	})
	mounts := storage.NewManager(app, nil)
	mounts.Bind(app)
	t.Cleanup(mounts.Close)
	st := store.New(app)
	mount(r, app, newHandler(app, st, mounts), st)
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	s.mux = mux
	return s
}

type resp struct {
	code int
	hdr  http.Header
	body string
}

func (s *server) do(method, path string, body any, setup func(*http.Request)) resp {
	s.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "192.0.2.7:4242"
	if body != nil {
		if _, ok := body.(string); ok {
			req.Header.Set("Content-Type", "application/xml")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if setup != nil {
		setup(req)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return resp{rec.Code, rec.Header(), rec.Body.String()}
}

func basic(email, secret string) func(*http.Request) {
	return func(r *http.Request) { r.SetBasicAuth(email, secret) }
}

func bearer(tok string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", tok) }
}

func (s *server) user(email string) (*core.Record, string) {
	s.t.Helper()
	u, err := createUser(s.t, s.app, email)
	if err != nil {
		s.t.Fatal(err)
	}
	tok, err := u.NewAuthToken()
	if err != nil {
		s.t.Fatal(err)
	}
	return u, tok
}

// newPassword creates an App Password through the account API.
func (s *server) newPassword(tok, label string, scopes ...string) account.AppPassword {
	s.t.Helper()
	r := s.do("POST", "/api/nexus/app-passwords", map[string]any{"label": label, "scopes": scopes}, bearer(tok))
	if r.code != http.StatusCreated {
		s.t.Fatalf("create app password: %d %s", r.code, r.body)
	}
	var v account.AppPassword
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		s.t.Fatal(err)
	}
	return v
}

// newPasswordWithPaths creates an App Password with a path restriction.
func (s *server) newPasswordWithPaths(tok, label string, paths []string, scopes ...string) account.AppPassword {
	s.t.Helper()
	r := s.do("POST", "/api/nexus/app-passwords", map[string]any{
		"label": label, "scopes": scopes, "paths": paths,
	}, bearer(tok))
	if r.code != http.StatusCreated {
		s.t.Fatalf("create path-restricted app password: %d %s", r.code, r.body)
	}
	var v account.AppPassword
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		s.t.Fatal(err)
	}
	return v
}

const propfind = `<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/></d:prop></d:propfind>`

func (s *server) propfind(path string, setup func(*http.Request)) int {
	s.t.Helper()
	return s.do("PROPFIND", path, propfind, func(r *http.Request) {
		r.Header.Set("Depth", "0")
		if setup != nil {
			setup(r)
		}
	}).code
}

func TestAppPasswordSignIn(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "laptop", "cal", "card")
	home := "/dav/" + u.Id + "/"
	cal := home + "calendars/personal/"

	if pw.Secret == "" || len(strings.ReplaceAll(pw.Secret, "-", "")) != 26 {
		t.Fatalf("secret %q: want 26 chars in groups", pw.Secret)
	}

	cases := []struct {
		name  string
		path  string
		setup func(*http.Request)
		want  int
	}{
		{"valid", cal, basic("ada@example.com", pw.Secret), http.StatusMultiStatus},
		{"email case ignored", cal, basic("ADA@Example.com", pw.Secret), http.StatusMultiStatus},
		{"secret dashes and case ignored", cal, basic("ada@example.com", strings.ToUpper(strings.ReplaceAll(pw.Secret, "-", ""))), http.StatusMultiStatus},
		{"no credentials", cal, nil, http.StatusUnauthorized},
		{"wrong secret", cal, basic("ada@example.com", auth.NewSecret()), http.StatusUnauthorized},
		{"unknown email", cal, basic("bob@example.com", pw.Secret), http.StatusUnauthorized},
		{"account password refused", cal, basic("ada@example.com", "correct horse battery staple"), http.StatusUnauthorized},
		{"PocketBase token refused", cal, bearer(tok), http.StatusUnauthorized},
	}
	for _, c := range cases {
		if got := s.propfind(c.path, c.setup); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	if r := s.do("PROPFIND", cal, propfind, nil); !strings.HasPrefix(r.hdr.Get("WWW-Authenticate"), "Basic ") {
		t.Errorf("401 without Basic challenge: %q", r.hdr.Get("WWW-Authenticate"))
	}

	// The email index is NOCASE, so a case variant can't be a second account.
	if _, err := createUser(t, s.app, "Ada@Example.COM"); err == nil {
		t.Error("created a second account differing only in email case")
	}

	// Another user's secret does not open Ada's account.
	_, bobTok := s.user("bob@example.com")
	bobPw := s.newPassword(bobTok, "phone", "cal")
	if got := s.propfind(cal, basic("ada@example.com", bobPw.Secret)); got != http.StatusUnauthorized {
		t.Errorf("Bob's secret with Ada's email: got %d, want 401", got)
	}
	// Bob signed in properly still can't read Ada's paths.
	if got := s.propfind(cal, basic("bob@example.com", bobPw.Secret)); got != http.StatusForbidden {
		t.Errorf("Bob on Ada's calendar: got %d, want 403", got)
	}
}

func TestAppPasswordScopes(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	cardOnly := s.newPassword(tok, "contacts app", "card")
	home := "/dav/" + u.Id + "/"
	creds := basic("ada@example.com", cardOnly.Secret)

	for path, want := range map[string]int{
		home:                         http.StatusMultiStatus, // discovery works for either scope
		home + "contacts/contacts/":  http.StatusMultiStatus,
		home + "calendars/":          http.StatusForbidden,
		home + "calendars/personal/": http.StatusForbidden,
	} {
		if got := s.propfind(path, creds); got != want {
			t.Errorf("card-only on %s: got %d, want %d", path, got, want)
		}
	}
}

func TestAppPasswordPaths(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")

	for _, paths := range [][]string{{"/api/"}, {"/dav/x/../y/"}} {
		r := s.do("POST", "/api/nexus/app-passwords", map[string]any{
			"label": "bad", "scopes": []string{"cal"}, "paths": paths,
		}, bearer(tok))
		if r.code != http.StatusUnprocessableEntity {
			t.Errorf("paths %v: got %d, want 422", paths, r.code)
		}
	}

	pw := s.newPasswordWithPaths(tok, "one calendar", []string{"/dav/calendars/personal"}, "cal", "card", "files")
	if got := strings.Join(pw.Paths, ","); got != "/dav/calendars/personal/" {
		t.Errorf("stored paths = %q, want trailing-slash normalisation", got)
	}
	home := "/dav/" + u.Id + "/"
	cal := home + "calendars/personal/"
	creds := basic("ada@example.com", pw.Secret)

	for path, want := range map[string]int{
		home:                             http.StatusMultiStatus, // discovery
		home + "calendars/":              http.StatusMultiStatus, // home listing
		cal:                              http.StatusMultiStatus,
		home + "calendars/work/":         http.StatusForbidden,
		home + "contacts/contacts/":      http.StatusForbidden,
		"/files/" + u.Id + "/docs/a.txt": http.StatusForbidden,
	} {
		if got := s.propfind(path, creds); got != want {
			t.Errorf("propfind %s: got %d, want %d", path, got, want)
		}
	}

	write := func(path string) int {
		const ics = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:a\r\nDTSTAMP:20261122T100000Z\r\nDTSTART:20261201T100000Z\r\nSUMMARY:A\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
		return s.do("PUT", path, ics, func(r *http.Request) {
			creds(r)
			r.Header.Set("Content-Type", "text/calendar")
		}).code
	}
	if got := write(home + "calendars/work/a.ics"); got != http.StatusForbidden {
		t.Errorf("PUT outside allowed path: got %d, want 403", got)
	}
	if n := count(t, s.app, schema.CalendarObjects); n != 0 {
		t.Errorf("%d objects created outside allowed path, want 0", n)
	}
	if got := write(cal + "a.ics"); got != http.StatusCreated {
		t.Errorf("PUT inside allowed path: got %d, want 201", got)
	}
	if n := count(t, s.app, schema.CalendarObjects); n != 1 {
		t.Errorf("%d objects after allowed PUT, want 1", n)
	}

	work := core.NewRecord(mustCollection(t, s.app, schema.Calendars))
	work.Set("owner", u.Id)
	work.Set("slug", "work")
	work.Set("name", "Work")
	work.Set("components", []string{"VEVENT", "VTODO"})
	if err := s.app.Save(work); err != nil {
		t.Fatal(err)
	}
	list := func(secret string) string {
		return s.do("PROPFIND", home+"calendars/", `<propfind xmlns="DAV:"><prop><displayname/></prop></propfind>`,
			func(r *http.Request) {
				r.SetBasicAuth("ada@example.com", secret)
				r.Header.Set("Depth", "1")
			}).body
	}
	body := list(pw.Secret)
	if !strings.Contains(body, "/personal/") || strings.Contains(body, "/work/") {
		t.Errorf("restricted calendar listing leaked or hid collections: %s", body)
	}
	all := s.newPassword(tok, "all calendars", "cal")
	if body := list(all.Secret); !strings.Contains(body, "/work/") {
		t.Errorf("unrestricted calendar listing lacks /work/: %s", body)
	}
}

func TestAppPasswordPathsFiles(t *testing.T) {
	f := newFilesEnv(t)
	restricted := f.server.newPasswordWithPaths(f.userTok, "docs only", []string{"/files/docs/"}, "files")
	creds := basic("ada@example.com", restricted.Secret)

	list := f.do("PROPFIND", f.base+"/", `<propfind xmlns="DAV:"><prop><displayname/></prop></propfind>`,
		func(r *http.Request) { creds(r); r.Header.Set("Depth", "1") })
	if list.code != http.StatusMultiStatus || !strings.Contains(list.body, "/docs/") ||
		strings.Contains(list.body, "/archive/") || strings.Contains(list.body, "/scratch/") {
		t.Errorf("restricted mount listing: %d %s", list.code, list.body)
	}
	put := func(path, body string) int {
		return f.do("PUT", path, body, creds).code
	}
	if got := put(f.base+"/archive/evil.txt", "x"); got != http.StatusForbidden {
		t.Errorf("PUT outside allowed mount: got %d, want 403", got)
	}
	if _, err := os.Stat(filepath.Join(f.arch, "evil.txt")); !os.IsNotExist(err) {
		t.Errorf("refused PUT created a file: %v", err)
	}
	if got := put(f.base+"/docs/ok.txt", "x"); got != http.StatusCreated {
		t.Errorf("PUT inside allowed mount: got %d, want 201", got)
	}
	if _, err := os.Stat(filepath.Join(f.docs, "ok.txt")); err != nil {
		t.Errorf("allowed PUT did not create the file: %v", err)
	}

	// A MOVE's Destination is a write too, even though the request path is
	// an allowed one.
	move := f.do("MOVE", f.base+"/docs/ok.txt", nil, func(r *http.Request) {
		creds(r)
		r.Header.Set("Destination", f.base+"/archive/stolen.txt")
	})
	if move.code != http.StatusForbidden {
		t.Errorf("MOVE to a disallowed destination: got %d, want 403", move.code)
	}
	if _, err := os.Stat(filepath.Join(f.arch, "stolen.txt")); !os.IsNotExist(err) {
		t.Errorf("refused MOVE created a file outside the allowed path: %v", err)
	}
}

// TestAppPasswordPathsParentsOnlyDiscover: a parent folder of an allowed
// path takes only discovery, and its listing shows only what leads to an
// allowed path. Before, a password limited to docs/a/b/ could list, copy
// out and delete the rest of docs/a/.
func TestAppPasswordPathsParentsOnlyDiscover(t *testing.T) {
	f := newFilesEnv(t)
	os.MkdirAll(filepath.Join(f.docs, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(f.docs, "a", "secret.txt"), []byte("outside"), 0o644)
	os.WriteFile(filepath.Join(f.docs, "a", "b", "in.txt"), []byte("inside"), 0o644)
	// Paths as a user would paste them from the account page, with their id.
	pw := f.server.newPasswordWithPaths(f.userTok, "b only",
		[]string{"/files/" + f.uid + "/docs/a/b/", "/files/scratch/"}, "files")
	creds := basic("ada@example.com", pw.Secret)
	depth := func(d string) func(*http.Request) {
		return func(r *http.Request) { creds(r); r.Header.Set("Depth", d) }
	}

	ls := f.do("PROPFIND", f.base+"/docs/a/", nil, depth("1"))
	if ls.code != http.StatusMultiStatus || !strings.Contains(ls.body, "/docs/a/b/") || strings.Contains(ls.body, "secret.txt") {
		t.Errorf("parent listing: %d, want b/ and not secret.txt:\n%s", ls.code, ls.body)
	}
	for _, d := range []string{"infinity", ""} {
		if r := f.do("PROPFIND", f.base+"/docs/", nil, depth(d)); r.code != http.StatusForbidden {
			t.Errorf("PROPFIND Depth %q on a parent: %d, want 403", d, r.code)
		}
	}
	if r := f.do("PROPFIND", f.base+"/docs/a/b/", nil, depth("1")); r.code != http.StatusMultiStatus || !strings.Contains(r.body, "in.txt") {
		t.Errorf("allowed folder (path stored with the user id): %d %s", r.code, r.body)
	}

	cp := f.do("COPY", f.base+"/docs/a/", nil, func(r *http.Request) { creds(r); r.Header.Set("Destination", f.base+"/scratch/loot/") })
	if cp.code != http.StatusForbidden {
		t.Errorf("COPY a parent into an allowed folder: %d, want 403", cp.code)
	}
	if _, err := os.Stat(filepath.Join(f.scratch, "loot")); !os.IsNotExist(err) {
		t.Errorf("refused COPY copied files: %v", err)
	}
	if r := f.do("DELETE", f.base+"/docs/a/", nil, creds); r.code != http.StatusForbidden {
		t.Errorf("DELETE a parent: %d, want 403", r.code)
	}
	if r := f.do("LOCK", f.base+"/docs/a/", lockBody, creds); r.code != http.StatusForbidden {
		t.Errorf("LOCK a parent: %d, want 403", r.code)
	}
	if _, err := os.Stat(filepath.Join(f.docs, "a", "secret.txt")); err != nil {
		t.Errorf("secret.txt is gone: %v", err)
	}
	// A Destination that is a parent of an allowed path is refused too:
	// moving over docs/a would replace everything in it.
	os.WriteFile(filepath.Join(f.scratch, "x.txt"), []byte("x"), 0o644)
	mv := f.do("MOVE", f.base+"/scratch/x.txt", nil, func(r *http.Request) {
		creds(r)
		r.Header.Set("Destination", f.base+"/docs/a")
		r.Header.Set("Overwrite", "T")
	})
	if mv.code != http.StatusForbidden {
		t.Errorf("MOVE over a parent folder: %d, want 403", mv.code)
	}
	if r := f.do("DELETE", f.base+"/docs/a/b/in.txt", nil, creds); r.code != http.StatusNoContent {
		t.Errorf("DELETE inside the allowed folder: %d, want 204", r.code)
	}
}

func TestRevokedAppPasswordStopsAtOnce(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "laptop", "cal")
	cal := "/dav/" + u.Id + "/calendars/personal/"
	creds := basic("ada@example.com", pw.Secret)

	if got := s.propfind(cal, creds); got != http.StatusMultiStatus {
		t.Fatalf("before revoke: %d", got)
	}
	if r := s.do("POST", "/api/nexus/app-passwords/"+pw.ID+"/revoke", nil, bearer(tok)); r.code != http.StatusOK {
		t.Fatalf("revoke: %d %s", r.code, r.body)
	}
	if got := s.propfind(cal, creds); got != http.StatusUnauthorized {
		t.Errorf("after revoke: got %d, want 401", got)
	}
}

func TestLastUsedAtIsThrottled(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "laptop", "cal")
	cal := "/dav/" + u.Id + "/calendars/personal/"
	creds := basic("ada@example.com", pw.Secret)

	lastUsed := func() string {
		rec, err := s.app.FindRecordById(schema.AppPasswords, pw.ID)
		if err != nil {
			t.Fatal(err)
		}
		return rec.GetString("last_used_at")
	}
	if lastUsed() != "" {
		t.Fatal("last_used_at set before use")
	}
	s.propfind(cal, creds)
	first := lastUsed()
	if first == "" {
		t.Fatal("last_used_at not set after use")
	}
	updated := mustRecord(t, s.app, pw.ID).GetString("updated")
	for i := 0; i < 5; i++ {
		s.propfind(cal, creds)
	}
	if lastUsed() != first || mustRecord(t, s.app, pw.ID).GetString("updated") != updated {
		t.Error("last_used_at rewritten within a minute")
	}
}

func TestFailedSignInsAreRateLimited(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "laptop", "cal")
	cal := "/dav/" + u.Id + "/calendars/personal/"

	for i := 0; i < MaxFailedSignIns; i++ {
		if got := s.propfind(cal, basic("ada@example.com", "guess")); got != http.StatusUnauthorized {
			t.Fatalf("failure %d: got %d, want 401", i+1, got)
		}
	}
	r := s.do("PROPFIND", cal, propfind, basic("ada@example.com", pw.Secret))
	if r.code != http.StatusTooManyRequests || r.hdr.Get("Retry-After") == "" {
		t.Errorf("after %d failures: got %d (Retry-After %q), want 429", MaxFailedSignIns, r.code, r.hdr.Get("Retry-After"))
	}
	// Another address is unaffected.
	other := func(r *http.Request) { r.SetBasicAuth("ada@example.com", pw.Secret); r.RemoteAddr = "198.51.100.1:1" }
	if got := s.propfind(cal, other); got != http.StatusMultiStatus {
		t.Errorf("other address: got %d, want 207", got)
	}
}

func TestAccountAPI(t *testing.T) {
	s := newServer(t)
	ada, tok := s.user("ada@example.com")
	_, bobTok := s.user("bob@example.com")

	// /me
	r := s.do("GET", "/api/nexus/me", nil, bearer(tok))
	if r.code != http.StatusOK || !strings.Contains(r.body, `"email":"ada@example.com"`) || !strings.Contains(r.body, ada.Id) {
		t.Errorf("/me: %d %s", r.code, r.body)
	}

	// Create: secret returned once, scopes cleaned, hash stored.
	pw := s.newPassword(tok, "  laptop  ", "card", "cal", "cal")
	if pw.Label != "laptop" || strings.Join(pw.Scopes, ",") != "cal,card" {
		t.Errorf("created %+v; want trimmed label and scopes cal,card", pw)
	}
	stored := mustRecord(t, s.app, pw.ID)
	if stored.GetString("secret_hash") != auth.HashSecret(pw.Secret) || strings.Contains(stored.GetString("secret_hash"), pw.Secret) {
		t.Error("secret_hash is not the hash of the returned secret")
	}

	// List: Ada's only, no secret or hash.
	s.newPassword(bobTok, "bob's", "cal")
	r = s.do("GET", "/api/nexus/app-passwords", nil, bearer(tok))
	var list []map[string]any
	if err := json.Unmarshal([]byte(r.body), &list); err != nil || r.code != http.StatusOK {
		t.Fatalf("list: %d %s", r.code, r.body)
	}
	if len(list) != 1 || list[0]["id"] != pw.ID {
		t.Errorf("list = %v; want only Ada's password", list)
	}
	if strings.Contains(r.body, "secret") || strings.Contains(r.body, stored.GetString("secret_hash")) {
		t.Errorf("list leaks secret or hash: %s", r.body)
	}

	// Revoke: idempotent; Bob can't revoke Ada's.
	if r := s.do("POST", "/api/nexus/app-passwords/"+pw.ID+"/revoke", nil, bearer(bobTok)); r.code != http.StatusNotFound {
		t.Errorf("Bob revoking Ada's: %d, want 404", r.code)
	}
	if mustRecord(t, s.app, pw.ID).GetString("revoked_at") != "" {
		t.Fatal("Bob's request revoked Ada's password")
	}
	for i := 0; i < 2; i++ {
		r := s.do("POST", "/api/nexus/app-passwords/"+pw.ID+"/revoke", nil, bearer(tok))
		if r.code != http.StatusOK || !strings.Contains(r.body, `"revokedAt":"2`) {
			t.Errorf("revoke #%d: %d %s", i+1, r.code, r.body)
		}
	}
	if r := s.do("POST", "/api/nexus/app-passwords/nope/revoke", nil, bearer(tok)); r.code != http.StatusNotFound {
		t.Errorf("revoke unknown id: %d, want 404", r.code)
	}

	// Validation.
	for name, body := range map[string]map[string]any{
		"no scopes":      {"label": "x", "scopes": []string{}},
		"unknown scope":  {"label": "x", "scopes": []string{"cal", "admin"}},
		"empty label":    {"label": "   ", "scopes": []string{"cal"}},
		"label too long": {"label": strings.Repeat("é", 101), "scopes": []string{"cal"}},
	} {
		if r := s.do("POST", "/api/nexus/app-passwords", body, bearer(tok)); r.code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422", name, r.code)
		}
	}

	// Auth: none, superuser, garbage.
	su := mustSuperuser(t, s.app)
	suTok, _ := su.NewAuthToken()
	for name, setup := range map[string]func(*http.Request){
		"no token":        nil,
		"superuser token": bearer(suTok),
	} {
		for _, p := range []string{"/api/nexus/me", "/api/nexus/app-passwords"} {
			if r := s.do("GET", p, nil, setup); r.code != http.StatusUnauthorized && r.code != http.StatusForbidden {
				t.Errorf("%s on %s: %d, want 401/403", name, p, r.code)
			}
		}
	}
	// App Passwords can't manage App Passwords.
	newPw := s.newPassword(tok, "x", "cal")
	if r := s.do("GET", "/api/nexus/app-passwords", nil, basic("ada@example.com", newPw.Secret)); r.code != http.StatusUnauthorized {
		t.Errorf("Basic App Password on account API: %d, want 401", r.code)
	}
}

func TestActivePasswordLimit(t *testing.T) {
	s := newServer(t)
	_, tok := s.user("ada@example.com")
	var last account.AppPassword
	for i := 0; i < account.MaxPasswordsPerUser; i++ {
		last = s.newPassword(tok, "p", "cal")
	}
	body := map[string]any{"label": "one too many", "scopes": []string{"cal"}}
	if r := s.do("POST", "/api/nexus/app-passwords", body, bearer(tok)); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("over the limit: %d, want 422", r.code)
	}
	s.do("POST", "/api/nexus/app-passwords/"+last.ID+"/revoke", nil, bearer(tok))
	if r := s.do("POST", "/api/nexus/app-passwords", body, bearer(tok)); r.code != http.StatusCreated {
		t.Errorf("after revoking one: %d, want 201", r.code)
	}
}

// TestAccountPages: every app path gets the same page with a strict CSP;
// /ui/ serves only built assets. Passes with or without a built app.
func TestAccountPages(t *testing.T) {
	s := newServer(t)
	first := s.do("GET", "/account", nil, nil)
	if first.code != http.StatusOK || !strings.Contains(first.body, "<title>Nexus</title>") {
		t.Fatalf("/account: %d %s", first.code, first.body)
	}
	for _, p := range []string{"/account/", "/account/calendars", "/account/app-passwords"} {
		r := s.do("GET", p, nil, nil)
		if r.code != http.StatusOK || r.body != first.body {
			t.Errorf("%s: %d, want the app page", p, r.code)
		}
		if csp := r.hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self';") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s CSP = %q", p, csp)
		}
	}
	for _, p := range []string{"/ui/", "/ui/index.html", "/ui/.gitkeep", "/ui/assets/missing.js"} {
		if r := s.do("GET", p, nil, nil); r.code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, r.code)
		}
	}
}

func mustRecord(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()
	rec, err := app.FindRecordById(schema.AppPasswords, id)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func mustSuperuser(t *testing.T, app core.App) *core.Record {
	t.Helper()
	c := mustCollection(t, app, core.CollectionNameSuperusers)
	su := core.NewRecord(c)
	su.SetEmail("root@example.com")
	su.SetPassword("superuser password 123")
	if err := app.Save(su); err != nil {
		t.Fatal(err)
	}
	return su
}

func TestRequestLogMeta(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "Pixel phone", "card")
	cal := "/dav/" + u.Id + "/calendars/personal/"
	book := "/dav/" + u.Id + "/contacts/contacts/"

	cases := []struct {
		name  string
		path  string
		setup func(*http.Request)
		want  auth.LogMeta
	}{
		{"ok", book, basic("ADA@example.com", pw.Secret),
			auth.LogMeta{"signin": "ok", "user": "ada@example.com", "appPassword": "Pixel phone", "appPasswordId": pw.ID, "depth": "0"}},
		{"no credentials", book, nil, auth.LogMeta{"signin": "none", "depth": "0"}},
		{"wrong secret", book, basic("ada@exmaple.com", "guess"),
			auth.LogMeta{"signin": "failed", "user": "ada@exmaple.com", "depth": "0"}},
		{"scope", cal, basic("ada@example.com", pw.Secret),
			auth.LogMeta{"signin": "scope", "user": "ada@example.com", "appPassword": "Pixel phone", "appPasswordId": pw.ID, "depth": "0"}},
	}
	for _, c := range cases {
		s.logMeta = nil
		s.propfind(c.path, c.setup)
		if fmt.Sprint(s.logMeta) != fmt.Sprint(c.want) {
			t.Errorf("%s: log meta %v, want %v", c.name, s.logMeta, c.want)
		}
	}
}
