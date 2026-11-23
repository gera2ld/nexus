package nexus

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexus/internal/account"
	"nexus/internal/admin"
	"nexus/internal/storage"
)

const adminTestKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes

// newAdminServer is a server with an encryption key and a superuser; it
// returns the superuser's token.
func newAdminServer(t *testing.T) (*server, string) {
	t.Setenv(storage.KeyEnv, adminTestKey)
	s := newServer(t)
	tok, err := mustSuperuser(t, s.app).NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	return s, tok
}

func (s *server) admin(tok string) func(method, path string, body any) resp {
	return func(method, path string, body any) resp {
		return s.do(method, "/api/nexus/admin"+path, body, bearer(tok))
	}
}

func TestAdminNeedsSuperuser(t *testing.T) {
	s, suTok := newAdminServer(t)
	_, userTok := s.user("ada@example.com")
	if r := s.do("GET", "/api/nexus/admin/storage-accounts", nil, nil); r.code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", r.code)
	}
	if r := s.do("GET", "/api/nexus/admin/storage-accounts", nil, bearer(userTok)); r.code != http.StatusForbidden {
		t.Errorf("user token: %d, want 403", r.code)
	}
	if r := s.do("GET", "/api/nexus/admin/storage-accounts", nil, bearer(suTok)); r.code != http.StatusOK {
		t.Errorf("superuser token: %d %s", r.code, r.body)
	}
	if r := s.do("GET", "/api/nexus/me", nil, bearer(suTok)); r.code != http.StatusForbidden {
		t.Errorf("superuser on the user API: %d, want 403", r.code)
	}
}

// TestAdminStorageAccounts: secrets are write-only and merged on update,
// options are checked by the storage hooks, and an account in use can't be
// deleted.
func TestAdminStorageAccounts(t *testing.T) {
	s, tok := newAdminServer(t)
	api := s.admin(tok)

	r := api("POST", "/storage-accounts", map[string]any{
		"name": "Backblaze", "type": "s3",
		"options": map[string]string{"provider": "Other", "endpoint": "https://s3.example.com"},
		"secrets": map[string]string{"access_key_id": "AKID-VALUE", "secret_access_key": "SECRET-VALUE"},
	})
	acc := decode[admin.StorageAccount](t, r)
	if r.code != http.StatusCreated || strings.Join(acc.Secrets, ",") != "access_key_id,secret_access_key" || acc.Options["provider"] != "Other" {
		t.Fatalf("create s3: %d %s", r.code, r.body)
	}
	for _, path := range []string{"/storage-accounts", "/storage-accounts/" + acc.ID} {
		if body := api("GET", path, nil).body; strings.Contains(body, "SECRET-VALUE") || strings.Contains(body, "AKID-VALUE") {
			t.Errorf("GET %s shows a secret: %s", path, body)
		}
	}

	// Changing one secret keeps the other.
	r = api("PATCH", "/storage-accounts/"+acc.ID, map[string]any{"secrets": map[string]string{"secret_access_key": "NEW"}})
	if got := decode[admin.StorageAccount](t, r); strings.Join(got.Secrets, ",") != "access_key_id,secret_access_key" {
		t.Fatalf("PATCH one secret: %s", r.body)
	}
	sealer, _ := storage.NewSealer(adminTestKey)
	rec, _ := s.app.FindRecordById("storage_accounts", acc.ID)
	secrets, err := storage.NewManager(s.app, sealer).Secrets(rec)
	if err != nil || secrets["access_key_id"] != "AKID-VALUE" || secrets["secret_access_key"] != "NEW" {
		t.Errorf("stored secrets after PATCH: %v %v", secrets, err)
	}
	// Options are replaced; secrets untouched by an options-only PATCH.
	r = api("PATCH", "/storage-accounts/"+acc.ID, map[string]any{"name": "B2", "options": map[string]string{"provider": "Other", "region": "eu-1"}})
	if got := decode[admin.StorageAccount](t, r); got.Name != "B2" || len(got.Options) != 2 || got.Options["endpoint"] != "" || len(got.Secrets) != 2 {
		t.Errorf("PATCH name and options: %s", r.body)
	}
	r = api("PATCH", "/storage-accounts/"+acc.ID, map[string]any{"secrets": map[string]string{"access_key_id": "", "secret_access_key": ""}})
	if got := decode[admin.StorageAccount](t, r); len(got.Secrets) != 0 {
		t.Errorf("remove every secret: %s", r.body)
	}

	for name, body := range map[string]map[string]any{
		"secret in options": {"name": "x", "type": "s3", "options": map[string]string{"secret_access_key": "x"}},
		"plain in secrets":  {"name": "x", "type": "s3", "secrets": map[string]string{"region": "x"}},
		"unknown option":    {"name": "x", "type": "s3", "options": map[string]string{"bogus": "x"}},
		"unknown type":      {"name": "x", "type": "ftp"},
		"same name":         {"name": "B2", "type": "local"},
	} {
		if r := api("POST", "/storage-accounts", body); r.code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s, want 422", name, r.code, r.body)
		}
	}
	if r := api("POST", "/storage-accounts", map[string]any{"name": "x", "type": "s3", "options": map[string]string{"secret_access_key": "x"}}); !strings.Contains(r.body, "Options: ") {
		t.Errorf("error doesn't name the field: %s", r.body)
	}

	opts := decode[[]storage.OptionInfo](t, api("GET", "/storage-types/s3/options", nil))
	found := false
	for _, o := range opts {
		if o.Name == "secret_access_key" {
			found = o.Secret
		}
	}
	if !found {
		t.Errorf("s3 options don't mark secret_access_key as secret")
	}
}

