// Package admin is the admin dashboard's API under /api/nexus/admin:
// storage accounts, mounts and the users they belong to. It needs a
// PocketBase superuser token (the routes are guarded in internal/account).
// Saves go through the same record hooks as the PocketBase admin UI, so
// option checks and secret encryption live in one place (internal/storage).
package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"ganigeorgiev.com/pocketbase/dbx"
	validation "ganigeorgiev.com/pocketbase/ozzo-validation/v4"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/danielgtaylor/huma/v2"

	"nexus/internal/schema"
	"nexus/internal/storage"
)

// Security is the OpenAPI security requirement of every admin operation.
var Security = []map[string][]string{{"superuser": {}}}

type handlers struct {
	app core.App
	mgr *storage.Manager
}

type body[T any] struct{ Body T }

type idPath struct {
	ID string `path:"id"`
}

// Register adds the admin operations to api.
func Register(api huma.API, app core.App, mgr *storage.Manager) {
	h := &handlers{app: app, mgr: mgr}
	h.registerAccounts(api)
	h.registerMounts(api)
}

func op(id, method, path, summary string) huma.Operation {
	return huma.Operation{
		OperationID: id, Method: method, Path: "/admin" + path, Summary: summary,
		Tags: []string{"admin"}, Security: Security,
	}
}

// ---- storage accounts ----

// StorageAccount is an account as the dashboard shows it; secrets are
// listed by name only.
type StorageAccount struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type" enum:"local,s3"`
	Options map[string]string `json:"options"`
	Secrets []string          `json:"secrets" doc:"Names of the stored secret options"`
	Mounts  int               `json:"mounts" doc:"Mounts using this account"`
}

type typePath struct {
	Type string `path:"type" enum:"local,s3"`
}

type createAccountInput struct {
	Body struct {
		Name    string            `json:"name" minLength:"1" maxLength:"100"`
		Type    string            `json:"type" enum:"local,s3"`
		Options map[string]string `json:"options,omitempty"`
		Secrets map[string]string `json:"secrets,omitempty" doc:"Secret options, stored encrypted"`
	}
}

type updateAccountInput struct {
	ID   string `path:"id"`
	Body struct {
		Name    *string            `json:"name,omitempty" minLength:"1" maxLength:"100"`
		Options *map[string]string `json:"options,omitempty" doc:"Replaces all plain options"`
		Secrets map[string]string  `json:"secrets,omitempty" doc:"Merged into the stored secrets; an empty value removes one"`
	}
}

type browseInput struct {
	ID   string `path:"id"`
	Body struct {
		Path string `json:"path,omitempty" maxLength:"1024" doc:"A remote path as a mount uses it; empty lists the top (/ for local disk, the buckets for S3)"`
	}
}

// Listing is one folder of a storage account.
type Listing struct {
	Path      string          `json:"path"`
	Entries   []storage.Entry `json:"entries"`
	Truncated bool            `json:"truncated" doc:"Only the first 1000 entries are listed"`
}

