// Package auth authenticates DAV requests with App Passwords
// (docs/ARCHITECTURE.md §6).
//
// A DAV client signs in over HTTP Basic with the account email and an App
// Password. The account's own password is never accepted here. Each App
// Password carries scopes; the request path decides which scope is needed.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"

	"nexus/internal/principal"
	"nexus/internal/schema"
)

// Scopes an App Password can carry.
const (
	ScopeCal   = "cal"
	ScopeCard  = "card"
	ScopeFiles = "files"
)

var AllScopes = []string{ScopeCal, ScopeCard, ScopeFiles}

// lastUsedEvery throttles last_used_at writes.
const lastUsedEvery = time.Minute

var secretEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewSecret returns a random 128-bit App Password, formatted for typing:
// 26 base32 characters in dash-separated groups of four or five.
func NewSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	s := secretEncoding.EncodeToString(b) // 26 chars
	groups := []string{s[0:4], s[4:8], s[8:12], s[12:16], s[16:21], s[21:26]}
	return strings.Join(groups, "-")
}

// HashSecret normalises a typed App Password (case, dashes and spaces are
// ignored) and returns the hex SHA-256 stored in app_passwords.secret_hash.
// A fast hash is enough: the secret is 128 random bits.
func HashSecret(secret string) string {
	norm := strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ':
			return -1
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return r
	}, secret)
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// RequiredScopes returns the scopes that grant access to a /dav or /files
// path; any one of them suffices.
func RequiredScopes(p string) []string {
	switch {
	case strings.HasPrefix(p, "/files/"):
		return []string{ScopeFiles}
	case strings.HasPrefix(p, "/dav/"):
		segs := strings.Split(strings.Trim(p, "/"), "/") // dav, uid, seg, ...
		if len(segs) >= 3 {
			switch segs[2] {
			case "calendars":
				return []string{ScopeCal}
			case "contacts":
				return []string{ScopeCard}
			}
		}
		// /dav/ and /dav/{uid}/: discovery, needed by either protocol.
		return []string{ScopeCal, ScopeCard}
	}
	return nil
}

type ipKey struct{}

// WithClientIP records the client address used for rate limiting.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ipKey{}, ip)
}

func clientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(ipKey{}).(string); ok && ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// AppPassword is the DAV authentication middleware.
type AppPassword struct {
	app     core.App
	limiter *Limiter
	now     func() time.Time
}

func NewAppPassword(app core.App, limiter *Limiter) *AppPassword {
	return &AppPassword{app: app, limiter: limiter, now: time.Now}
}

// Wrap authenticates and authorises each request before calling next.
func (a *AppPassword) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if a.limiter.Blocked(ip) {
			w.Header().Set("Retry-After", "300")
			http.Error(w, "too many failed sign-ins", http.StatusTooManyRequests)
			return
		}

		email, secret, ok := r.BasicAuth()
		if !ok {
			Challenge(w)
			return
		}
		user, pw := a.check(email, secret)
		if pw == nil {
			a.limiter.Fail(ip)
			Challenge(w)
			return
		}

		if !hasAnyScope(pw.GetStringSlice("scopes"), RequiredScopes(r.URL.Path)) {
			http.Error(w, "this App Password is not allowed to access "+r.URL.Path, http.StatusForbidden)
			return
		}

		a.touch(pw)
		next.ServeHTTP(w, r.WithContext(principal.With(r.Context(), user.Id)))
	})
}

// check returns the user and App Password record for valid credentials.
func (a *AppPassword) check(email, secret string) (*core.Record, *core.Record) {
	if email == "" || secret == "" {
		return nil, nil
	}
	user, err := a.app.FindAuthRecordByEmail(schema.Users, email)
	if err != nil {
		return nil, nil
	}
	pw, err := a.app.FindFirstRecordByFilter(schema.AppPasswords,
		"secret_hash = {:h} && user = {:u} && revoked_at = ''",
		dbx.Params{"h": HashSecret(secret), "u": user.Id})
	if err != nil {
		return nil, nil
	}
	return user, pw
}

// touch updates last_used_at at most once per lastUsedEvery.
func (a *AppPassword) touch(pw *core.Record) {
	now := a.now().UTC()
	if last := pw.GetDateTime("last_used_at"); !last.IsZero() && now.Sub(last.Time()) < lastUsedEvery {
		return
	}
	dt, _ := types.ParseDateTime(now)
	pw.Set("last_used_at", dt)
	if err := a.app.Save(pw); err != nil {
		a.app.Logger().Warn("auth: update last_used_at", "error", err)
	}
}

func hasAnyScope(have, want []string) bool {
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return true
			}
		}
	}
	return false
}

// Challenge writes a 401 asking for Basic credentials.
func Challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="nexus", charset="UTF-8"`)
	http.Error(w, "unauthorized: sign in with your email and an App Password", http.StatusUnauthorized)
}
