package account

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/danielgtaylor/huma/v2"

	webdav "nexus/internal/dav"
	"nexus/internal/dav/caldav"
	"nexus/internal/dav/carddav"
	"nexus/internal/principal"
	"nexus/internal/schema"
)

// Collection is a calendar or address book as the dashboard shows it.
type Collection struct {
	Slug        string   `json:"slug" doc:"URL name, fixed at creation"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Color       string   `json:"color,omitempty" doc:"Calendars only: #rrggbb or #rrggbbaa"`
	Components  []string `json:"components,omitempty" enum:"VEVENT,VTODO" doc:"Calendars only: what it holds"`
	Items       int      `json:"items" doc:"Events and tasks, or contacts"`
	URL         string   `json:"url" doc:"The collection's DAV address"`
}

type slugPath struct {
	Slug string `path:"slug"`
}

type createCalendarInput struct {
	Body struct {
		Name        string   `json:"name" minLength:"1" maxLength:"255"`
		Description string   `json:"description,omitempty" maxLength:"2000"`
		Color       string   `json:"color,omitempty" pattern:"^#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$"`
		Components  []string `json:"components,omitempty" enum:"VEVENT,VTODO" doc:"Default: events and tasks"`
	}
}

type updateCalendarInput struct {
	Slug string `path:"slug"`
	Body struct {
		Name        *string `json:"name,omitempty" minLength:"1" maxLength:"255"`
		Description *string `json:"description,omitempty" maxLength:"2000"`
		Color       *string `json:"color,omitempty" pattern:"^(#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?)?$" doc:"Empty removes the colour"`
	}
}

type createAddressbookInput struct {
	Body struct {
		Name        string `json:"name" minLength:"1" maxLength:"255"`
		Description string `json:"description,omitempty" maxLength:"2000"`
	}
}

type updateAddressbookInput struct {
	Slug string `path:"slug"`
	Body struct {
		Name        *string `json:"name,omitempty" minLength:"1" maxLength:"255"`
		Description *string `json:"description,omitempty" maxLength:"2000"`
	}
}

// kind describes one collection type for the shared handlers.
type kind struct {
	collection, objects, parent, seg, tag, noun string
	extra                                       func(rec *core.Record, v *Collection)
}

var (
	calendars = kind{schema.Calendars, schema.CalendarObjects, "calendar", "calendars", "calendars", "calendar",
		func(rec *core.Record, v *Collection) {
			v.Color = rec.GetString("color")
			v.Components = rec.GetStringSlice("components")
		}}
	addressbooks = kind{schema.Addressbooks, schema.Contacts, "addressbook", "contacts", "address books", "address book", nil}
)

func (k kind) path(uid, slug string) string { return "/dav/" + uid + "/" + k.seg + "/" + slug + "/" }

func (h *handlers) find(ctx context.Context, k kind, slug string) (*core.Record, error) {
	rec, err := h.app.FindFirstRecordByFilter(k.collection, "owner = {:u} && slug = {:s}",
		dbx.Params{"u": from(ctx).user.Id, "s": slug})
	if err != nil {
		return nil, huma.Error404NotFound("No such " + k.noun + ".")
	}
	return rec, nil
}

func (h *handlers) toView(ctx context.Context, k kind, rec *core.Record) (Collection, error) {
	n, err := h.app.CountRecords(k.objects, dbx.HashExp{k.parent: rec.Id})
	if err != nil {
		return Collection{}, err
	}
	c := from(ctx)
	v := Collection{
		Slug:        rec.GetString("slug"),
		Name:        rec.GetString("name"),
		Description: rec.GetString("description"),
		Items:       int(n),
		URL:         requestOrigin(c.req) + k.path(c.user.Id, rec.GetString("slug")),
	}
	if k.extra != nil {
		k.extra(rec, &v)
	}
	return v, nil
}

func (h *handlers) viewSlug(ctx context.Context, k kind, slug string) (*body[Collection], error) {
	rec, err := h.find(ctx, k, slug)
	if err != nil {
		return nil, err
	}
	v, err := h.toView(ctx, k, rec)
	if err != nil {
		return nil, err
	}
	return &body[Collection]{v}, nil
}

func (h *handlers) registerCollections(api huma.API) {
	for _, k := range []kind{calendars, addressbooks} {
		base := "/" + k.collection
		huma.Register(api, huma.Operation{
			OperationID: "list-" + k.collection, Method: http.MethodGet, Path: base, Tags: []string{k.tag},
			Summary: "List your " + k.tag + ", oldest first",
		}, func(ctx context.Context, _ *struct{}) (*body[[]Collection], error) {
			recs, err := h.app.FindRecordsByFilter(k.collection, "owner = {:u}", "created", 0, 0,
				dbx.Params{"u": from(ctx).user.Id})
			if err != nil {
				return nil, err
			}
			out := make([]Collection, 0, len(recs))
			for _, r := range recs {
				v, err := h.toView(ctx, k, r)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			return &body[[]Collection]{out}, nil
		})
		huma.Register(api, huma.Operation{
			OperationID: "get-" + k.noun2(), Method: http.MethodGet, Path: base + "/{slug}", Tags: []string{k.tag},
			Summary: "Get one " + k.noun,
		}, func(ctx context.Context, in *slugPath) (*body[Collection], error) {
			return h.viewSlug(ctx, k, in.Slug)
		})
		huma.Register(api, huma.Operation{
			OperationID: "delete-" + k.noun2(), Method: http.MethodDelete, Path: base + "/{slug}", Tags: []string{k.tag},
			Summary: "Delete a " + k.noun + " and everything in it", DefaultStatus: http.StatusNoContent,
			Description: "DAV clients can't delete collections; this is the user's way to do it.",
		}, func(ctx context.Context, in *slugPath) (*struct{}, error) {
			rec, err := h.find(ctx, k, in.Slug)
			if err != nil {
				return nil, err
			}
			// Cascades to the objects; a hook in internal/nexus removes
			// the change log.
			return nil, h.app.Delete(rec)
		})
	}

	huma.Register(api, huma.Operation{
		OperationID: "create-calendar", Method: http.MethodPost, Path: "/calendars", Tags: []string{"calendars"},
		Summary: "Create a calendar; its URL name comes from the name", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createCalendarInput) (*body[Collection], error) {
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("Name must not be blank.")
		}
		uid := from(ctx).user.Id
		slug := h.newSlug(ctx, calendars, name)
		err := h.st.CalDAV().CreateCalendar(userCtx(ctx), &caldav.Calendar{
			Path:                  calendars.path(uid, slug),
			Name:                  name,
			Description:           strings.TrimSpace(in.Body.Description),
			Color:                 in.Body.Color,
			SupportedComponentSet: in.Body.Components,
		})
		if err != nil {
			return nil, storeError(err)
		}
		return h.viewSlug(ctx, calendars, slug)
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-calendar", Method: http.MethodPatch, Path: "/calendars/{slug}", Tags: []string{"calendars"},
		Summary:     "Rename, re-describe or recolour a calendar",
		Description: "What it holds (events, tasks) is fixed at creation.",
	}, func(ctx context.Context, in *updateCalendarInput) (*body[Collection], error) {
		if _, err := h.find(ctx, calendars, in.Slug); err != nil {
			return nil, err
		}
		name, err := trimName(in.Body.Name)
		if err != nil {
			return nil, err
		}
		err = h.st.CalDAV().UpdateCalendar(userCtx(ctx), calendars.path(from(ctx).user.Id, in.Slug),
			&caldav.CalendarUpdate{Name: name, Description: in.Body.Description, Color: in.Body.Color})
		if err != nil {
			return nil, storeError(err)
		}
		return h.viewSlug(ctx, calendars, in.Slug)
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-addressbook", Method: http.MethodPost, Path: "/addressbooks", Tags: []string{"address books"},
		Summary: "Create an address book; its URL name comes from the name", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createAddressbookInput) (*body[Collection], error) {
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("Name must not be blank.")
		}
		uid := from(ctx).user.Id
		slug := h.newSlug(ctx, addressbooks, name)
		err := h.st.CardDAV().CreateAddressBook(userCtx(ctx), &carddav.AddressBook{
			Path:        addressbooks.path(uid, slug),
			Name:        name,
			Description: strings.TrimSpace(in.Body.Description),
		})
		if err != nil {
			return nil, storeError(err)
		}
		return h.viewSlug(ctx, addressbooks, slug)
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-addressbook", Method: http.MethodPatch, Path: "/addressbooks/{slug}", Tags: []string{"address books"},
		Summary: "Rename or re-describe an address book",
	}, func(ctx context.Context, in *updateAddressbookInput) (*body[Collection], error) {
		if _, err := h.find(ctx, addressbooks, in.Slug); err != nil {
			return nil, err
		}
		name, err := trimName(in.Body.Name)
		if err != nil {
			return nil, err
		}
		err = h.st.CardDAV().UpdateAddressBook(userCtx(ctx), addressbooks.path(from(ctx).user.Id, in.Slug),
			&carddav.AddressBookUpdate{Name: name, Description: in.Body.Description})
		if err != nil {
			return nil, storeError(err)
		}
		return h.viewSlug(ctx, addressbooks, in.Slug)
	})
}

// noun2 is the noun as an operation id: "calendar", "addressbook".
func (k kind) noun2() string { return strings.ReplaceAll(k.noun, " ", "") }

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// newSlug makes a URL name from the display name, unique for this user.
func (h *handlers) newSlug(ctx context.Context, k kind, name string) string {
	base := strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
	}
	if base == "" {
		base = k.noun2()
	}
	slug := base
	for i := 2; ; i++ {
		if _, err := h.app.FindFirstRecordByFilter(k.collection, "owner = {:u} && slug = {:s}",
			dbx.Params{"u": from(ctx).user.Id, "s": slug}); err != nil {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

func trimName(p *string) (*string, error) {
	if p == nil {
		return nil, nil
	}
	name := strings.TrimSpace(*p)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("Name must not be blank.")
	}
	return &name, nil
}

// storeError keeps a store error's status (403 for journals-only
// calendars, 400 for a bad colour, …).
func storeError(err error) error {
	code, msg := webdav.HTTPErrorStatus(err)
	if code >= 500 {
		return err
	}
	return huma.NewError(code, strings.TrimPrefix(msg, "store: "))
}

// userCtx gives the store the principal it expects from DAV requests.
func userCtx(ctx context.Context) context.Context {
	return principal.With(ctx, from(ctx).user.Id)
}