func (h *handlers) registerAccounts(api huma.API) {
	huma.Register(api, op("list-storage-options", http.MethodGet, "/storage-types/{type}/options",
		"The options a storage type takes, from rclone"),
		func(ctx context.Context, in *typePath) (*body[[]storage.OptionInfo], error) {
			opts, err := storage.Options(in.Type)
			if err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			return &body[[]storage.OptionInfo]{opts}, nil
		})

	huma.Register(api, op("list-storage-accounts", http.MethodGet, "/storage-accounts", "List storage accounts by name"),
		func(ctx context.Context, _ *struct{}) (*body[[]StorageAccount], error) {
			recs, err := h.app.FindRecordsByFilter(schema.StorageAccounts, "", "name", 0, 0)
			if err != nil {
				return nil, err
			}
			out := make([]StorageAccount, 0, len(recs))
			for _, r := range recs {
				v, err := h.accountView(r)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			return &body[[]StorageAccount]{out}, nil
		})

	huma.Register(api, op("get-storage-account", http.MethodGet, "/storage-accounts/{id}", "Get one storage account"),
		func(ctx context.Context, in *idPath) (*body[StorageAccount], error) {
			rec, err := h.findAccount(in.ID)
			if err != nil {
				return nil, err
			}
			return h.accountBody(rec)
		})

	create := op("create-storage-account", http.MethodPost, "/storage-accounts", "Create a storage account")
	create.DefaultStatus = http.StatusCreated
	huma.Register(api, create, func(ctx context.Context, in *createAccountInput) (*body[StorageAccount], error) {
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("Name must not be blank.")
		}
		coll, err := h.app.FindCollectionByNameOrId(schema.StorageAccounts)
		if err != nil {
			return nil, err
		}
		rec := core.NewRecord(coll)
		rec.Set("name", name)
		rec.Set("type", in.Body.Type)
		rec.Set("options", nonEmpty(in.Body.Options))
		rec.Set("secret_options", nonEmpty(in.Body.Secrets))
		if err := h.app.Save(rec); err != nil {
			return nil, saveError(err)
		}
		return h.accountBody(rec)
	})

	huma.Register(api, op("update-storage-account", http.MethodPatch, "/storage-accounts/{id}",
		"Rename a storage account or change its options and secrets"),
		func(ctx context.Context, in *updateAccountInput) (*body[StorageAccount], error) {
			rec, err := h.findAccount(in.ID)
			if err != nil {
				return nil, err
			}
			if in.Body.Name != nil {
				name := strings.TrimSpace(*in.Body.Name)
				if name == "" {
					return nil, huma.Error422UnprocessableEntity("Name must not be blank.")
				}
				rec.Set("name", name)
			}
			if in.Body.Options != nil {
				rec.Set("options", nonEmpty(*in.Body.Options))
			}
			if len(in.Body.Secrets) > 0 {
				secrets, err := h.mgr.Secrets(rec)
				if err != nil {
					return nil, err
				}
				for k, v := range in.Body.Secrets {
					if v == "" {
						delete(secrets, k)
					} else {
						secrets[k] = v
					}
				}
				if len(secrets) == 0 {
					// The save hook keeps stored secrets when none are
					// given, so removing the last one is done here.
					rec.Set("secrets_enc", "")
				} else {
					rec.Set("secret_options", secrets)
				}
			}
			if err := h.app.Save(rec); err != nil {
				return nil, saveError(err)
			}
			return h.accountBody(rec)
		})

	del := op("delete-storage-account", http.MethodDelete, "/storage-accounts/{id}",
		"Delete a storage account that no mount uses")
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *idPath) (*struct{}, error) {
		rec, err := h.findAccount(in.ID)
		if err != nil {
			return nil, err
		}
		n, err := h.app.CountRecords(schema.Mounts, dbx.HashExp{"account": rec.Id})
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, huma.Error409Conflict(fmt.Sprintf("%s is used by %s; delete those first.", rec.GetString("name"), plural(int(n), "mount")))
		}
		return nil, h.app.Delete(rec)
	})

	huma.Register(api, op("browse-storage-account", http.MethodPost, "/storage-accounts/{id}/browse",
		"List a folder of a storage account; tests the connection and picks remote paths"),
		func(ctx context.Context, in *browseInput) (*body[Listing], error) {
			rec, err := h.findAccount(in.ID)
			if err != nil {
				return nil, err
			}
			p := strings.TrimSpace(in.Body.Path)
			if p == "" && rec.GetString("type") == "local" {
				p = "/"
			}
			entries, truncated, err := h.mgr.Browse(ctx, rec, p)
			switch {
			case errors.Is(err, storage.ErrBrowseNotFound):
				return nil, huma.Error404NotFound("No such folder: " + p)
			case err != nil:
				// The backend's own message is what the admin needs to
				// fix the settings (wrong key, unknown bucket, …).
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return &body[Listing]{Listing{Path: p, Entries: entries, Truncated: truncated}}, nil
		})
}

func (h *handlers) findAccount(id string) (*core.Record, error) {
	rec, err := h.app.FindRecordById(schema.StorageAccounts, id)
	if err != nil {
		return nil, huma.Error404NotFound("No such storage account.")
	}
	return rec, nil
}

