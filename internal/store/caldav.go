package store

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
	"github.com/emersion/go-ical"

	"nexus/internal/dav/caldav"
)

// CalDAV is the caldav.Backend (plus SyncBackend and UpdateBackend).
type CalDAV struct{ s *Store }

func (s *Store) CalDAV() *CalDAV { return &CalDAV{s} }

var (
	_ caldav.Backend       = (*CalDAV)(nil)
	_ caldav.SyncBackend   = (*CalDAV)(nil)
	_ caldav.UpdateBackend = (*CalDAV)(nil)
)

func (b *CalDAV) CurrentUserPrincipal(ctx context.Context) (string, error) {
	uid, err := userID(ctx)
	return principalPath(uid), err
}

func (b *CalDAV) CalendarHomeSetPath(ctx context.Context) (string, error) {
	uid, err := userID(ctx)
	return homePath(uid, calKind), err
}

func (b *CalDAV) toCalendar(uid string, rec *core.Record) caldav.Calendar {
	return caldav.Calendar{
		Path:                  collectionPath(uid, calKind, rec.GetString("slug")),
		Name:                  rec.GetString("name"),
		Description:           rec.GetString("description"),
		Color:                 rec.GetString("color"),
		MaxResourceSize:       MaxResourceSize,
		SupportedComponentSet: rec.GetStringSlice("components"),
		SyncToken:             syncToken(rec.GetInt("sync_seq")),
	}
}

func (b *CalDAV) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	recs, err := b.s.listCollections(calKind, uid)
	if err != nil {
		return nil, err
	}
	out := make([]caldav.Calendar, 0, len(recs))
	for _, r := range recs {
		out = append(out, b.toCalendar(uid, r))
	}
	return out, nil
}

func (b *CalDAV) GetCalendar(ctx context.Context, p string) (*caldav.Calendar, error) {
	uid, t, err := parse(ctx, calKind, p)
	if err != nil {
		return nil, err
	}
	if t.href != "" {
		return nil, notFound(p)
	}
	rec, err := b.s.findCollection(b.s.app, calKind, uid, t.slug)
	if err != nil {
		return nil, err
	}
	cal := b.toCalendar(uid, rec)
	return &cal, nil
}

// CreateCalendar handles extended MKCOL. Slugs come from the URL.
func (b *CalDAV) CreateCalendar(ctx context.Context, c *caldav.Calendar) error {
	uid, t, err := parse(ctx, calKind, c.Path)
	if err != nil {
		return err
	}
	if t.href != "" {
		return httpErr(http.StatusForbidden, "store: calendars can only be created in the calendar home")
	}
	if _, err := b.s.findCollection(b.s.app, calKind, uid, t.slug); err == nil {
		return httpErr(http.StatusMethodNotAllowed, "store: %s already exists", c.Path)
	}
	cals, err := b.s.app.FindCollectionByNameOrId(calKind.collections)
	if err != nil {
		return err
	}
	rec := core.NewRecord(cals)
	rec.Set("owner", uid)
	rec.Set("slug", t.slug)
	rec.Set("name", c.Name)
	rec.Set("description", c.Description)
	comps := c.SupportedComponentSet
	if len(comps) == 0 {
		comps = []string{ical.CompEvent, ical.CompToDo}
	}
	rec.Set("components", comps)
	if err := b.s.app.Save(rec); err != nil {
		return httpErr(http.StatusBadRequest, "store: create calendar: %v", err)
	}
	return nil
}

func (b *CalDAV) toObject(uid string, coll, rec *core.Record) (caldav.CalendarObject, error) {
	data := rec.GetString("ics")
	cal, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return caldav.CalendarObject{}, err
	}
	return caldav.CalendarObject{
		Path:          objectPath(uid, calKind, coll, rec),
		ModTime:       rec.GetDateTime("updated").Time(),
		ContentLength: int64(len(data)),
		ETag:          rec.GetString("etag"),
		Data:          cal,
	}, nil
}

func (b *CalDAV) GetCalendarObject(ctx context.Context, p string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	uid, t, err := parse(ctx, calKind, p)
	if err != nil {
		return nil, err
	}
	coll, err := b.s.findCollection(b.s.app, calKind, uid, t.slug)
	if err != nil {
		return nil, err
	}
	rec, err := findObject(b.s.app, calKind, coll, t.href)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, notFound(p)
	}
	o, err := b.toObject(uid, coll, rec)
	return &o, err
}

