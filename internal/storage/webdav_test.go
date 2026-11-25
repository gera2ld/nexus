package storage_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"golang.org/x/net/webdav"

	"nexus/internal/storage"
)

// davServer serves root over WebDAV behind basic auth, like
// `rclone serve webdav --user nexus --pass hunter2` on another machine.
func davServer(t *testing.T, root string) string {
	t.Helper()
	h := &webdav.Handler{FileSystem: webdav.Dir(root), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "nexus" || p != "hunter2" {
			w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (e *env) davAccount(url, pass string) *core.Record {
	e.t.Helper()
	return must[*core.Record](e.t)(e.account("webdav", map[string]any{
		"options":        map[string]any{"url": url, "vendor": "rclone"},
		"secret_options": map[string]any{"user": "nexus", "pass": pass},
	}))
}

func TestWebDAVMount(t *testing.T) {
	e := setup(t, testKey)
	ada := e.user("ada@example.com")
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "ada"), 0o755)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("outside"), 0o644)
	acc := e.davAccount(davServer(t, root), "hunter2")
	if strings.Contains(acc.GetString("secrets_enc"), "hunter2") {
		t.Fatal("password stored in plain text")
	}
	ctx := context.Background()

	// The connection test lists the URL itself for an empty path.
	entries, _, err := e.m.Browse(ctx, acc, "")
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "ada" || !entries[0].Dir || entries[1].Name != "notes.txt" {
		t.Errorf("browse = %+v", entries)
	}

	must[*core.Record](t)(e.mount(ada, acc.Id, "docs", "ada", false))
	mt := must[*storage.Mount](t)(e.m.Get(ctx, ada, "docs"))
	writeFile(t, mt, "hello.txt", "hi")
	if b, err := os.ReadFile(filepath.Join(root, "ada", "hello.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("file on the WebDAV server's disk: %q %v", b, err)
	}
	if f, err := mt.VFS.OpenFile("../notes.txt", os.O_RDONLY, 0); err == nil {
		f.Close()
		t.Error("../notes.txt escaped the mount")
	}

	// A wrong password fails the connection test with the server's answer.
	bad := e.davAccount(davServer(t, root), "wrong")
	if _, _, err := e.m.Browse(ctx, bad, ""); err == nil {
		t.Error("browse with a wrong password succeeded")
	}
}

func TestWebDAVOptionsAndPaths(t *testing.T) {
	e := setup(t, testKey)
	uid := e.user("ada@example.com")

	for name, f := range map[string]map[string]any{
		"command":         {"options": map[string]any{"url": "http://x", "bearer_token_command": "id"}},
		"pass in options": {"options": map[string]any{"url": "http://x", "pass": "x"}},
	} {
		if _, err := e.account("webdav", f); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	opts, err := storage.Options("webdav")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if strings.HasSuffix(o.Name, "_command") {
			t.Errorf("Options offers %q", o.Name)
		}
	}

	acc := e.davAccount("http://127.0.0.1:1", "x")
	for _, c := range []struct {
		path string
		ok   bool
	}{
		{"", true}, // the URL itself
		{"photos", true},
		{"photos/2026", true},
		{"/photos", false},
		{"photos/../..", false},
		{".", false},
	} {
		_, err := e.mount(uid, acc.Id, "m"+strings.ToLower(core.GenerateDefaultRandomId()[:5]), c.path, false)
		if (err == nil) != c.ok {
			t.Errorf("remote_path %q: err = %v, want ok=%v", c.path, err, c.ok)
		}
	}
}
