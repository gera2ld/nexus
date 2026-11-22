// Package account serves the user's account page and the small API it uses
// to create and revoke App Passwords. These routes use PocketBase's own user
// auth (a token from auth-with-password); app_passwords itself stays
// superuser-only, so secret hashes never leave the server.
package account

import (
	_ "embed"
	"net/http"
	"sort"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"

	"nexus/internal/auth"
	"nexus/internal/schema"
)

// MaxPasswordsPerUser bounds active App Passwords per account.
const MaxPasswordsPerUser = 50

//go:embed page.html
var page string

// AppPasswordView is what the API returns; never the hash.
type AppPasswordView struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Scopes     []string `json:"scopes"`
	Created    string   `json:"created"`
	LastUsedAt string   `json:"lastUsedAt"`
	RevokedAt  string   `json:"revokedAt"`
	// Secret is set only in the create response.
	Secret string `json:"secret,omitempty"`
}

func view(r *core.Record) AppPasswordView {
	return AppPasswordView{
		ID:         r.Id,
		Label:      r.GetString("label"),
		Scopes:     r.GetStringSlice("scopes"),
		Created:    r.GetString("created"),
		LastUsedAt: r.GetString("last_used_at"),
		RevokedAt:  r.GetString("revoked_at"),
	}
}

// Register mounts /account and /api/nexus/* on r.
func Register(r *router.Router[*core.RequestEvent]) {
	r.GET("/account", func(e *core.RequestEvent) error {
		e.Response.Header().Set("Cache-Control", "no-store")
		e.Response.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		return e.HTML(http.StatusOK, page)
	})

	g := r.Group("/api/nexus")
	g.Bind(apis.RequireAuth(schema.Users))
	g.GET("/me", me)
	g.GET("/app-passwords", list)
	g.POST("/app-passwords", create)
	g.POST("/app-passwords/{id}/revoke", revoke)
}

func me(e *core.RequestEvent) error {
	return e.JSON(http.StatusOK, map[string]string{
		"id":     e.Auth.Id,
		"email":  e.Auth.Email(),
		"server": requestOrigin(e.Request),
	})
}

func list(e *core.RequestEvent) error {
	recs, err := e.App.FindRecordsByFilter(schema.AppPasswords, "user = {:u}", "-created", 0, 0,
		dbx.Params{"u": e.Auth.Id})
	if err != nil {
		return e.InternalServerError("", err)
	}
	out := make([]AppPasswordView, 0, len(recs))
	for _, r := range recs {
		out = append(out, view(r))
	}
	return e.JSON(http.StatusOK, out)
}

type createReq struct {
	Label  string   `json:"label"`
	Scopes []string `json:"scopes"`
}

func create(e *core.RequestEvent) error {
	var req createReq
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("Invalid request body.", err)
	}
	label := strings.TrimSpace(req.Label)
	if label == "" || len([]rune(label)) > 100 {
		return e.BadRequestError("Label must be 1–100 characters.", nil)
	}
	scopes, ok := cleanScopes(req.Scopes)
	if !ok {
		return e.BadRequestError("Choose at least one of: cal, card, files.", nil)
	}
	active, err := e.App.CountRecords(schema.AppPasswords,
		dbx.HashExp{"user": e.Auth.Id, "revoked_at": ""})
	if err != nil {
		return e.InternalServerError("", err)
	}
	if active >= MaxPasswordsPerUser {
		return e.BadRequestError("Too many active App Passwords; revoke some first.", nil)
	}

	coll, err := e.App.FindCollectionByNameOrId(schema.AppPasswords)
	if err != nil {
		return e.InternalServerError("", err)
	}
	secret := auth.NewSecret()
	rec := core.NewRecord(coll)
	rec.Set("user", e.Auth.Id)
	rec.Set("label", label)
	rec.Set("scopes", scopes)
	rec.Set("secret_hash", auth.HashSecret(secret))
	if err := e.App.Save(rec); err != nil {
		return e.InternalServerError("", err)
	}
	v := view(rec)
	v.Secret = secret
	return e.JSON(http.StatusCreated, v)
}

func revoke(e *core.RequestEvent) error {
	rec, err := e.App.FindFirstRecordByFilter(schema.AppPasswords, "id = {:id} && user = {:u}",
		dbx.Params{"id": e.Request.PathValue("id"), "u": e.Auth.Id})
	if err != nil {
		return e.NotFoundError("", nil) // also for other users' passwords
	}
	if rec.GetString("revoked_at") == "" {
		dt, _ := types.ParseDateTime(time.Now().UTC())
		rec.Set("revoked_at", dt)
		if err := e.App.Save(rec); err != nil {
			return e.InternalServerError("", err)
		}
	}
	return e.JSON(http.StatusOK, view(rec))
}

// cleanScopes dedupes and validates; ok is false if none are valid or any
// is unknown.
func cleanScopes(in []string) ([]string, bool) {
	valid := map[string]bool{}
	for _, s := range auth.AllScopes {
		valid[s] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !valid[s] {
			return nil, false
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out, len(out) > 0
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