func (b *CalDAV) objects(ctx context.Context, p, filter string, params dbx.Params) ([]caldav.CalendarObject, error) {
	uid, t, err := parse(ctx, calKind, p)
	if err != nil {
		return nil, err
	}
	coll, err := b.s.findCollection(b.s.app, calKind, uid, t.slug)
	if err != nil {
		return nil, err
	}
	recs, err := listObjects(b.s.app, calKind, coll, filter, params)
	if err != nil {
		return nil, err
	}
	out := make([]caldav.CalendarObject, 0, len(recs))
	for _, r := range recs {
		o, err := b.toObject(uid, coll, r)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (b *CalDAV) ListCalendarObjects(ctx context.Context, p string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	return b.objects(ctx, p, "", nil)
}

// QueryCalendarObjects narrows by the indexed date range, then runs the
// exact matcher (which handles recurrence).
func (b *CalDAV) QueryCalendarObjects(ctx context.Context, p string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	filter, params := "", dbx.Params{}
	if f := q.CompFilter; f.Name == ical.CompCalendar && len(f.Comps) == 1 {
		comp := f.Comps[0]
		filter = "component = {:comp}"
		params["comp"] = comp.Name
		if !comp.End.IsZero() {
			filter += " && (dtstart = '' || dtstart < {:end})"
			params["end"] = dbDate(comp.End)
		}
		if !comp.Start.IsZero() {
			filter += " && (dtend = '' || dtend > {:start})"
			params["start"] = dbDate(comp.Start)
		}
	}
	objs, err := b.objects(ctx, p, filter, params)
	if err != nil {
		return nil, err
	}
	return caldav.Filter(q, objs)
}

func dbDate(t time.Time) string {
	dt, _ := types.ParseDateTime(t.UTC())
	return dt.String()
}

func (b *CalDAV) PutCalendarObject(ctx context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	info, err := inspectCalendar(cal)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, httpErr(http.StatusBadRequest, "store: invalid iCalendar: %v", err)
	}

	extra := map[string]any{"component": info.component, "dtstart": "", "dtend": ""}
	if !info.start.IsZero() {
		extra["dtstart"] = info.start.UTC()
	}
	if !info.end.IsZero() {
		extra["dtend"] = info.end.UTC()
	}
	uid, coll, rec, replaced, err := b.s.put(ctx, calKind, p, putReq{
		data: buf.Bytes(), uid: info.uid, extra: extra,
		ifMatch: opts.IfMatch, ifNone: opts.IfNoneMatch,
		allow: func(coll *core.Record) error {
			for _, c := range coll.GetStringSlice("components") {
				if c == info.component {
					return nil
				}
			}
			return httpErr(http.StatusForbidden, "store: calendar does not accept %s", info.component)
		},
	})
	if err != nil {
		return nil, err
	}
	o, err := b.toObject(uid, coll, rec)
	o.Replaced = replaced
	return &o, err
}

func (b *CalDAV) DeleteCalendarObject(ctx context.Context, p string) error {
	return b.s.delete(ctx, calKind, p)
}

func (b *CalDAV) SyncCalendar(ctx context.Context, p string, q *caldav.SyncQuery) (*caldav.SyncResponse, error) {
	uid, t, err := parse(ctx, calKind, p)
	if err != nil {
		return nil, err
	}
	coll, err := b.s.findCollection(b.s.app, calKind, uid, t.slug)
	if err != nil {
		return nil, err
	}
	res, err := b.s.changesSince(calKind, coll, q.SyncToken, q.Limit)
	if err != nil {
		return nil, err
	}
	out := &caldav.SyncResponse{SyncToken: res.token, Truncated: res.truncated}
	for _, r := range res.updated {
		o, err := b.toObject(uid, coll, r)
		if err != nil {
			return nil, err
		}
		out.Updated = append(out.Updated, o)
	}
	for _, h := range res.deleted {
		out.Deleted = append(out.Deleted, collectionPath(uid, calKind, t.slug)+h)
	}
	return out, nil
}

func (b *CalDAV) UpdateCalendar(ctx context.Context, p string, u *caldav.CalendarUpdate) error {
	uid, t, err := parse(ctx, calKind, p)
	if err != nil {
		return err
	}
	rec, err := b.s.findCollection(b.s.app, calKind, uid, t.slug)
	if err != nil {
		return err
	}
	if u.Name != nil {
		rec.Set("name", *u.Name)
	}
	if u.Description != nil {
		rec.Set("description", *u.Description)
	}
	if u.Color != nil {
		rec.Set("color", *u.Color)
	}
	if err := b.s.app.Save(rec); err != nil {
		return httpErr(http.StatusBadRequest, "store: update calendar: %v", err)
	}
	return nil
}

// ---- iCalendar inspection ----

type calInfo struct {
	component  string
	uid        string
	start, end time.Time // end zero = unbounded (recurring or unknown)
}

// inspectCalendar enforces one component type and one UID per resource
// (RFC 4791 §4.1) and computes the indexed date range.
func inspectCalendar(cal *ical.Calendar) (*calInfo, error) {
	info := &calInfo{}
	recurring := false
	for _, c := range cal.Children {
		if c.Name == ical.CompTimezone {
			continue
		}
		if c.Name != ical.CompEvent && c.Name != ical.CompToDo {
			return nil, httpErr(http.StatusForbidden, "store: unsupported component %s", c.Name)
		}
		if info.component != "" && c.Name != info.component {
			return nil, httpErr(http.StatusBadRequest, "store: mixed component types in one resource")
		}
		info.component = c.Name

		uid := ""
		if prop := c.Props.Get(ical.PropUID); prop != nil {
			uid = prop.Value
		}
		if uid == "" {
			return nil, httpErr(http.StatusBadRequest, "store: %s without UID", c.Name)
		}
		if info.uid != "" && uid != info.uid {
			return nil, httpErr(http.StatusBadRequest, "store: more than one UID in one resource")
		}
		info.uid = uid

		if c.Props.Get(ical.PropRecurrenceRule) != nil || c.Props.Get(ical.PropRecurrenceDates) != nil {
			recurring = true
		}
		for _, name := range []string{ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropDue, ical.PropRecurrenceID} {
			if prop := c.Props.Get(name); prop != nil {
				if _, err := prop.DateTime(time.UTC); err != nil {
					return nil, httpErr(http.StatusBadRequest, "store: invalid %s %q: %v", name, prop.Value, err)
				}
			}
		}
		if prop := c.Props.Get(ical.PropDuration); prop != nil {
			if _, err := prop.Duration(); err != nil {
				return nil, httpErr(http.StatusBadRequest, "store: invalid DURATION %q: %v", prop.Value, err)
			}
		}
		start, end := componentRange(c)
		if !start.IsZero() && (info.start.IsZero() || start.Before(info.start)) {
			info.start = start
		}
		if end.After(info.end) {
			info.end = end
		}
	}
	if info.component == "" {
		return nil, httpErr(http.StatusBadRequest, "store: no VEVENT or VTODO in resource")
	}
	if recurring {
		info.end = time.Time{}
	}
	return info, nil
}

// componentRange returns a component's [start, end]. Zero start means the
// component has no start (e.g. a task without DTSTART).
func componentRange(c *ical.Component) (start, end time.Time) {
	dt := func(name string) (time.Time, bool) {
		prop := c.Props.Get(name)
		if prop == nil {
			return time.Time{}, false
		}
		t, err := prop.DateTime(time.UTC)
		return t, err == nil
	}
	start, hasStart := dt(ical.PropDateTimeStart)
	if e, ok := dt(ical.PropDateTimeEnd); ok {
		return start, e
	}
	if e, ok := dt(ical.PropDue); ok {
		if !hasStart {
			start = e
		}
		return start, e
	}
	if prop := c.Props.Get(ical.PropDuration); prop != nil && hasStart {
		if d, err := prop.Duration(); err == nil {
			return start, start.Add(d)
		}
	}
	if hasStart {
		if v := c.Props.Get(ical.PropDateTimeStart); v != nil && v.ValueType() == ical.ValueDate {
			return start, start.Add(24 * time.Hour) // all-day
		}
		return start, start
	}
	return time.Time{}, time.Time{}
}
