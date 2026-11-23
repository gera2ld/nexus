package nexus

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"nexus/internal/schema"
)

type filesEnv struct {
	*server
	uid, base           string
	docs, arch, scratch string // directories behind the mounts
	creds               func(*http.Request)
	userTok             string
}

func newFilesEnv(t *testing.T) *filesEnv {
	t.Helper()
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "files", "files")
	f := &filesEnv{server: s, uid: u.Id, base: "/dav/files/" + u.Id, userTok: tok,
		docs: t.TempDir(), arch: t.TempDir(), scratch: t.TempDir(), creds: basic("ada@example.com", pw.Secret)}
	os.WriteFile(filepath.Join(f.arch, "old.txt"), []byte("archived"), 0o644)

	acc := core.NewRecord(mustCollection(t, s.app, schema.StorageAccounts))
	acc.Set("name", "disk")
	acc.Set("type", "local")
	if err := s.app.Save(acc); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		name, dir string
		ro        bool
	}{{"docs", f.docs, false}, {"archive", f.arch, true}, {"scratch", f.scratch, false}} {
		r := core.NewRecord(mustCollection(t, s.app, schema.Mounts))
		r.Set("user", u.Id)
		r.Set("account", acc.Id)
		r.Set("name", m.name)
		r.Set("remote_path", m.dir)
		r.Set("read_only", m.ro)
		if err := s.app.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// req sends method to base+p with the files App Password plus extra headers.
func (f *filesEnv) req(method, p, body string, hdr ...string) resp {
	f.t.Helper()
	return f.do(method, f.base+p, nilIfEmpty(body), func(r *http.Request) {
		f.creds(r)
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
	})
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestFilesDiscoveryAndAccess(t *testing.T) {
	f := newFilesEnv(t)

	for _, p := range []string{"/dav/files", "/dav/files/"} {
		if r := f.do("PROPFIND", p, nil, f.creds); r.code != http.StatusMovedPermanently || r.hdr.Get("Location") != f.base+"/" {
			t.Errorf("%s: %d -> %q", p, r.code, r.hdr.Get("Location"))
		}
	}
	// /files is the web app's page now; WebDAV there is gone.
	for _, p := range []string{"/files", "/files/", "/files/" + f.uid + "/docs/"} {
		if r := f.do("PROPFIND", p, nil, f.creds); r.code == http.StatusMultiStatus || r.code < 400 {
			t.Errorf("PROPFIND %s: %d, want an error", p, r.code)
		}
	}
	r := f.req("PROPFIND", "/", "", "Depth", "1")
	if r.code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND root: %d %s", r.code, r.body)
	}
	for _, href := range []string{f.base + "/docs/", f.base + "/archive/"} {
		if !strings.Contains(r.body, "<D:href>"+href+"</D:href>") {
			t.Errorf("root listing lacks %s:\n%s", href, r.body)
		}
	}
	if r := f.req("OPTIONS", "/docs/", ""); !strings.Contains(r.hdr.Get("DAV"), "2") {
		t.Errorf("OPTIONS DAV header %q, want class 2 (locking)", r.hdr.Get("DAV"))
	}

	// Scope and isolation.
	calPw := f.newPassword(f.userTok, "cal only", "cal")
	if r := f.do("PROPFIND", f.base+"/", nil, basic("ada@example.com", calPw.Secret)); r.code != http.StatusForbidden {
		t.Errorf("cal-only password on /files: %d, want 403", r.code)
	}
	_, bobTok := f.user("bob@example.com")
	bobPw := f.newPassword(bobTok, "bob files", "files")
	if r := f.do("PROPFIND", f.base+"/docs/", nil, basic("bob@example.com", bobPw.Secret)); r.code != http.StatusForbidden {
		t.Errorf("Bob on Ada's files: %d, want 403", r.code)
	}
	if r := f.do("PROPFIND", f.base+"/docs/", nil, nil); r.code != http.StatusUnauthorized {
		t.Errorf("no credentials: %d, want 401", r.code)
	}
	if r := f.req("GET", "/nope/x.txt", ""); r.code != http.StatusNotFound {
		t.Errorf("unknown mount: %d, want 404", r.code)
	}
	for _, p := range []string{"/docs/../archive/old.txt", "/docs/%2e%2e/%2e%2e/x"} {
		if r := f.req("GET", p, ""); r.code == http.StatusOK {
			t.Errorf("GET %s: 200; dot segments must be refused", p)
		}
	}
}

func TestFilesReadWrite(t *testing.T) {
	f := newFilesEnv(t)

	r := f.req("PUT", "/docs/a.txt", "hello")
	if r.code != http.StatusCreated || r.hdr.Get("ETag") == "" {
		t.Fatalf("PUT new: %d etag %q", r.code, r.hdr.Get("ETag"))
	}
	if b, _ := os.ReadFile(filepath.Join(f.docs, "a.txt")); string(b) != "hello" {
		t.Fatalf("on disk: %q", b)
	}
	r = f.req("PUT", "/docs/a.txt", "hello again")
	if r.code != http.StatusNoContent || r.body != "" {
		t.Errorf("PUT overwrite: %d %q, want 204 and no body", r.code, r.body)
	}
	etag := r.hdr.Get("ETag")
	g := f.req("GET", "/docs/a.txt", "")
	if g.code != http.StatusOK || g.body != "hello again" || g.hdr.Get("ETag") != etag {
		t.Errorf("GET: %d %q etag %q, want PUT's %q", g.code, g.body, g.hdr.Get("ETag"), etag)
	}
	if p := f.req("PROPFIND", "/docs/a.txt", "", "Depth", "0"); !strings.Contains(p.body, "<D:getetag>"+etag+"</D:getetag>") {
		t.Errorf("PROPFIND getetag differs from GET's %s:\n%s", etag, p.body)
	}

	// Conditional writes.
	if r := f.req("PUT", "/docs/a.txt", "x", "If-Match", `"stale"`); r.code != http.StatusPreconditionFailed {
		t.Errorf("If-Match stale: %d, want 412", r.code)
	}
	if r := f.req("PUT", "/docs/a.txt", "x", "If-None-Match", "*"); r.code != http.StatusPreconditionFailed {
		t.Errorf("If-None-Match * on existing: %d, want 412", r.code)
	}
	if r := f.req("PUT", "/docs/new.txt", "x", "If-None-Match", "*"); r.code != http.StatusCreated {
		t.Errorf("If-None-Match * on new: %d, want 201", r.code)
	}
	if r := f.req("PUT", "/docs/a.txt", "v3", "If-Match", etag); r.code != http.StatusNoContent {
		t.Errorf("If-Match current: %d, want 204", r.code)
	}
	if r := f.req("DELETE", "/docs/missing.txt", "", "If-Match", "*"); r.code != http.StatusPreconditionFailed {
		t.Errorf("DELETE If-Match * on missing: %d, want 412", r.code)
	}

	// Directories, move, copy, delete.
	if r := f.req("MKCOL", "/docs/sub/", ""); r.code != http.StatusCreated {
		t.Errorf("MKCOL: %d", r.code)
	}
	if r := f.req("MOVE", "/docs/a.txt", "", "Destination", "http://example.com"+f.base+"/docs/sub/b.txt"); r.code != http.StatusCreated {
		t.Errorf("MOVE: %d %s", r.code, r.body)
	}
	if _, err := os.Stat(filepath.Join(f.docs, "sub", "b.txt")); err != nil {
		t.Errorf("moved file missing: %v", err)
	}
	if r := f.req("COPY", "/archive/old.txt", "", "Destination", f.base+"/docs/old.txt"); r.code != http.StatusCreated {
		t.Errorf("COPY from read-only mount: %d", r.code)
	}
	if r := f.req("DELETE", "/docs/sub/", ""); r.code != http.StatusNoContent {
		t.Errorf("DELETE dir: %d", r.code)
	}
}

func TestFilesReadOnlyAndTopLevel(t *testing.T) {
	f := newFilesEnv(t)
	f.req("PUT", "/docs/a.txt", "hello")

	if r := f.req("GET", "/archive/old.txt", ""); r.code != http.StatusOK || r.body != "archived" {
		t.Errorf("GET read-only: %d %q", r.code, r.body)
	}
	for _, c := range []struct{ method, path, dest string }{
		{"PUT", "/archive/new.txt", ""},
		{"DELETE", "/archive/old.txt", ""},
		{"MKCOL", "/archive/d/", ""},
		{"LOCK", "/archive/old.txt", ""},
		{"MOVE", "/archive/old.txt", "/docs/old.txt"},
		{"MOVE", "/docs/a.txt", "/archive/a.txt"},
		{"COPY", "/docs/a.txt", "/archive/a.txt"},
		{"MKCOL", "/newmount/", ""},
		{"PUT", "/top.txt", ""},
		{"DELETE", "/docs/", ""},
		{"MOVE", "/docs/", "/docs2/"},
	} {
		var hdr []string
		if c.dest != "" {
			hdr = []string{"Destination", f.base + c.dest}
		}
		if r := f.req(c.method, c.path, "x", hdr...); r.code != http.StatusForbidden {
			t.Errorf("%s %s %s: %d, want 403", c.method, c.path, c.dest, r.code)
		}
	}
	// Refused by Nexus's own check, not only by the read-only VFS below it.
	if r := f.req("COPY", "/docs/a.txt", "", "Destination", f.base+"/archive/a.txt"); !strings.Contains(r.body, "read-only") {
		t.Errorf("COPY into read-only mount: %d %q, want Nexus's read-only message", r.code, r.body)
	}
	if r := f.req("MKCOL", "/docs/", ""); r.code != http.StatusMethodNotAllowed {
		t.Errorf("MKCOL on an existing mount: %d, want 405 (already exists)", r.code)
	}
	if b, _ := os.ReadFile(filepath.Join(f.arch, "old.txt")); string(b) != "archived" {
		t.Error("read-only mount was changed")
	}
	if _, err := os.Stat(filepath.Join(f.docs, "a.txt")); err != nil {
		t.Error("source of a refused MOVE is gone")
	}
}

const lockBody = `<?xml version="1.0"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner>test</D:owner></D:lockinfo>`

func TestFilesLocking(t *testing.T) {
	f := newFilesEnv(t)
	f.req("PUT", "/docs/a.txt", "v1")

	r := f.req("LOCK", "/docs/a.txt", lockBody, "Timeout", "Second-60")
	tok := r.hdr.Get("Lock-Token")
	if r.code != http.StatusOK || tok == "" {
		t.Fatalf("LOCK: %d token %q", r.code, tok)
	}
	if r := f.req("PUT", "/docs/a.txt", "v2"); r.code != http.StatusLocked {
		t.Errorf("PUT without token: %d, want 423", r.code)
	}
	if r := f.req("PUT", "/docs/a.txt", "v2", "If", "("+tok+")"); r.code != http.StatusNoContent {
		t.Errorf("PUT with token: %d, want 204", r.code)
	}
	if r := f.req("UNLOCK", "/docs/a.txt", "", "Lock-Token", tok); r.code != http.StatusNoContent {
		t.Errorf("UNLOCK: %d", r.code)
	}
	if r := f.req("PUT", "/docs/a.txt", "v3"); r.code != http.StatusNoContent {
		t.Errorf("PUT after UNLOCK: %d", r.code)
	}
	// Locking a new name creates an empty file (lock-null), as Finder expects.
	if r := f.req("LOCK", "/docs/fresh.txt", lockBody); r.code != http.StatusCreated {
		t.Errorf("LOCK new name: %d, want 201", r.code)
	}
}

func TestFilesMountChangesApply(t *testing.T) {
	f := newFilesEnv(t)
	if r := f.req("PUT", "/docs/a.txt", "x"); r.code != http.StatusCreated {
		t.Fatal(r.code)
	}
	m, err := f.app.FindFirstRecordByFilter(schema.Mounts, "name = 'docs'")
	if err != nil {
		t.Fatal(err)
	}
	m.Set("read_only", true)
	if err := f.app.Save(m); err != nil {
		t.Fatal(err)
	}
	if r := f.req("PUT", "/docs/b.txt", "x"); r.code != http.StatusForbidden {
		t.Errorf("after making docs read-only: %d, want 403", r.code)
	}
	if err := f.app.Delete(m); err != nil {
		t.Fatal(err)
	}
	if r := f.req("GET", "/docs/a.txt", ""); r.code != http.StatusNotFound {
		t.Errorf("after deleting the mount: %d, want 404", r.code)
	}
	r := f.do("GET", "/api/nexus/me", nil, bearer(f.userTok))
	if !strings.Contains(r.body, `"mounts":[{"name":"archive","readOnly":true},{"name":"scratch","readOnly":false}]`) {
		t.Errorf("/me mounts: %s", r.body)
	}
}

func TestFilesCrossMountMove(t *testing.T) {
	f := newFilesEnv(t)
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	// A file over an existing destination.
	f.req("PUT", "/docs/a.txt", "new")
	os.WriteFile(filepath.Join(f.scratch, "a.txt"), []byte("old"), 0o644)
	if r := f.req("MOVE", "/docs/a.txt", "", "Destination", f.base+"/scratch/a.txt", "Overwrite", "F"); r.code != http.StatusPreconditionFailed {
		t.Errorf("Overwrite F onto existing: %d, want 412", r.code)
	}
	if read(filepath.Join(f.scratch, "a.txt")) != "old" || !exists(filepath.Join(f.docs, "a.txt")) {
		t.Fatal("refused move changed something")
	}
	if r := f.req("MOVE", "/docs/a.txt", "", "Destination", f.base+"/scratch/a.txt"); r.code != http.StatusNoContent {
		t.Errorf("MOVE onto existing: %d %s, want 204", r.code, r.body)
	}
	if read(filepath.Join(f.scratch, "a.txt")) != "new" || exists(filepath.Join(f.docs, "a.txt")) {
		t.Error("file move: destination not replaced or source left behind")
	}

	// A directory tree to a new name.
	f.req("MKCOL", "/docs/dir/", "")
	f.req("MKCOL", "/docs/dir/sub/", "")
	f.req("PUT", "/docs/dir/sub/x.txt", "x")
	if r := f.req("MOVE", "/docs/dir/", "", "Destination", f.base+"/scratch/moved/"); r.code != http.StatusCreated {
		t.Errorf("dir MOVE: %d %s, want 201", r.code, r.body)
	}
	if read(filepath.Join(f.scratch, "moved", "sub", "x.txt")) != "x" || exists(filepath.Join(f.docs, "dir")) {
		t.Error("dir move: tree not copied or source left behind")
	}

	// Another client's lock on the source blocks the move entirely.
	f.req("PUT", "/docs/locked.txt", "mine")
	if r := f.req("LOCK", "/docs/locked.txt", lockBody); r.code != http.StatusOK {
		t.Fatalf("LOCK: %d", r.code)
	}
	if r := f.req("MOVE", "/docs/locked.txt", "", "Destination", f.base+"/scratch/locked.txt"); r.code != http.StatusLocked {
		t.Errorf("MOVE of a locked file: %d, want 423", r.code)
	}
	if !exists(filepath.Join(f.docs, "locked.txt")) || exists(filepath.Join(f.scratch, "locked.txt")) {
		t.Error("locked file was moved or copied")
	}

	// Within one mount MOVE is still a rename.
	f.req("PUT", "/docs/r.txt", "r")
	if r := f.req("MOVE", "/docs/r.txt", "", "Destination", f.base+"/docs/r2.txt"); r.code != http.StatusCreated || read(filepath.Join(f.docs, "r2.txt")) != "r" {
		t.Errorf("same-mount MOVE: %d", r.code)
	}
}

// TestFilesDashboardToken: the web app's token works on /files as a Bearer
// header for every method and as the session cookie for reads only.
func TestFilesDashboardToken(t *testing.T) {
	f := newFilesEnv(t)
	tok := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.userTok) }
	os.WriteFile(filepath.Join(f.docs, "a.txt"), []byte("hello"), 0o644)

	if r := f.do("PROPFIND", f.base+"/docs/", nil, tok); r.code != http.StatusMultiStatus {
		t.Errorf("PROPFIND with the token: %d", r.code)
	}
	if r := f.do("PUT", f.base+"/docs/b.txt", "new", tok); r.code != http.StatusCreated {
		t.Errorf("PUT with the token: %d %s", r.code, r.body)
	}
	if r := f.do("PUT", f.base+"/archive/b.txt", "new", tok); r.code != http.StatusForbidden {
		t.Errorf("PUT to a read-only mount with the token: %d", r.code)
	}
	bad := func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }
	if r := f.do("GET", f.base+"/docs/a.txt", nil, bad); r.code != http.StatusUnauthorized ||
		!strings.HasPrefix(r.hdr.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("bad token: %d %q (a Basic challenge would make browsers ask for a password)", r.code, r.hdr.Get("WWW-Authenticate"))
	}
	_, bobTok := f.user("bob@example.com")
	if r := f.do("GET", f.base+"/docs/a.txt", nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+bobTok) }); r.code != http.StatusForbidden {
		t.Errorf("another user's token: %d, want 403", r.code)
	}
	if r := f.do("PROPFIND", "/dav/"+f.uid+"/", nil, tok); r.code != http.StatusUnauthorized {
		t.Errorf("the token on /dav: %d, want 401", r.code)
	}

	// The session cookie comes from the API and works for GET and HEAD.
	r := f.do("POST", "/api/nexus/session", nil, bearer(f.userTok))
	var cookie *http.Cookie
	for _, c := range (&http.Response{Header: r.hdr}).Cookies() {
		if c.Name == "nexus_session" {
			cookie = c
		}
	}
	if r.code != http.StatusNoContent || cookie == nil || cookie.Value != f.userTok || !cookie.HttpOnly ||
		cookie.Path != "/dav/files/" || cookie.SameSite != http.SameSiteLaxMode || cookie.Expires.IsZero() {
		t.Fatalf("POST /session: %d %v", r.code, r.hdr["Set-Cookie"])
	}
	withCookie := func(r *http.Request) { r.AddCookie(cookie) }
	for _, m := range []string{"GET", "HEAD"} {
		if r := f.do(m, f.base+"/docs/a.txt", nil, withCookie); r.code != http.StatusOK {
			t.Errorf("%s with the cookie: %d", m, r.code)
		}
	}
	for _, m := range []string{"PUT", "DELETE", "PROPFIND", "MKCOL"} {
		if r := f.do(m, f.base+"/docs/c.txt", nil, withCookie); r.code != http.StatusUnauthorized {
			t.Errorf("%s with only the cookie: %d, want 401", m, r.code)
		}
	}
	if r := f.do("DELETE", "/api/nexus/session", nil, bearer(f.userTok)); r.code != http.StatusNoContent ||
		!strings.Contains(r.hdr.Get("Set-Cookie"), "nexus_session=;") || !strings.Contains(r.hdr.Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("DELETE /session: %d %q", r.code, r.hdr.Get("Set-Cookie"))
	}
	if r := f.do("POST", "/api/nexus/session", nil, nil); r.code != http.StatusUnauthorized {
		t.Errorf("POST /session signed out: %d", r.code)
	}
}

