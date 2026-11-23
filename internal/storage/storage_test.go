package storage_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations"
	"github.com/rclone/rclone/fs/config/configmap"

	"nexus/internal/nexus"
	"nexus/internal/schema"
	"nexus/internal/storage"
)

const testKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes

type env struct {
	t   *testing.T
	app core.App
	m   *storage.Manager
}

// setup bootstraps Nexus with key as NEXUS_ENC_KEY. The test's Manager
// shares the app's hooks only through the database, so it is bound too.
func setup(t *testing.T, key string) *env {
	t.Helper()
	t.Setenv(storage.KeyEnv, key)
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := nexus.Setup(app); err != nil {
		t.Fatal(err)
	}
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.ResetBootstrapState() })
	s, err := storage.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	m := storage.NewManager(app, s)
	m.Bind(app)
	t.Cleanup(m.Close)
	return &env{t: t, app: app, m: m}
}

func (e *env) user(email string) string {
	e.t.Helper()
	c, _ := e.app.FindCollectionByNameOrId(schema.Users)
	u := core.NewRecord(c)
	u.SetEmail(email)
	u.SetPassword("correct horse battery staple")
	if err := e.app.Save(u); err != nil {
		e.t.Fatal(err)
	}
	return u.Id
}

func (e *env) account(typ string, fields map[string]any) (*core.Record, error) {
	c, _ := e.app.FindCollectionByNameOrId(schema.StorageAccounts)
	r := core.NewRecord(c)
	r.Set("name", typ+"-"+core.GenerateDefaultRandomId())
	r.Set("type", typ)
	for k, v := range fields {
		r.Set(k, v)
	}
	return r, e.app.Save(r)
}

