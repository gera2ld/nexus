package account

import (
	"context"
	"net/http"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"github.com/danielgtaylor/huma/v2"

	"nexus/internal/auth"
	"nexus/internal/files"
)

type sessionOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

// registerSession adds the raw-link cookie (docs/WEB.md §3): the same
// token the app sends as a Bearer header, as an HttpOnly cookie that
// /files accepts for GET and HEAD only.
func (h *handlers) registerSession(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-session", Method: http.MethodPost, Path: "/session", Tags: []string{"user"},
		Summary:       "Set the cookie that lets raw file links open in a new tab",
		Description:   "The cookie holds this request's token, is limited to /files/, and expires with the token. /files accepts it for GET and HEAD only.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
		c := from(ctx)
		token := bearerToken(c.req)
		claims, err := security.ParseUnverifiedJWT(token)
		if err != nil {
			return nil, huma.Error401Unauthorized("No token.")
		}
		exp, _ := claims.GetExpirationTime()
		if exp == nil {
			return nil, huma.Error401Unauthorized("The token has no expiry.")
		}
		ck := sessionCookie(c.req, token)
		ck.Expires = exp.Time
		return &sessionOutput{ck}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-session", Method: http.MethodDelete, Path: "/session", Tags: []string{"user"},
		Summary: "Clear the raw-link cookie (on sign-out)", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
		ck := sessionCookie(from(ctx).req, "")
		ck.Expires = time.Unix(0, 0)
		ck.MaxAge = -1
		return &sessionOutput{ck}, nil
	})
}

func sessionCookie(r *http.Request, value string) http.Cookie {
	return http.Cookie{
		Name:     auth.SessionCookie,
		Value:    value,
		Path:     files.Prefix + "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(requestOrigin(r), "https:"),
		SameSite: http.SameSiteLaxMode,
	}
}

// bearerToken is the token PocketBase authenticated this request with; it
// accepts the header with or without the "Bearer " prefix.
func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return h
}
