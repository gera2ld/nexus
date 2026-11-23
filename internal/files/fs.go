package files

import (
	"context"
	"errors"
	"io"
	"mime"
	"os"
	"path"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/vfs"
	"golang.org/x/net/webdav"

	"nexus/internal/auth"
	"nexus/internal/storage"
)

// userFS is one user's file space: a read-only root whose entries are the
// user's mounts, each backed by its rclone VFS. Paths arrive cleaned and
// rooted ("/docs/a.txt") from x/net/webdav.
type userFS struct {
	m   *storage.Manager
	uid string
}

var _ webdav.FileSystem = (*userFS)(nil)

// split returns the mount name and the path inside it ("/" for its root).
// An empty mount name means the user's root.
func split(name string) (mount, inner string) {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "" {
		return "", "/"
	}
	mount, rest, _ := strings.Cut(name, "/")
	return mount, "/" + rest
}

func (u *userFS) open(ctx context.Context, name string) (*storage.Mount, string, error) {
	mount, inner := split(name)
	if mount == "" {
		return nil, "/", nil
	}
	mt, err := u.m.Get(ctx, u.uid, mount)
	if errors.Is(err, storage.ErrNoMount) {
		return nil, "", os.ErrNotExist
	}
	return mt, inner, err
}

func (u *userFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	mt, inner, err := u.open(ctx, name)
	switch {
	case err != nil:
		return err
	case mt == nil || inner == "/":
		return os.ErrPermission
	}
	return mt.VFS.Mkdir(inner, perm)
}

func (u *userFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	mt, inner, err := u.open(ctx, name)
	if err != nil {
		return nil, err
	}
	if mt == nil {
		if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
			return nil, os.ErrPermission
		}
		return u.root(ctx), nil
	}
	h, err := mt.VFS.OpenFile(inner, flag, perm)
	if err != nil {
		return nil, err
	}
	out := handle{Handle: h, ctx: ctx, name: rootName(mt, inner)}
	// A parent of an App Password's allowed paths lists only the entries
	// that lead to one.
	allowed := auth.AllowedPaths(ctx)
	dir := Prefix + "/" + u.uid + strings.TrimSuffix(name, "/") + "/"
	if auth.PathAccess(dir, u.uid, allowed) == auth.Discover {
		out.visible = func(child string) bool { return auth.PathAllowed(dir+child+"/", u.uid, allowed) }
	}
	return out, nil
}

func (u *userFS) RemoveAll(ctx context.Context, name string) error {
	mt, inner, err := u.open(ctx, name)
	switch {
	case err != nil:
		return err
	case mt == nil || inner == "/":
		return os.ErrPermission
	}
	node, err := mt.VFS.Stat(inner)
	if err != nil {
		return err
	}
	return node.RemoveAll()
}

func (u *userFS) Rename(ctx context.Context, oldName, newName string) error {
	from, oldInner, err := u.open(ctx, oldName)
	if err != nil {
		return err
	}
	to, newInner, err := u.open(ctx, newName)
	if err != nil {
		return err
	}
	if from == nil || to == nil || oldInner == "/" || newInner == "/" || from != to {
		// Across mounts a rename would be a copy; x/net/webdav answers 403
		// and clients fall back to COPY + DELETE.
		return os.ErrPermission
	}
	return from.VFS.Rename(oldInner, newInner)
}

func (u *userFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	mt, inner, err := u.open(ctx, name)
	if err != nil {
		return nil, err
	}
	if mt == nil {
		return dirInfo{name: "/", mod: time.Time{}}, nil
	}
	node, err := mt.VFS.Stat(inner)
	if err != nil {
		return nil, err
	}
	return fileInfo{FileInfo: node, name: rootName(mt, inner)}, nil
}

// rootName names a mount's root after the mount, not the remote directory.
func rootName(mt *storage.Mount, inner string) string {
	if inner == "/" {
		return mt.Name
	}
	return ""
}

// root lists the user's mounts as directories, filtered by the App
// Password's path restriction when it has one.
func (u *userFS) root(ctx context.Context) webdav.File {
	return &rootDir{u: u, allowed: auth.AllowedPaths(ctx)}
}

type rootDir struct {
	u       *userFS
	allowed []string
	done    bool
}

// mountAllowed reports whether a mount is inside (or contains) an allowed
// path prefix. With no restriction every mount is listed.
func mountAllowed(allowed []string, name string) bool {
	if len(allowed) == 0 {
		return true
	}
	prefix := "/files/" + name + "/"
	for _, p := range allowed {
		if strings.HasPrefix(p, prefix) || strings.HasPrefix(prefix, p) {
			return true
		}
	}
	return false
}