func (e *env) mount(user, account, name, remote string, readOnly bool) (*core.Record, error) {
	c, _ := e.app.FindCollectionByNameOrId(schema.Mounts)
	r := core.NewRecord(c)
	r.Set("user", user)
	r.Set("account", account)
	r.Set("name", name)
	r.Set("remote_path", remote)
	r.Set("read_only", readOnly)
	return r, e.app.Save(r)
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestSealer(t *testing.T) {
	s, err := storage.NewSealer(testKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal([]byte("hunter2"), "acc1")
	if err != nil || strings.Contains(sealed, "hunter2") {
		t.Fatalf("seal: %q %v", sealed, err)
	}
	if pt, err := s.Open(sealed, "acc1"); err != nil || string(pt) != "hunter2" {
		t.Errorf("open: %q %v", pt, err)
	}
	if _, err := s.Open(sealed, "acc2"); err == nil {
		t.Error("opened with another account's id")
	}
	other, _ := storage.NewSealer(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if _, err := other.Open(sealed, "acc1"); err == nil {
		t.Error("opened with another key")
	}
	for _, bad := range []string{"not base64!", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := storage.NewSealer(bad); err == nil {
			t.Errorf("NewSealer(%q) accepted", bad)
		}
	}
	var none *storage.Sealer
	if _, err := none.Seal(nil, ""); !errors.Is(err, storage.ErrNoKey) {
		t.Errorf("nil sealer: %v, want ErrNoKey", err)
	}
	t.Setenv(storage.KeyEnv, "short")
	if err := nexus.Setup(core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})); err == nil {
		t.Error("Setup accepted a malformed NEXUS_ENC_KEY")
	}
}

func TestAccountOptionsAndSecrets(t *testing.T) {
	e := setup(t, testKey)
	secrets := map[string]any{"access_key_id": "AKIAEXAMPLE", "secret_access_key": "s3cr3t/value"}

	for name, f := range map[string]map[string]any{
		"secret in plain options": {"options": map[string]any{"secret_access_key": "x"}},
		"unknown option":          {"options": map[string]any{"bucket_name": "x"}},
		"plain option as secret":  {"secret_options": map[string]any{"region": "x"}},
		"options not an object":   {"options": []string{"a"}},
	} {
		if _, err := e.account("s3", f); err == nil {
			t.Errorf("%s: saved", name)
		}
	}

	acc := must[*core.Record](t)(e.account("s3", map[string]any{
		"options":        map[string]any{"provider": "Other", "endpoint": "http://127.0.0.1:9000", "force_path_style": true},
		"secret_options": secrets,
	}))
	stored, _ := e.app.FindRecordById(schema.StorageAccounts, acc.Id)
	enc := stored.GetString("secrets_enc")
	if !strings.HasPrefix(enc, "v1:") || strings.Contains(enc, "s3cr3t") {
		t.Errorf("secrets_enc = %q", enc)
	}
	if raw := string(jsonRaw(stored.Get("secret_options"))); raw != "" && raw != "null" {
		t.Errorf("secret_options kept in plain text: %s", raw)
	}
	cm := must[configmap.Getter](t)(e.m.Config(stored))
	// Unset options get rclone's defaults: backends read every option from
	// the map, and S3 refuses a zero chunk size.
	want := map[string]string{"provider": "Other", "endpoint": "http://127.0.0.1:9000", "force_path_style": "true",
		"access_key_id": "AKIAEXAMPLE", "secret_access_key": "s3cr3t/value", "chunk_size": "5Mi"}
	for k, v := range want {
		if got, _ := cm.Get(k); got != v {
			t.Errorf("config[%s] = %q, want %q", k, got, v)
		}
	}

	// Blank secret_options on update keeps the secrets.
	stored.Set("options", map[string]any{"provider": "Other", "region": "eu-1"})
	if err := e.app.Save(stored); err != nil {
		t.Fatal(err)
	}
	again, _ := e.app.FindRecordById(schema.StorageAccounts, acc.Id)
	if again.GetString("secrets_enc") != enc {
		t.Error("update without secret_options changed the secrets")
	}
	// Changing the type needs new secrets.
	again.Set("type", "local")
	again.Set("options", nil)
	if err := e.app.Save(again); err == nil {
		t.Error("type change kept the old backend's secrets")
	}
}

func TestSecretsNeedKey(t *testing.T) {
	e := setup(t, "")
	if _, err := e.account("s3", map[string]any{"secret_options": map[string]any{"secret_access_key": "x"}}); err == nil ||
		!strings.Contains(err.Error(), storage.KeyEnv) {
		t.Errorf("saving secrets without a key: %v", err)
	}
	if _, err := e.account("local", nil); err != nil {
		t.Errorf("account without secrets needs no key: %v", err)
	}
}

func TestMountPaths(t *testing.T) {
	e := setup(t, testKey)
	uid := e.user("ada@example.com")
	local := must[*core.Record](t)(e.account("local", nil))
	s3 := must[*core.Record](t)(e.account("s3", map[string]any{"options": map[string]any{"provider": "AWS"}}))
	for _, c := range []struct {
		acc, path string
		ok        bool
	}{
		{local.Id, t.TempDir(), true},
		{local.Id, "relative/dir", false},
		{local.Id, "/srv/../etc", false},
		{s3.Id, "bucket/prefix", true},
		{s3.Id, "/bucket", false},
		{s3.Id, "bucket/../other", false},
	} {
		_, err := e.mount(uid, c.acc, "m"+strings.ToLower(core.GenerateDefaultRandomId()[:5]), c.path, false)
		if (err == nil) != c.ok {
			t.Errorf("remote_path %q: err = %v, want ok=%v", c.path, err, c.ok)
		}
	}
	if _, err := e.mount(uid, local.Id, "Bad Name", t.TempDir(), false); err == nil {
		t.Error("mount name with spaces/capitals accepted")
	}
	dir := t.TempDir()
	must[*core.Record](t)(e.mount(uid, local.Id, "docs", dir, false))
	if _, err := e.mount(uid, local.Id, "docs", dir, false); err == nil {
		t.Error("duplicate mount name for one user accepted")
	}
}

func TestManagerOpensCachesAndInvalidates(t *testing.T) {
	e := setup(t, testKey)
	ada, bob := e.user("ada@example.com"), e.user("bob@example.com")
	acc := must[*core.Record](t)(e.account("local", nil))
	dir := t.TempDir()
	mrec := must[*core.Record](t)(e.mount(ada, acc.Id, "docs", dir, false))
	ctx := context.Background()

	mt := must[*storage.Mount](t)(e.m.Get(ctx, ada, "docs"))
	writeFile(t, mt, "hello.txt", "hi")
	if b, err := os.ReadFile(filepath.Join(dir, "hello.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("file on disk: %q %v", b, err)
	}
	if again := must[*storage.Mount](t)(e.m.Get(ctx, ada, "docs")); again != mt {
		t.Error("second Get built a new VFS")
	}
	if _, err := e.m.Get(ctx, bob, "docs"); !errors.Is(err, storage.ErrNoMount) {
		t.Errorf("Bob opening Ada's mount: %v, want ErrNoMount", err)
	}
	if names := must[[]*core.Record](t)(e.m.List(ada)); len(names) != 1 || names[0].GetString("name") != "docs" {
		t.Errorf("List = %v", names)
	}

	// Making it read-only drops the cached VFS; the new one refuses writes.
	mrec.Set("read_only", true)
	if err := e.app.Save(mrec); err != nil {
		t.Fatal(err)
	}
	ro := must[*storage.Mount](t)(e.m.Get(ctx, ada, "docs"))
	if ro == mt || !ro.ReadOnly {
		t.Fatal("mount update did not reopen it read-only")
	}
	if f, err := ro.VFS.OpenFile("new.txt", os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
		t.Error("read-only VFS allowed a write")
	}

	// An account in use can't be deleted.
	if err := e.app.Delete(acc); err == nil {
		t.Error("deleted a storage account that has mounts")
	}
	// Account edits drop its mounts from the cache.
	acc.Set("options", map[string]any{"no_check_updated": true})
	if err := e.app.Save(acc); err != nil {
		t.Fatal(err)
	}
	if e.m.Cached() != 0 {
		t.Error("account update left mounts cached")
	}
	// Deleting the user deletes the mount and drops it from the cache.
	must[*storage.Mount](t)(e.m.Get(ctx, ada, "docs"))
	u, _ := e.app.FindRecordById(schema.Users, ada)
	if err := e.app.Delete(u); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.app.CountRecords(schema.Mounts); n != 0 || e.m.Cached() != 0 {
		t.Errorf("after user delete: %d mounts, %d cached", n, e.m.Cached())
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Error("deleting the user deleted the files; they belong to the storage")
	}
}

func TestMountIsConfinedToRemotePath(t *testing.T) {
	e := setup(t, testKey)
	ada := e.user("ada@example.com")
	acc := must[*core.Record](t)(e.account("local", nil))
	root := t.TempDir()
	inside := filepath.Join(root, "ada")
	os.MkdirAll(inside, 0o755)
	os.WriteFile(filepath.Join(root, "secret.txt"), []byte("outside"), 0o644)
	must[*core.Record](t)(e.mount(ada, acc.Id, "docs", inside, false))
	mt := must[*storage.Mount](t)(e.m.Get(context.Background(), ada, "docs"))

	for _, p := range []string{"../secret.txt", "/../secret.txt", "a/../../secret.txt"} {
		if f, err := mt.VFS.OpenFile(p, os.O_RDONLY, 0); err == nil {
			b, _ := io.ReadAll(f)
			f.Close()
			if string(b) == "outside" {
				t.Errorf("%q escaped the mount", p)
			}
		}
	}
}

func writeFile(t *testing.T, mt *storage.Mount, name, body string) {
	t.Helper()
	f, err := mt.VFS.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func jsonRaw(v any) []byte {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return x
	case interface{ String() string }:
		return []byte(x.String())
	}
	return nil
}
