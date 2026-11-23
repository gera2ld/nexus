package auth

import "context"

// LogMeta collects facts about a DAV request for PocketBase's request log
// (stored as the log entry's "meta"), so "why isn't my phone syncing?" can
// be answered from the admin UI's Logs page: who signed in, with which App
// Password, and why a sign-in failed.
type LogMeta map[string]any

type metaKey struct{}

// WithLogMeta attaches an empty LogMeta to ctx and returns both.
func WithLogMeta(ctx context.Context) (context.Context, LogMeta) {
	m := LogMeta{}
	return context.WithValue(ctx, metaKey{}, m), m
}

func setMeta(ctx context.Context, k string, v any) {
	if m, ok := ctx.Value(metaKey{}).(LogMeta); ok {
		m[k] = v
	}
}

// Sign-in outcomes recorded under "signin".
const (
	SignInOK      = "ok"
	SignInNone    = "none"    // no credentials; clients often try this first
	SignInFailed  = "failed"  // unknown email, wrong or revoked App Password
	SignInBlocked = "blocked" // too many failures from this address
	SignInScope   = "scope"   // valid App Password without the needed scope
	SignInPath    = "path"    // valid App Password outside its allowed paths
)
