package files

import (
	"bytes"
	"net/http"
	"time"

	"golang.org/x/net/webdav"
)

// crossMountMove handles MOVE between two mounts as COPY then DELETE.
//
// Clients see the user's file space as one volume, so Finder drag-and-drop
// and rclone send MOVE between mounts and treat any error as final (neither
// falls back to copying). x/net/webdav can't do it itself: its MOVE deletes
// an existing destination and then renames, and a rename can't cross
// mounts, so the destination would be lost and the move would still fail.
//
// The source is deleted only after the copy succeeded, so a failure leaves
// at worst both copies. Unlike a rename within one mount it is not atomic,
// which is already true of every move on S3.
func (h *Handler) crossMountMove(w http.ResponseWriter, r *http.Request, wh *webdav.Handler, src string) {
	// Lock the source tree for the duration, as x/net/webdav does for a
	// request without an If header. This fails if another client holds a
	// lock on it or anything inside, so a locked file is never deleted out
	// from under its holder, and nobody can lock it mid-move.
	ls := wh.LockSystem
	token, err := ls.Create(time.Now(), webdav.LockDetails{Root: src, Duration: time.Hour})
	if err != nil {
		http.Error(w, "the source is locked", http.StatusLocked)
		return
	}
	defer ls.Unlock(time.Now(), token)

	cp := r.Clone(r.Context())
	cp.Method = "COPY"
	cp.Header.Set("Depth", "infinity") // MOVE always moves the whole tree
	cp.Body = http.NoBody
	rec := &recorder{header: http.Header{}}
	wh.ServeHTTP(rec, cp)
	if rec.status != http.StatusCreated && rec.status != http.StatusNoContent {
		rec.replay(w)
		return
	}

	del := r.Clone(r.Context())
	del.Method = "DELETE"
	del.Header.Del("Destination")
	del.Header.Set("If", "(<"+token+">)") // our own lock on the source
	del.Body = http.NoBody
	drec := &recorder{header: http.Header{}}
	wh.ServeHTTP(drec, del)
	if drec.status != http.StatusNoContent {
		h.log.Error("files: cross-mount move copied but could not delete the source",
			"path", r.URL.Path, "status", drec.status, "body", drec.body.String())
		drec.replay(w)
		return
	}
	rec.replay(w)
}

// recorder buffers a sub-request's response.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (r *recorder) replay(w http.ResponseWriter) {
	for k, v := range r.header {
		w.Header()[k] = v
	}
	if r.status == 0 {
		r.status = http.StatusOK
	}
	w.WriteHeader(r.status)
	if r.status != http.StatusNoContent {
		w.Write(r.body.Bytes())
	}
}
