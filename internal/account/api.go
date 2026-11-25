// Package account serves the user dashboard's API at /api/nexus: the
// signed-in user, App Passwords, calendars and address books. It is built
// with Huma, which also generates the OpenAPI document the web app's types
// come from (api/openapi.json). Requests use PocketBase's own user auth (a
// token from auth-with-password); app_passwords itself stays superuser-only,
// so secret hashes never leave the server.
package account

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"nexus/internal/admin"
	"nexus/internal/auth"
	"nexus/internal/schema"
	"nexus/internal/storage"
	"nexus/internal/store"
)

// Prefix is where the API is mounted.
const Prefix = "/api/nexus"

// MaxPasswordsPerUser bounds active App Passwords per account.
const MaxPasswordsPerUser = 50

type ctxKey struct{}

type caller struct {
	user *core.Record
	req  *http.Request
}

// Every array the API returns is a real array, never null, so the web
// app's types needn't allow null.
func init() { huma.DefaultArrayNullable = false }

func from(ctx context.Context) caller { c, _ := ctx.Value(ctxKey{}).(caller); return c }

// handlers holds what the operations need.
type handlers struct {
	app core.App
	st  *store.Store
}

// NewAPI builds the Huma API: the user dashboard's operations and the
// admin dashboard's (internal/admin). The returned handler serves every
// path under Prefix and expects the signed-in user from serve.
func NewAPI(app core.App, st *store.Store, mgr *storage.Manager) (huma.API, *http.ServeMux) {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Nexus API", "1")
	cfg.Info.Description = "The dashboards' API. Sign in with PocketBase (users " +
		"auth-with-password, or _superusers for /admin) and send the token as a Bearer header."
	cfg.DocsPath = "" // no docs UI: it would load scripts from a CDN
	cfg.Servers = []*huma.Server{{URL: Prefix}}
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer":    {Type: "http", Scheme: "bearer", Description: "PocketBase user token"},
		"superuser": {Type: "http", Scheme: "bearer", Description: "PocketBase superuser token"},
	}
	cfg.Security = []map[string][]string{{"bearer": {}}}
	api := humago.NewWithPrefix(mux, Prefix, cfg)
	h := &handlers{app: app, st: st}
	h.registerUser(api)
	h.registerCollections(api)
	h.registerSession(api)
	admin.Register(api, app, mgr)
	return api, mux
}

// Register mounts the API on r.
func Register(r *router.Router[*core.RequestEvent], app core.App, st *store.Store, mgr *storage.Manager) {
	_, mux := NewAPI(app, st, mgr)
	serve := func(e *core.RequestEvent) error {
		ctx := context.WithValue(e.Request.Context(), ctxKey{}, caller{user: e.Auth, req: e.Request})
		mux.ServeHTTP(e.Response, e.Request.WithContext(ctx))
		return nil
	}
	// The OpenAPI document is public; /admin needs a superuser and
	// everything else a user. Neither token works on the other's routes.
	r.GET(Prefix+"/openapi.json", serve)
	r.GET(Prefix+"/openapi.yaml", serve)
	r.GET(Prefix+"/schemas/{path...}", serve)
	r.Any(Prefix+"/admin/{path...}", serve).Bind(apis.RequireSuperuserAuth())
	r.Any(Prefix+"/{path...}", serve).Bind(apis.RequireAuth(schema.Users))
}

// ---- the signed-in user ----

type Mount struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly"`
	Type     string `json:"type" enum:"local,s3,webdav" doc:"The storage account's type, for the mount's icon"`
}

type Me struct {
	ID     string  `json:"id"`
	Email  string  `json:"email"`
	Server string  `json:"server" doc:"Origin clients should use, e.g. https://dav.example.com"`
	Mounts []Mount `json:"mounts"`
}