func (d *rootDir) Close() error                   { return nil }
func (d *rootDir) Read([]byte) (int, error)       { return 0, io.EOF }
func (d *rootDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (d *rootDir) Seek(int64, int) (int64, error) { return 0, nil }
func (d *rootDir) Stat() (os.FileInfo, error)     { return dirInfo{name: "/"}, nil }
func (d *rootDir) Readdir(count int) ([]os.FileInfo, error) {
	if d.done {
		if count > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}
	d.done = true
	recs, err := d.u.m.List(d.u.uid)
	if err != nil {
		return nil, err
	}
	out := make([]os.FileInfo, 0, len(recs))
	for _, r := range recs {
		if !mountAllowed(d.allowed, r.GetString("name")) {
			continue
		}
		out = append(out, dirInfo{name: r.GetString("name"), mod: r.GetDateTime("updated").Time()})
	}
	return out, nil
}

// dirInfo is a synthetic directory: the root or a mount entry in it.
type dirInfo struct {
	name string
	mod  time.Time
}

func (d dirInfo) Name() string       { return d.name }
func (d dirInfo) Size() int64        { return 0 }
func (d dirInfo) Mode() os.FileMode  { return os.ModeDir | 0o555 }
func (d dirInfo) ModTime() time.Time { return d.mod }
func (d dirInfo) IsDir() bool        { return true }
func (d dirInfo) Sys() any           { return nil }

// handle wraps an open VFS file so that Stat and Readdir return fileInfo.
type handle struct {
	vfs.Handle
	ctx     context.Context
	name    string
	visible func(child string) bool // nil: list every entry
}

func (h handle) Stat() (os.FileInfo, error) {
	fi, err := h.Handle.Stat()
	if err != nil {
		return nil, err
	}
	return fileInfo{FileInfo: fi, name: h.name}, nil
}

func (h handle) Readdir(count int) ([]os.FileInfo, error) {
	fis, err := h.Handle.Readdir(count)
	out := fis[:0]
	for _, fi := range fis {
		if h.visible == nil || h.visible(fi.Name()) {
			out = append(out, fileInfo{FileInfo: fi})
		}
	}
	return out, err
}

// DeadProps and Patch accept property writes without storing them, as
// rclone serve webdav does. Windows Explorer and Finder PROPPATCH their own
// properties after uploads and treat failures as errors.
func (h handle) DeadProps() (map[xmlName]webdav.Property, error) { return nil, nil }

func (h handle) Patch(patches []webdav.Proppatch) ([]webdav.Propstat, error) {
	st := webdav.Propstat{Status: 200}
	for _, p := range patches {
		for _, prop := range p.Props {
			st.Props = append(st.Props, webdav.Property{XMLName: prop.XMLName})
		}
	}
	return []webdav.Propstat{st}, nil
}

// fileInfo adds ETag and ContentType to a VFS node.
type fileInfo struct {
	os.FileInfo
	name string // overrides Name() for a mount root
}

func (fi fileInfo) Name() string {
	if fi.name != "" {
		return fi.name
	}
	return fi.FileInfo.Name()
}

// ETag is the backend's MD5 when the backend stores it (S3 does), else the
// same modification-time-and-size tag x/net/webdav uses by default. Local
// disk would have to read the whole file for an MD5, so it uses the default.
func (fi fileInfo) ETag(ctx context.Context) (string, error) {
	if node, ok := fi.FileInfo.(vfs.Node); ok {
		if o, ok := node.DirEntry().(fs.Object); ok {
			f := o.Fs()
			if !f.Features().SlowHash && f.Hashes().Contains(hash.MD5) {
				if sum, err := o.Hash(ctx, hash.MD5); err == nil && sum != "" {
					return `"` + sum + `"`, nil
				}
			}
		}
	}
	return defaultETag(fi), nil
}

func defaultETag(fi os.FileInfo) string {
	return `"` + hexInt(fi.ModTime().UnixNano()) + hexInt(fi.Size()) + `"`
}

// ContentType uses the stored type (S3) or the extension, never sniffing,
// which would read every file during PROPFIND.
func (fi fileInfo) ContentType(ctx context.Context) (string, error) {
	if node, ok := fi.FileInfo.(vfs.Node); ok {
		switch x := node.DirEntry().(type) {
		case fs.Object:
			return fs.MimeType(ctx, x), nil
		case fs.Directory:
			return "httpd/unix-directory", nil
		}
	}
	if t := mime.TypeByExtension(path.Ext(fi.Name())); t != "" {
		return t, nil
	}
	return "application/octet-stream", nil
}
