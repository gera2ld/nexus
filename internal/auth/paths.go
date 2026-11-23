package auth

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
)

const (
	maxPaths     = 100
	maxPathsSize = 8192 // keep within app_passwords.paths maxSize
)

// NormalizePaths validates and normalizes the optional path prefixes an App
// Password is restricted to. Prefixes are user-rooted with the service
// namespace included, e.g. "/dav/calendars/personal/" or "/files/docs/".
// The addresses users see include their id ("/files/{uid}/docs/"), so the
// user's own id is removed. Every entry ends with a slash, so a sibling like
// "personal2/" can never match "personal/". DAV entries stop at a calendar
// or address book: below that, discovery on the collection would list
// objects the password may not read. An empty list means unrestricted.
func NormalizePaths(in []string, uid string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxPaths {
		return nil, fmt.Errorf("at most %d paths are allowed", maxPaths)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	total := 0
	for _, raw := range in {
		p := strings.TrimSpace(raw)
		if p == "" {
			return nil, fmt.Errorf("a path must not be empty")
		}
		if strings.Contains(p, `\`) {
			return nil, fmt.Errorf("path %q must not contain a backslash", raw)
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == ".." {
				return nil, fmt.Errorf("path %q must not contain ..", raw)
			}
		}
		p = path.Clean("/" + strings.TrimPrefix(p, "/"))
		if !strings.HasSuffix(p, "/") {
			p += "/"
		}
		if p != "/" && !strings.HasPrefix(p, "/dav/") && !strings.HasPrefix(p, "/files/") {
			return nil, fmt.Errorf("path %q must start with /dav/ or /files/", raw)
		}
		if uid != "" {
			for _, ns := range []string{"/dav/", "/files/"} {
				if rest, ok := strings.CutPrefix(p, ns+uid+"/"); ok {
					p = ns + rest
				}
			}
		}
		if rest, ok := strings.CutPrefix(p, "/dav/"); ok && rest != "" {
			segs := strings.Split(strings.TrimSuffix(rest, "/"), "/")
			if (segs[0] != "calendars" && segs[0] != "contacts") || len(segs) > 2 {
				return nil, fmt.Errorf("path %q must be /dav/calendars/, /dav/contacts/ or one calendar or address book in them", raw)
			}
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
			total += len(p)
		}
	}
	if total > maxPathsSize {
		return nil, fmt.Errorf("paths are too long")
	}
	sort.Strings(out)
	return out, nil
}

// Access is what a restricted App Password may do at a path.
type Access int

const (
	NoAccess Access = iota
	// Discover: a parent of an allowed path. Only OPTIONS and PROPFIND with
	// Depth 0 or 1 are allowed, and listings show only what leads to an
	// allowed path.
	Discover
	// Full: inside an allowed path.
	Full
)

// PathAccess reports what a password limited to allowed may do at reqPath.
// An empty allowed list means unrestricted.
func PathAccess(reqPath, uid string, allowed []string) Access {
	if len(allowed) == 0 {
		return Full
	}
	rel, ok := userRelativePath(reqPath, uid)
	if !ok {
		return NoAccess
	}
	access := NoAccess
	for _, p := range allowed {
		switch {
		case p == "/" || strings.HasPrefix(rel, p):
			return Full
		case strings.HasPrefix(p, rel):
			access = Discover
		}
	}
	return access
}

// PathAllowed reports whether reqPath is visible to a password limited to
// allowed: inside an allowed path or a parent of one. Backends use it to
// filter listings.
func PathAllowed(reqPath, uid string, allowed []string) bool {
	return PathAccess(reqPath, uid, allowed) != NoAccess
}

// RequestAllowed reports whether r may be served for a password limited to
// allowed. Parents of allowed paths take only discovery requests; anything
// that writes, copies, moves, locks or lists a whole tree needs Full.
func RequestAllowed(r *http.Request, uid string, allowed []string) bool {
	switch PathAccess(r.URL.Path, uid, allowed) {
	case Full:
		return true
	case Discover:
		switch r.Method {
		case http.MethodOptions:
			return true
		case "PROPFIND":
			d := r.Header.Get("Depth")
			return d == "0" || d == "1" // missing means infinity (RFC 4918)
		}
	}
	return false
}

// MarkPathDenied records in the request log that a path restriction
// refused the request, for checks made after the auth middleware.
func MarkPathDenied(ctx context.Context) { setMeta(ctx, "signin", SignInPath) }

// userRelativePath removes the "/dav/{uid}" or "/files/{uid}" prefix from a
// request path, leaving the form used in stored rules. It refuses paths
// under another user's id.
func userRelativePath(reqPath, uid string) (string, bool) {
	for _, ns := range []string{"dav", "files"} {
		base := "/" + ns + "/"
		if reqPath == "/"+ns {
			return base, true
		}
		if !strings.HasPrefix(reqPath, base) {
			continue
		}
		rest := strings.TrimPrefix(reqPath, base)
		if rest == uid {
			return base, true
		}
		if !strings.HasPrefix(rest, uid+"/") {
			return base + rest, false
		}
		rel := path.Clean("/" + strings.TrimPrefix(rest, uid+"/"))
		if !strings.HasSuffix(rel, "/") {
			rel += "/"
		}
		return base + strings.TrimPrefix(rel, "/"), true
	}
	return "", false
}

type allowedPathsKey struct{}

// WithAllowedPaths attaches a password's path restriction to ctx.
func WithAllowedPaths(ctx context.Context, paths []string) context.Context {
	return context.WithValue(ctx, allowedPathsKey{}, paths)
}

// AllowedPaths returns the path restriction from ctx; nil means unrestricted.
func AllowedPaths(ctx context.Context) []string {
	paths, _ := ctx.Value(allowedPathsKey{}).([]string)
	return paths
}