// AppPassword is what the API returns; never the hash.
type AppPassword struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Scopes     []string `json:"scopes" enum:"cal,card,files"`
	Paths      []string `json:"paths" doc:"Path limits; empty means none"`
	Created    string   `json:"created"`
	LastUsedAt string   `json:"lastUsedAt"`
	RevokedAt  string   `json:"revokedAt"`
	// Secret is set only in the create response.
	Secret string `json:"secret,omitempty" doc:"Only in the create response; shown once"`
}

func view(r *core.Record) AppPassword {
	return AppPassword{
		ID:         r.Id,
		Label:      r.GetString("label"),
		Scopes:     nonNil(r.GetStringSlice("scopes")),
		Paths:      nonNil(r.GetStringSlice("paths")),
		Created:    r.GetString("created"),
		LastUsedAt: r.GetString("last_used_at"),
		RevokedAt:  r.GetString("revoked_at"),
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type body[T any] struct{ Body T }

type idPath struct {
	ID string `path:"id"`
}

type createPasswordInput struct {
	Body struct {
		Label  string   `json:"label" minLength:"1" maxLength:"100"`
		Scopes []string `json:"scopes" minItems:"1" enum:"cal,card,files"`
		Paths  []string `json:"paths,omitempty" doc:"Optional path limits, e.g. /calendars/personal/ or /files/docs/. A /dav prefix and your user id are removed; invalid ones are refused (422)."`
	}
}

type updatePasswordInput struct {
	ID   string `path:"id"`
	Body struct {
		Label  *string   `json:"label,omitempty" minLength:"1" maxLength:"100"`
		Scopes []string  `json:"scopes,omitempty" minItems:"1" enum:"cal,card,files"`
		Paths  *[]string `json:"paths,omitempty" doc:"Omit to keep; [] removes the limit"`
	}
}

func (h *handlers) registerUser(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-me", Method: http.MethodGet, Path: "/me", Tags: []string{"user"},
		Summary: "The signed-in user, the server address and their mounts",
	}, h.me)
	huma.Register(api, huma.Operation{
		OperationID: "list-app-passwords", Method: http.MethodGet, Path: "/app-passwords", Tags: []string{"app passwords"},
		Summary: "List App Passwords, newest first, including revoked ones",
	}, h.listPasswords)
	huma.Register(api, huma.Operation{
		OperationID: "create-app-password", Method: http.MethodPost, Path: "/app-passwords", Tags: []string{"app passwords"},
		Summary: "Create an App Password; the secret is in this response only", DefaultStatus: http.StatusCreated,
	}, h.createPassword)
	huma.Register(api, huma.Operation{
		OperationID: "update-app-password", Method: http.MethodPatch, Path: "/app-passwords/{id}", Tags: []string{"app passwords"},
		Summary: "Change an App Password's label, scopes or paths; the secret stays the same",
	}, h.updatePassword)
	huma.Register(api, huma.Operation{
		OperationID: "revoke-app-password", Method: http.MethodPost, Path: "/app-passwords/{id}/revoke", Tags: []string{"app passwords"},
		Summary: "Revoke an App Password; it stops working at once",
	}, h.revokePassword)
}

func (h *handlers) me(ctx context.Context, _ *struct{}) (*body[Me], error) {
	c := from(ctx)
	recs, err := h.app.FindRecordsByFilter(schema.Mounts, "user = {:u}", "name", 0, 0, dbx.Params{"u": c.user.Id})
	if err != nil {
		return nil, err
	}
	if errs := h.app.ExpandRecords(recs, []string{"account"}, nil); len(errs) > 0 {
		for _, err := range errs {
			return nil, err
		}
	}
	mounts := make([]Mount, 0, len(recs))
	for _, r := range recs {
		m := Mount{Name: r.GetString("name"), ReadOnly: r.GetBool("read_only")}
		if a := r.ExpandedOne("account"); a != nil {
			m.Type = a.GetString("type")
		}
		mounts = append(mounts, m)
	}
	return &body[Me]{Me{ID: c.user.Id, Email: c.user.Email(), Server: requestOrigin(c.req), Mounts: mounts}}, nil
}

