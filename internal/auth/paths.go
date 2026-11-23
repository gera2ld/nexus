package auth

import (
	"context"
	"errors"
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

// NormalizePaths validates and normalizes the path patterns an App
// Password is restricted to, when they are saved. Every path a password
// can reach is under /dav, so patterns leave it out: "/calendars/personal/",
// "/contacts/", "/files/docs/", or "/" for everything. A "/dav" prefix and
// the user's own id are removed, so addresses copied from the dashboard
// work ("/dav/files/{uid}/docs/" is "/files/docs/"). Every entry ends with
// a slash, so a sibling like "personal2/" can never match "personal/".
// Calendar and contact entries stop at a calendar or address book: below
// that, discovery on the collection would list objects the password may
// not read. Invalid patterns are all named in one error. An empty list
// means unrestricted.
//
// Requests use the saved patterns as they are.
func NormalizePaths(in []string, uid string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxPaths {
		return nil, fmt.Errorf("at most %d paths are allowed", maxPaths)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	var bad, invalid []string
	total := 0
	for _, raw := range in {
		p, err := normalizePath(raw, uid)
		if errors.Is(err, errInvalidPath) {
			invalid = append(invalid, fmt.Sprintf("%q", strings.TrimSpace(raw)))
			continue
		}
		if err != nil {
			bad = append(bad, err.Error())
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
			total += len(p)
		}
	}
	if len(invalid) > 0 {
		what := "aren't valid paths"
		if len(invalid) == 1 {
			what = "isn't a valid path"
		}
		bad = append(bad, strings.Join(invalid, ", ")+" "+what+
			": use /files/…, /calendars/ or /contacts/, or one calendar or address book in them")
	}
	if len(bad) > 0 {
		return nil, errors.New(strings.Join(bad, "; "))
	}
	if total > maxPathsSize {
		return nil, fmt.Errorf("paths are too long")
	}
	sort.Strings(out)
	return out, nil
}

func normalizePath(raw, uid string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("a path must not be empty")
	}
	if strings.Contains(p, `\`) {
		return "", fmt.Errorf("%q must not contain a backslash", raw)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%q must not contain ..", raw)
		}
	}
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	if uid != "" {
		for _, ns := range []string{FilesPrefix + "/", "/dav/"} {
			if rest, ok := strings.CutPrefix(p, ns+uid+"/"); ok {
				p = ns + rest
				break
			}
		}
	}
	if rest, ok := strings.CutPrefix(p, "/dav/"); ok {
		p = "/" + rest
	}
	if !validRule(p) {
		return "", errInvalidPath
	}
	return p, nil
}

var errInvalidPath = errors.New("invalid path")

// validRule accepts "/", anything in "/files/", and "/calendars/" or
// "/contacts/" down to one collection.
func validRule(p string) bool {
	if p == "/" || strings.HasPrefix(p, "/files/") {
		return true
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	return (segs[0] == "calendars" || segs[0] == "contacts") && len(segs) <= 2
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

// userRelativePath turns a request path into the form rules use: without
// "/dav" and the user's id, so "/dav/files/{uid}/docs/a" is "/files/docs/a/"
// and "/dav/{uid}/calendars/" is "/calendars/". It refuses paths under
// another user's id.
func userRelativePath(reqPath, uid string) (string, bool) {
	for _, ns := range []string{FilesPrefix, "/dav"} {
		if reqPath == ns || reqPath == ns+"/" {
			return strings.TrimPrefix(ns, "/dav") + "/", true
		}
		rest, ok := strings.CutPrefix(reqPath, ns+"/")
		if !ok {
			continue
		}
		base := strings.TrimPrefix(ns, "/dav") + "/"
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
