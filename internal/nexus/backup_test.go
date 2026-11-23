package nexus

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"nexus/internal/schema"
	"nexus/internal/storage"
	"nexus/internal/store"
)

const backupKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// serverAt is newServer on an existing data directory.
func serverAt(t *testing.T, dir string) *server {
	t.Helper()
	app := newApp(t, dir)
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{t: t, app: app}
	sealer, err := storage.NewSealer(os.Getenv(storage.KeyEnv))
	if err != nil {
		t.Fatal(err)
	}
	mounts := storage.NewManager(app, sealer)
	mounts.Bind(app)
	t.Cleanup(mounts.Close)
	st := store.New(app)
	mount(r, app, newHandler(app, st, mounts), st, mounts)
	if s.mux, err = r.BuildMux(); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestBackupRestoreRoundTrip takes a PocketBase backup of a used instance,
// unpacks it into an empty data directory as a restore would, and checks
// that sign-in, calendars, contacts, files and encrypted storage secrets
// all survive.
func TestBackupRestoreRoundTrip(t *testing.T) {
	t.Setenv(storage.KeyEnv, backupKey)
	files := t.TempDir()
	srcDir := t.TempDir()

	a := serverAt(t, srcDir)
	u, tok := a.user("ada@example.com")
	pw := a.newPassword(tok, "laptop", "cal", "card", "files")
	creds := basic("ada@example.com", pw.Secret)
	cal := "/dav/" + u.Id + "/calendars/personal/"
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:e1\r\nDTSTAMP:20261122T100000Z\r\nDTSTART:20261201T100000Z\r\nSUMMARY:Backed up\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if r := a.do("PUT", cal+"e1.ics", ics, func(r *http.Request) { creds(r); r.Header.Set("Content-Type", "text/calendar") }); r.code != http.StatusCreated {
		t.Fatalf("PUT event: %d %s", r.code, r.body)
	}
	disk := core.NewRecord(mustCollection(t, a.app, schema.StorageAccounts))
	disk.Set("name", "disk")
	disk.Set("type", "local")
	s3 := core.NewRecord(mustCollection(t, a.app, schema.StorageAccounts))
	s3.Set("name", "s3")
	s3.Set("type", "s3")
	s3.Set("secret_options", map[string]any{"secret_access_key": "kept-secret"})
	for _, rec := range []*core.Record{disk, s3} {
		if err := a.app.Save(rec); err != nil {
			t.Fatal(err)
		}
	}
	m := core.NewRecord(mustCollection(t, a.app, schema.Mounts))
	m.Set("user", u.Id)
	m.Set("account", disk.Id)
	m.Set("name", "home")
	m.Set("remote_path", files)
	if err := a.app.Save(m); err != nil {
		t.Fatal(err)
	}

	if err := a.app.CreateBackup(context.Background(), "nexus.zip"); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	dstDir := t.TempDir()
	unzip(t, filepath.Join(srcDir, core.LocalBackupsDirName, "nexus.zip"), dstDir)

	b := serverAt(t, dstDir)
	if r := b.do("GET", cal+"e1.ics", nil, creds); r.code != http.StatusOK || !strings.Contains(r.body, "Backed up") {
		t.Errorf("event after restore: %d %q", r.code, r.body)
	}
	if got := b.propfind("/dav/"+u.Id+"/contacts/contacts/", creds); got != http.StatusMultiStatus {
		t.Errorf("address book after restore: %d", got)
	}
	if r := b.do("PUT", "/dav/files/"+u.Id+"/home/after.txt", "x", creds); r.code != http.StatusCreated {
		t.Errorf("files after restore: %d %s", r.code, r.body)
	}
	restored, err := b.app.FindRecordById(schema.StorageAccounts, s3.Id)
	if err != nil {
		t.Fatal(err)
	}
	sealer, _ := storage.NewSealer(backupKey)
	if plain, err := sealer.Open(restored.GetString("secrets_enc"), s3.Id); err != nil || !strings.Contains(string(plain), "kept-secret") {
		t.Errorf("storage secrets after restore: %q %v", plain, err)
	}
	other, _ := storage.NewSealer("ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA=")
	if _, err := other.Open(restored.GetString("secrets_enc"), s3.Id); err == nil {
		t.Error("secrets opened with a different NEXUS_ENC_KEY; the backup alone must not be enough")
	}
}

func unzip(t *testing.T, src, dst string) {
	t.Helper()
	zr, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		p := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(p, filepath.Clean(dst)+string(os.PathSeparator)) {
			t.Fatalf("zip entry escapes: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(p, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(out, rc)
		out.Close()
		rc.Close()
	}
}