// TestFilesRawHeaders: raw files display sandboxed, text is UTF-8, and
// ?download makes a download.
func TestFilesRawHeaders(t *testing.T) {
	f := newFilesEnv(t)
	os.WriteFile(filepath.Join(f.docs, "page.html"), []byte("<script>alert(1)</script>"), 0o644)
	os.WriteFile(filepath.Join(f.docs, "notes.md"), []byte("# hi"), 0o644)
	os.WriteFile(filepath.Join(f.docs, "café.png"), []byte("not really"), 0o644)
	os.WriteFile(filepath.Join(f.docs, "a.pdf"), []byte("%PDF-1.4"), 0o644)

	r := f.req("GET", "/docs/page.html", "")
	if r.code != http.StatusOK || r.hdr.Get("Content-Security-Policy") != "sandbox" ||
		r.hdr.Get("X-Content-Type-Options") != "nosniff" || r.hdr.Get("Content-Type") != "text/html; charset=utf-8" ||
		r.hdr.Get("Content-Disposition") != "" {
		t.Errorf("page.html: %d %v", r.code, r.hdr)
	}
	if r := f.req("HEAD", "/docs/page.html", ""); r.hdr.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("HEAD: %v", r.hdr)
	}
	if r := f.req("GET", "/docs/caf%C3%A9.png?download", ""); r.hdr.Get("Content-Type") != "image/png" ||
		r.hdr.Get("Content-Disposition") != "attachment; filename*=utf-8''caf%C3%A9.png" {
		t.Errorf("?download: %v", r.hdr)
	}
	if r := f.req("GET", "/docs/a.pdf", ""); r.hdr.Get("Content-Type") != "application/pdf" || r.hdr.Get("Content-Security-Policy") != "" {
		t.Errorf("PDF (Chrome's viewer won't run sandboxed): %v", r.hdr)
	}
	if r := f.req("GET", "/docs/notes.md", "", "Range", "bytes=0-1"); r.code != http.StatusPartialContent || r.body != "# " {
		t.Errorf("Range: %d %q", r.code, r.body)
	}
}

