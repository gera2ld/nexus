// Package files serves each user's mounts over WebDAV at /files/{uid}/
// (docs/ARCHITECTURE.md §7). It uses golang.org/x/net/webdav on top of the
// rclone VFS from internal/storage, with in-memory locks per user.
package files

import (
	"context"
	"encoding/xml"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/webdav"

	"nexus/internal/auth"
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

	// COPY only writes its destination; every other write method writes
	// the request path.
	if writeMethods[r.Method] && r.Method != "COPY" {
		if status, msg := h.writable(ctx, fsys, r.Method, target, true); status != 0 {
			http.Error(w, msg, status)
			return
		}
	}
	dst, hasDst := "", false
	if r.Method == "MOVE" || r.Method == "COPY" {
		if dst, hasDst = destination(r, base); hasDst {
			// The source path was checked by the auth middleware; a COPY or
			// MOVE also writes its Destination, which must be inside an
			// allowed path (a parent of one only allows discovery).
			if auth.PathAccess(base+dst, uid, auth.AllowedPaths(ctx)) != auth.Full {
				auth.MarkPathDenied(ctx)
				http.Error(w, "this App Password is not allowed to access "+base+dst, http.StatusForbidden)
				return
			}
			if status, msg := h.writable(ctx, fsys, "PUT", dst, false); status != 0 {
				http.Error(w, msg, status)
				return
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
	if r.Method == "MOVE" && hasDst && mountOf(target) != mountOf(dst) {
		h.crossMountMove(w, r, wh, target)
		return
	}
	if r.Method == "GET" || r.Method == "HEAD" {
		w = rawHeaders(w, r, target)
	}
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
	if method == "MKCOL" && mount != "" && inner == "/" {
		// RFC 4918: MKCOL on an existing collection is 405, which clients
		// (rclone making parent directories) read as "already there".
		if _, err := h.m.Get(ctx, fsys.uid, mount); err == nil {
			return http.StatusMethodNotAllowed, "this mount already exists"
		}
	}
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

// rawHeaders sets the headers of a raw file (docs/WEB.md §6.3). The file
// displays but can't run scripts with the viewer's session: a page's
// scripts run only through a share link. ?download makes it a download.
func rawHeaders(w http.ResponseWriter, r *http.Request, target string) http.ResponseWriter {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	if _, ok := r.URL.Query()["download"]; ok {
		if name := path.Base(target); name != "/" && name != "." {
			h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		}
	}
	return &charsetWriter{ResponseWriter: w}
}

// charsetWriter finishes the raw headers once the type is known: text
// without a charset is UTF-8, and everything but PDF is sandboxed. The type
// itself comes from the backend or the extension.
type charsetWriter struct {
	http.ResponseWriter
	done bool
}

func (c *charsetWriter) WriteHeader(code int) {
	if !c.done {
		c.done = true
		h := c.Header()
		ct := h.Get("Content-Type")
		if strings.HasPrefix(ct, "text/") && !strings.Contains(strings.ToLower(ct), "charset=") {
			h.Set("Content-Type", ct+"; charset=utf-8")
		}
		// Chrome's PDF viewer refuses to run in a sandboxed document, so
		// PDFs aren't sandboxed. The viewer runs apart from the page, so a
		// PDF can't act with the viewer's session either way.
		if mt, _, _ := mime.ParseMediaType(ct); mt != "application/pdf" {
			h.Set("Content-Security-Policy", "sandbox")
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *charsetWriter) Write(b []byte) (int, error) {
	if !c.done {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

func (c *charsetWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
