// Package files serves each user's mounts over WebDAV at /files/{uid}/
// (docs/ARCHITECTURE.md §7). It uses golang.org/x/net/webdav on top of the
// rclone VFS from internal/storage, with in-memory locks per user.
package files

import (
	"context"
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/webdav"

	"nexus/internal/principal"
	"nexus/internal/storage"
)

// Prefix is where file spaces are mounted.
const Prefix = "/files"

type xmlName = xml.Name

func hexInt(n int64) string { return strconv.FormatInt(n, 16) }

// Handler serves /files. Requests must already carry a principal.
type Handler struct {
	m   *storage.Manager
	log *slog.Logger

	mu    sync.Mutex
	users map[string]*webdav.Handler
}

func New(m *storage.Manager, log *slog.Logger) *Handler {
	return &Handler{m: m, log: log, users: map[string]*webdav.Handler{}}
}

func (h *Handler) forUser(uid string) *webdav.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	wh := h.users[uid]
	if wh == nil {
		wh = &webdav.Handler{
			Prefix:     Prefix + "/" + uid,
			FileSystem: &userFS{m: h.m, uid: uid},
			LockSystem: webdav.NewMemLS(),
			Logger: func(r *http.Request, err error) {
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					h.log.Debug("files", "method", r.Method, "path", r.URL.Path, "error", err)
				}
			},
		}
		h.users[uid] = wh
	}
	return wh
}

var writeMethods = map[string]bool{
	"PUT": true, "DELETE": true, "MKCOL": true, "PROPPATCH": true,
	"MOVE": true, "COPY": true, "LOCK": true,
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, ok := principal.ID(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, Prefix+"/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if rest == "" {
		// Clients configured with just /files/ land in their own space.
		http.Redirect(w, r, Prefix+"/"+uid+"/", http.StatusMovedPermanently)
		return
	}
	seg, _, _ := strings.Cut(rest, "/")
	if seg != uid {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	for _, el := range strings.Split(rest, "/") {
		if el == ".." || el == "." {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
	}

	ctx := r.Context()
	fsys := &userFS{m: h.m, uid: uid}
	base := Prefix + "/" + uid
	target := strings.TrimPrefix(r.URL.Path, base)

	if writeMethods[r.Method] && r.Method != "COPY" { // COPY only writes its destination
		if status, msg := h.writable(ctx, fsys, r.Method, target, true); status != 0 {
			http.Error(w, msg, status)
			return
		}
		if r.Method == "MOVE" || r.Method == "COPY" {
			if dst, ok := destination(r, base); ok {
				if status, msg := h.writable(ctx, fsys, "PUT", dst, false); status != 0 {
					http.Error(w, msg, status)
					return
				}
				// x/net/webdav deletes an existing destination before
				// renaming, so a cross-mount MOVE (which can't rename) would
				// lose it. Refuse up front; clients fall back to COPY+DELETE.
				if from, _ := split(target); r.Method == "MOVE" && from != mountOf(dst) {
					http.Error(w, "can't move between mounts; copy and delete instead", http.StatusForbidden)
					return
				}
			}
		}
	}

	var existed bool
	if r.Method == "PUT" || r.Method == "DELETE" || r.Method == "MOVE" {
		fi, err := fsys.Stat(ctx, target)
		existed = err == nil
		if status := checkConditions(ctx, r, fi, existed); status != 0 {
			w.WriteHeader(status)
			return
		}
	}

	wh := h.forUser(uid)
	if r.Method == "PUT" && existed {
		// x/net/webdav answers 201 even when it replaced a file.
		w = &overwriteWriter{ResponseWriter: w}
	}
	wh.ServeHTTP(w, r)
}

// writable checks a write to target; it returns 0 if allowed. source
// distinguishes the request path from a MOVE/COPY destination.
func (h *Handler) writable(ctx context.Context, fsys *userFS, method, target string, source bool) (int, string) {
	mount, inner := split(target)
	if mount == "" || inner == "/" && (method != "LOCK" && method != "PROPPATCH" || !source) {
		// Mounts are made by the admin; clients can't create, replace,
		// move or delete them.
		return http.StatusForbidden, "the top level lists your mounts and is read-only"
	}
	mt, err := h.m.Get(ctx, fsys.uid, mount)
	if errors.Is(err, storage.ErrNoMount) {
		return http.StatusNotFound, "no such mount"
	}
	if err != nil {
		h.log.Error("files: open mount", "mount", mount, "error", err)
		return http.StatusBadGateway, "the storage for this mount is unavailable"
	}
	if mt.ReadOnly {
		return http.StatusForbidden, "this mount is read-only"
	}
	return 0, ""
}

func mountOf(p string) string { m, _ := split(p); return m }

// destination returns the Destination header's path below base, if it is
// one of this user's paths. Others are left for x/net/webdav to reject.
func destination(r *http.Request, base string) (string, bool) {
	u, err := url.Parse(r.Header.Get("Destination"))
	if err != nil {
		return "", false
	}
	p, ok := strings.CutPrefix(u.Path, base)
	if !ok || (p != "" && !strings.HasPrefix(p, "/")) {
		return "", false
	}
	return p, true
}

// checkConditions applies If-Match and If-None-Match to a write. On S3 the
// check and the write are not atomic, so concurrent writers can still race
// (last writer wins); it does stop a client from overwriting a version it
// hasn't seen.
func checkConditions(ctx context.Context, r *http.Request, fi os.FileInfo, exists bool) int {
	var etag string
	if exists {
		etag = etagOf(ctx, fi)
	}
	if im := r.Header.Get("If-Match"); im != "" {
		if !exists || !matches(im, etag) {
			return http.StatusPreconditionFailed
		}
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if exists && matches(inm, etag) {
			return http.StatusPreconditionFailed
		}
	}
	return 0
}

func etagOf(ctx context.Context, fi os.FileInfo) string {
	if et, ok := fi.(webdav.ETager); ok {
		if s, err := et.ETag(ctx); err == nil {
			return s
		}
	}
	return defaultETag(fi)
}

// matches reports whether an If-Match/If-None-Match list matches etag.
// Weak tags compare by value; "*" matches any existing resource.
func matches(list, etag string) bool {
	for _, t := range strings.Split(list, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

type overwriteWriter struct {
	http.ResponseWriter
	noBody bool
}

func (o *overwriteWriter) WriteHeader(code int) {
	if code == http.StatusCreated {
		code, o.noBody = http.StatusNoContent, true
	}
	o.ResponseWriter.WriteHeader(code)
}

func (o *overwriteWriter) Write(b []byte) (int, error) {
	if o.noBody { // x/net/webdav writes the status text; 204 has no body
		return len(b), nil
	}
	return o.ResponseWriter.Write(b)
}

func (o *overwriteWriter) Unwrap() http.ResponseWriter { return o.ResponseWriter }