func (h *handlers) accountView(r *core.Record) (StorageAccount, error) {
	opts := map[string]string{}
	if err := r.UnmarshalJSONField("options", &opts); err != nil || opts == nil {
		opts = map[string]string{}
	}
	secrets, err := h.mgr.Secrets(r)
	if err != nil {
		return StorageAccount{}, err
	}
	names := make([]string, 0, len(secrets))
	for k := range secrets {
		names = append(names, k)
	}
	sort.Strings(names)
	n, err := h.app.CountRecords(schema.Mounts, dbx.HashExp{"account": r.Id})
	if err != nil {
		return StorageAccount{}, err
	}
	return StorageAccount{
		ID: r.Id, Name: r.GetString("name"), Type: r.GetString("type"),
		Options: opts, Secrets: names, Mounts: int(n),
	}, nil
}

func (h *handlers) accountBody(r *core.Record) (*body[StorageAccount], error) {
	v, err := h.accountView(r)
	if err != nil {
		return nil, err
	}
	return &body[StorageAccount]{v}, nil
}

// ---- users and mounts ----

// AdminUser is a user as the mount form lists them.
type AdminUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// AccountRef names a related record.
type AccountRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AdminMount is a mount as the dashboard shows it.
type AdminMount struct {
	ID         string     `json:"id"`
	User       AdminUser  `json:"user"`
	Account    AccountRef `json:"account"`
	Name       string     `json:"name" doc:"The folder name under /dav/files/{user}/"`
	RemotePath string     `json:"remotePath"`
	ReadOnly   bool       `json:"readOnly"`
}

type createMountInput struct {
	Body struct {
		User       string `json:"user" minLength:"1"`
		Account    string `json:"account" minLength:"1"`
		Name       string `json:"name" pattern:"^[a-z0-9][a-z0-9._-]*$" patternDescription:"lower-case letters, digits, . _ and -, starting with a letter or digit" maxLength:"64"`
		RemotePath string `json:"remotePath" maxLength:"1024" doc:"An absolute directory for local disk; bucket or bucket/prefix for S3, or empty for all buckets"`
		ReadOnly   bool   `json:"readOnly,omitempty"`
	}
}

type updateMountInput struct {
	ID   string `path:"id"`
	Body struct {
		Account    *string `json:"account,omitempty" minLength:"1"`
		Name       *string `json:"name,omitempty" pattern:"^[a-z0-9][a-z0-9._-]*$" patternDescription:"lower-case letters, digits, . _ and -, starting with a letter or digit" maxLength:"64"`
		RemotePath *string `json:"remotePath,omitempty" maxLength:"1024"`
		ReadOnly   *bool   `json:"readOnly,omitempty"`
	}
}

