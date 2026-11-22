// Package principal carries the authenticated user through a request context.
package principal

import "context"

type ctxKey struct{}

// With returns ctx carrying the user's record id.
func With(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, userID)
}

// ID returns the authenticated user's record id, if any.
func ID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKey{}).(string)
	return id, ok && id != ""
}