func (h *handlers) listPasswords(ctx context.Context, _ *struct{}) (*body[[]AppPassword], error) {
	recs, err := h.app.FindRecordsByFilter(schema.AppPasswords, "user = {:u}", "-created", 0, 0,
		dbx.Params{"u": from(ctx).user.Id})
	if err != nil {
		return nil, err
	}
	out := make([]AppPassword, 0, len(recs))
	for _, r := range recs {
		out = append(out, view(r))
	}
	return &body[[]AppPassword]{out}, nil
}

func (h *handlers) createPassword(ctx context.Context, in *createPasswordInput) (*body[AppPassword], error) {
	uid := from(ctx).user.Id
	label := strings.TrimSpace(in.Body.Label)
	if label == "" {
		return nil, huma.Error422UnprocessableEntity("Label must not be blank.")
	}
	scopes := cleanScopes(in.Body.Scopes)
	paths, err := auth.NormalizePaths(in.Body.Paths, uid)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	active, err := h.app.CountRecords(schema.AppPasswords, dbx.HashExp{"user": uid, "revoked_at": ""})
	if err != nil {
		return nil, err
	}
	if active >= MaxPasswordsPerUser {
		return nil, huma.Error422UnprocessableEntity("Too many active App Passwords; revoke some first.")
	}
	coll, err := h.app.FindCollectionByNameOrId(schema.AppPasswords)
	if err != nil {
		return nil, err
	}
	secret := auth.NewSecret()
	rec := core.NewRecord(coll)
	rec.Set("user", uid)
	rec.Set("label", label)
	rec.Set("scopes", scopes)
	rec.Set("paths", paths)
	rec.Set("secret_hash", auth.HashSecret(secret))
	if err := h.app.Save(rec); err != nil {
		return nil, err
	}
	v := view(rec)
	v.Secret = secret
	return &body[AppPassword]{v}, nil
}

func (h *handlers) findPassword(ctx context.Context, id string) (*core.Record, error) {
	rec, err := h.app.FindFirstRecordByFilter(schema.AppPasswords, "id = {:id} && user = {:u}",
		dbx.Params{"id": id, "u": from(ctx).user.Id})
	if err != nil {
		return nil, huma.Error404NotFound("No such App Password.") // also for other users' passwords
	}
	return rec, nil
}

func (h *handlers) updatePassword(ctx context.Context, in *updatePasswordInput) (*body[AppPassword], error) {
	rec, err := h.findPassword(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if rec.GetString("revoked_at") != "" {
		return nil, huma.Error409Conflict("This App Password is revoked.")
	}
	if in.Body.Label != nil {
		label := strings.TrimSpace(*in.Body.Label)
		if label == "" {
			return nil, huma.Error422UnprocessableEntity("Label must not be blank.")
		}
		rec.Set("label", label)
	}
	if in.Body.Scopes != nil {
		rec.Set("scopes", cleanScopes(in.Body.Scopes))
	}
	if in.Body.Paths != nil {
		paths, err := auth.NormalizePaths(*in.Body.Paths, from(ctx).user.Id)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		rec.Set("paths", paths)
	}
	if err := h.app.Save(rec); err != nil {
		return nil, err
	}
	return &body[AppPassword]{view(rec)}, nil
}

func (h *handlers) revokePassword(ctx context.Context, in *idPath) (*body[AppPassword], error) {
	rec, err := h.findPassword(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if rec.GetString("revoked_at") == "" {
		dt, _ := types.ParseDateTime(time.Now().UTC())
		rec.Set("revoked_at", dt)
		if err := h.app.Save(rec); err != nil {
			return nil, err
		}
	}
	return &body[AppPassword]{view(rec)}, nil
}

// cleanScopes dedupes and sorts; Huma has already checked the values.
func cleanScopes(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