func TestAdminBrowseAndMounts(t *testing.T) {
	s, tok := newAdminServer(t)
	api := s.admin(tok)
	u, userTok := s.user("ada@example.com")
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "photos"), 0o755)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(root, "Zed.txt"), nil, 0o644)

	disk := decode[admin.StorageAccount](t, api("POST", "/storage-accounts", map[string]any{"name": "Disk", "type": "local"}))
	browse := func(p string) resp {
		return api("POST", "/storage-accounts/"+disk.ID+"/browse", map[string]any{"path": p})
	}
	r := browse(root)
	l := decode[admin.Listing](t, r)
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	if r.code != http.StatusOK || strings.Join(names, ",") != "photos,a.txt,Zed.txt" || !l.Entries[0].Dir || l.Entries[1].Size != 5 {
		t.Fatalf("browse: %d %s", r.code, r.body)
	}
	if r := browse(filepath.Join(root, "missing")); r.code != http.StatusNotFound {
		t.Errorf("missing folder: %d", r.code)
	}
	if r := browse(filepath.Join(root, "a.txt")); r.code != http.StatusUnprocessableEntity {
		t.Errorf("a file: %d", r.code)
	}
	if r := browse("relative/path"); r.code != http.StatusUnprocessableEntity {
		t.Errorf("relative local path: %d", r.code)
	}

	mount := func(body map[string]any) resp { return api("POST", "/mounts", body) }
	r = mount(map[string]any{"user": u.Id, "account": disk.ID, "name": "docs", "remotePath": root})
	m := decode[admin.AdminMount](t, r)
	if r.code != http.StatusCreated || m.User.Email != "ada@example.com" || m.Account.Name != "Disk" {
		t.Fatalf("create mount: %d %s", r.code, r.body)
	}
	for name, body := range map[string]map[string]any{
		"bad name":      {"user": u.Id, "account": disk.ID, "name": "My Docs", "remotePath": root},
		"relative path": {"user": u.Id, "account": disk.ID, "name": "x", "remotePath": "srv"},
		"same name":     {"user": u.Id, "account": disk.ID, "name": "docs", "remotePath": root},
		"no such user":  {"user": "nosuchuser1234", "account": disk.ID, "name": "x", "remotePath": root},
	} {
		if r := mount(body); r.code != http.StatusUnprocessableEntity {
			t.Errorf("mount with %s: %d %s, want 422", name, r.code, r.body)
		}
	}
	if r := mount(map[string]any{"user": u.Id, "account": disk.ID, "name": "x", "remotePath": "srv"}); !strings.Contains(r.body, "Remote path: ") {
		t.Errorf("error doesn't name the field: %s", r.body)
	}

	r = api("PATCH", "/mounts/"+m.ID, map[string]any{"readOnly": true, "name": "papers"})
	if got := decode[admin.AdminMount](t, r); !got.ReadOnly || got.Name != "papers" || got.RemotePath != root {
		t.Fatalf("PATCH mount: %s", r.body)
	}
	me := decode[account.Me](t, s.do("GET", "/api/nexus/me", nil, bearer(userTok)))
	if len(me.Mounts) != 1 || me.Mounts[0].Name != "papers" || !me.Mounts[0].ReadOnly {
		t.Errorf("the user's view: %+v", me.Mounts)
	}
	if list := decode[[]admin.AdminMount](t, api("GET", "/mounts", nil)); len(list) != 1 {
		t.Errorf("list: %+v", list)
	}

	if r := api("DELETE", "/storage-accounts/"+disk.ID, nil); r.code != http.StatusConflict || !strings.Contains(r.body, "1 mount") {
		t.Errorf("delete an account in use: %d %s", r.code, r.body)
	}
	if r := api("DELETE", "/mounts/"+m.ID, nil); r.code != http.StatusNoContent {
		t.Errorf("delete mount: %d", r.code)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Errorf("deleting the mount touched the files: %v", err)
	}
	if r := api("DELETE", "/storage-accounts/"+disk.ID, nil); r.code != http.StatusNoContent {
		t.Errorf("delete unused account: %d %s", r.code, r.body)
	}
}