// TestMigratePasswordPaths: path limits saved while files lived at /files
// are rewritten at startup, and keep working.
func TestMigratePasswordPaths(t *testing.T) {
	f := newFilesEnv(t)
	pw := f.newPasswordWithPaths(f.userTok, "old", []string{"/dav/files/docs/"}, "files", "cal")
	rec, err := f.app.FindRecordById(schema.AppPasswords, pw.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.Set("paths", []string{"/dav/", "/files/docs/"}) // as stored before the move
	if err := f.app.Save(rec); err != nil {
		t.Fatal(err)
	}
	if err := migratePasswordPaths(f.app); err != nil {
		t.Fatal(err)
	}
	rec, _ = f.app.FindRecordById(schema.AppPasswords, pw.ID)
	if got := strings.Join(rec.GetStringSlice("paths"), ","); got != "/dav/calendars/,/dav/contacts/,/dav/files/docs/" {
		t.Fatalf("migrated paths: %s", got)
	}
	creds := basic("ada@example.com", pw.Secret)
	if r := f.do("PROPFIND", f.base+"/docs/", nil, func(r *http.Request) { creds(r); r.Header.Set("Depth", "1") }); r.code != http.StatusMultiStatus {
		t.Errorf("docs after the rewrite: %d", r.code)
	}
	if r := f.do("PROPFIND", f.base+"/scratch/", nil, func(r *http.Request) { creds(r); r.Header.Set("Depth", "1") }); r.code != http.StatusForbidden {
		t.Errorf("scratch (old /dav/ didn't cover files): %d, want 403", r.code)
	}
}
