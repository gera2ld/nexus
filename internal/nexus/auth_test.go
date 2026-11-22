package nexus

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"nexus/internal/account"
	"nexus/internal/auth"
	"nexus/internal/schema"
	"nexus/internal/store"
)

// server is a Nexus app behind the real PocketBase router and middleware.
type server struct {
	t   *testing.T
	app core.App
	mux http.Handler
}

func newServer(t *testing.T) *server {
	t.Helper()
	app := newApp(t, t.TempDir())
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	r.Bind(apis.CORS(apis.CORSConfig{AllowOrigins: []string{"*"}})) // as apis.Serve does
	mount(r, newDAV(app, store.New(app)))
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	return &server{t: t, app: app, mux: mux}
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
func (s *server) newPassword(tok, label string, scopes ...string) account.AppPasswordView {
	s.t.Helper()
	r := s.do("POST", "/api/nexus/app-passwords", map[string]any{"label": label, "scopes": scopes}, bearer(tok))
	if r.code != http.StatusCreated {
		s.t.Fatalf("create app password: %d %s", r.code, r.body)
	}
	var v account.AppPasswordView
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
		if r := s.do("POST", "/api/nexus/app-passwords", body, bearer(tok)); r.code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, r.code)
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
	var last account.AppPasswordView
	for i := 0; i < account.MaxPasswordsPerUser; i++ {
		last = s.newPassword(tok, "p", "cal")
	}
	body := map[string]any{"label": "one too many", "scopes": []string{"cal"}}
	if r := s.do("POST", "/api/nexus/app-passwords", body, bearer(tok)); r.code != http.StatusBadRequest {
		t.Fatalf("over the limit: %d, want 400", r.code)
	}
	s.do("POST", "/api/nexus/app-passwords/"+last.ID+"/revoke", nil, bearer(tok))
	if r := s.do("POST", "/api/nexus/app-passwords", body, bearer(tok)); r.code != http.StatusCreated {
		t.Errorf("after revoking one: %d, want 201", r.code)
	}
}

func TestAccountPage(t *testing.T) {
	s := newServer(t)
	r := s.do("GET", "/account", nil, nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, "App Password") {
		t.Fatalf("/account: %d", r.code)
	}
	if csp := r.hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if strings.Contains(r.body, "innerHTML") {
		t.Error("page uses innerHTML; render user data with textContent")
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