func (h *handlers) registerMounts(api huma.API) {
	huma.Register(api, op("list-users", http.MethodGet, "/users", "List users by email"),
		func(ctx context.Context, _ *struct{}) (*body[[]AdminUser], error) {
			recs, err := h.app.FindRecordsByFilter(schema.Users, "", "email", 0, 0)
			if err != nil {
				return nil, err
			}
			out := make([]AdminUser, 0, len(recs))
			for _, r := range recs {
				out = append(out, userView(r))
			}
			return &body[[]AdminUser]{out}, nil
		})

	huma.Register(api, op("list-mounts", http.MethodGet, "/mounts", "List every user's mounts, by user and name"),
		func(ctx context.Context, _ *struct{}) (*body[[]AdminMount], error) {
			recs, err := h.app.FindRecordsByFilter(schema.Mounts, "", "name", 0, 0)
			if err != nil {
				return nil, err
			}
			if errs := h.app.ExpandRecords(recs, []string{"user", "account"}, nil); len(errs) > 0 {
				for _, e := range errs {
					return nil, e
				}
			}
			out := make([]AdminMount, 0, len(recs))
			for _, r := range recs {
				out = append(out, mountView(r))
			}
			sort.SliceStable(out, func(i, j int) bool { return out[i].User.Email < out[j].User.Email })
			return &body[[]AdminMount]{out}, nil
		})

	create := op("create-mount", http.MethodPost, "/mounts", "Give a user a mount")
	create.DefaultStatus = http.StatusCreated
	huma.Register(api, create, func(ctx context.Context, in *createMountInput) (*body[AdminMount], error) {
		coll, err := h.app.FindCollectionByNameOrId(schema.Mounts)
		if err != nil {
			return nil, err
		}
		rec := core.NewRecord(coll)
		rec.Set("user", in.Body.User)
		rec.Set("account", in.Body.Account)
		rec.Set("name", in.Body.Name)
		rec.Set("remote_path", strings.TrimSpace(in.Body.RemotePath))
		rec.Set("read_only", in.Body.ReadOnly)
		if err := h.app.Save(rec); err != nil {
			return nil, saveError(err)
		}
		return h.mountBody(rec)
	})

	huma.Register(api, op("update-mount", http.MethodPatch, "/mounts/{id}",
		"Change a mount; its user stays the same"),
		func(ctx context.Context, in *updateMountInput) (*body[AdminMount], error) {
			rec, err := h.findMount(in.ID)
			if err != nil {
				return nil, err
			}
			b := in.Body
			if b.Account != nil {
				rec.Set("account", *b.Account)
			}
			if b.Name != nil {
				rec.Set("name", *b.Name)
			}
			if b.RemotePath != nil {
				rec.Set("remote_path", strings.TrimSpace(*b.RemotePath))
			}
			if b.ReadOnly != nil {
				rec.Set("read_only", *b.ReadOnly)
			}
			if err := h.app.Save(rec); err != nil {
				return nil, saveError(err)
			}
			return h.mountBody(rec)
		})

	del := op("delete-mount", http.MethodDelete, "/mounts/{id}",
		"Remove a mount; the files themselves are not touched")
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *idPath) (*struct{}, error) {
		rec, err := h.findMount(in.ID)
		if err != nil {
			return nil, err
		}
		return nil, h.app.Delete(rec)
	})
}

func (h *handlers) findMount(id string) (*core.Record, error) {
	rec, err := h.app.FindRecordById(schema.Mounts, id)
	if err != nil {
		return nil, huma.Error404NotFound("No such mount.")
	}
	return rec, nil
}

func (h *handlers) mountBody(rec *core.Record) (*body[AdminMount], error) {
	if errs := h.app.ExpandRecord(rec, []string{"user", "account"}, nil); len(errs) > 0 {
		for _, e := range errs {
			return nil, e
		}
	}
	return &body[AdminMount]{mountView(rec)}, nil
}

func userView(r *core.Record) AdminUser {
	return AdminUser{ID: r.Id, Email: r.Email(), Name: r.GetString("name")}
}

func mountView(r *core.Record) AdminMount {
	m := AdminMount{
		ID: r.Id, Name: r.GetString("name"), RemotePath: r.GetString("remote_path"),
		ReadOnly: r.GetBool("read_only"),
		User:     AdminUser{ID: r.GetString("user")},
		Account:  AccountRef{ID: r.GetString("account")},
	}
	if u := r.ExpandedOne("user"); u != nil {
		m.User = userView(u)
	}
	if a := r.ExpandedOne("account"); a != nil {
		m.Account.Name = a.GetString("name")
	}
	return m
}

// ---- helpers ----

func nonEmpty(m map[string]string) any {
	if len(m) == 0 {
		return nil
	}
	return m
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// fieldNames are the API's names for record fields in error messages.
var fieldNames = map[string]string{
	"name": "Name", "type": "Type", "options": "Options", "secret_options": "Secrets",
	"user": "User", "account": "Storage account", "remote_path": "Remote path", "read_only": "Read-only",
}

// saveError turns a record validation failure (from PocketBase or the
// storage hooks) into a 422 that names the field.
func saveError(err error) error {
	var ve validation.Errors
	if !errors.As(err, &ve) {
		return err
	}
	keys := make([]string, 0, len(ve))
	for k := range ve {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	msgs := make([]string, 0, len(keys))
	for _, k := range keys {
		label := fieldNames[k]
		if label == "" {
			label = k
		}
		msgs = append(msgs, label+": "+strings.TrimSuffix(ve[k].Error(), "."))
	}
	return huma.Error422UnprocessableEntity(strings.Join(msgs, "; ") + ".")
}
