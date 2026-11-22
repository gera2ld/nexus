package store_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations"
	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"

	webdav "nexus/internal/dav"
	"nexus/internal/dav/caldav"
	"nexus/internal/dav/carddav"
	"nexus/internal/nexus"
	"nexus/internal/principal"
	"nexus/internal/schema"
	"nexus/internal/store"
)

type env struct {
	t    *testing.T
	app  core.App
	st   *store.Store
	uid  string
	ctx  context.Context
	home string
}

func setup(t *testing.T) *env {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	nexus.Setup(app, nexus.Options{})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.ResetBootstrapState() })
	e := &env{t: t, app: app, st: store.New(app)}
	e.uid = e.newUser("ada@example.com")
	e.ctx = principal.With(context.Background(), e.uid)
	e.home = "/dav/" + e.uid + "/calendars/personal/"
	return e
}

func (e *env) newUser(email string) string {
	e.t.Helper()
	users, _ := e.app.FindCollectionByNameOrId(schema.Users)
	u := core.NewRecord(users)
	u.SetEmail(email)
	u.SetPassword("correct horse battery staple")
	if err := e.app.Save(u); err != nil {
		e.t.Fatal(err)
	}
	return u.Id
}

func event(uid, start string, extra ...string) *ical.Calendar {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//nexus//test//EN\r\nBEGIN:VEVENT\r\n")
	dtstart := "DTSTART:" + start
	if !strings.Contains(start, "T") {
		dtstart = "DTSTART;VALUE=DATE:" + start // all-day
	}
	fmt.Fprintf(&b, "UID:%s\r\nDTSTAMP:20261122T100000Z\r\n%s\r\nSUMMARY:%s\r\n", uid, dtstart, uid)
	for _, l := range extra {
		b.WriteString(l + "\r\n")
	}
	b.WriteString("END:VEVENT\r\nEND:VCALENDAR\r\n")
	cal, err := ical.NewDecoder(strings.NewReader(b.String())).Decode()
	if err != nil {
		panic(err)
	}
	return cal
}

func todo(uid string) *ical.Calendar {
	cal, err := ical.NewDecoder(strings.NewReader("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//nexus//test//EN\r\nBEGIN:VTODO\r\nUID:" +
		uid + "\r\nDTSTAMP:20261122T100000Z\r\nSUMMARY:task\r\nEND:VTODO\r\nEND:VCALENDAR\r\n")).Decode()
	if err != nil {
		panic(err)
	}
	return cal
}

// httpCode extracts the status of a webdav HTTP error ("404 Not Found: …").
func httpCode(err error) int {
	if err == nil {
		return 0
	}
	var code int
	fmt.Sscanf(err.Error(), "%d", &code)
	return code
}

func (e *env) put(name string, cal *ical.Calendar, opts caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	return e.st.CalDAV().PutCalendarObject(e.ctx, e.home+name, cal, &opts)
}

func TestPutGetAndConditions(t *testing.T) {
	e := setup(t)
	o, err := e.put("a.ics", event("a", "20261201T100000Z"), caldav.PutCalendarObjectOptions{IfNoneMatch: "*"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.st.CalDAV().GetCalendarObject(e.ctx, e.home+"a.ics", nil)
	if err != nil || got.ETag != o.ETag || got.Data.Children[0].Props.Get(ical.PropUID).Value != "a" {
		t.Fatalf("get: %v %+v", err, got)
	}

	if _, err := e.put("a.ics", event("a", "20261201T100000Z"), caldav.PutCalendarObjectOptions{IfNoneMatch: "*"}); httpCode(err) != http.StatusPreconditionFailed {
		t.Errorf("If-None-Match:* on existing: %v, want 412", err)
	}
	if _, err := e.put("a.ics", event("a", "20261202T100000Z"), caldav.PutCalendarObjectOptions{IfMatch: `"stale"`}); httpCode(err) != http.StatusPreconditionFailed {
		t.Errorf("wrong If-Match: %v, want 412", err)
	}
	o2, err := e.put("a.ics", event("a", "20261202T100000Z"), caldav.PutCalendarObjectOptions{IfMatch: webdav.ConditionalMatch(`"` + o.ETag + `"`)})
	if err != nil || o2.ETag == o.ETag {
		t.Errorf("right If-Match: err %v, etag unchanged=%v", err, o2 != nil && o2.ETag == o.ETag)
	}
	if _, err := e.put("new.ics", event("new", "20261201T100000Z"), caldav.PutCalendarObjectOptions{IfMatch: "*"}); httpCode(err) != http.StatusPreconditionFailed {
		t.Errorf("If-Match:* on missing: %v, want 412", err)
	}
}

func TestUIDConflictAndComponents(t *testing.T) {
	e := setup(t)
	if _, err := e.put("a.ics", event("same", "20261201T100000Z"), caldav.PutCalendarObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.put("b.ics", event("same", "20261201T100000Z"), caldav.PutCalendarObjectOptions{}); httpCode(err) != http.StatusConflict {
		t.Errorf("duplicate UID at another href: %v, want 409", err)
	}
	bad := event("bad", "20261201T100000Z")
	bad.Children[0].Props.Get(ical.PropDateTimeStart).Value = "20261215" // DATE value without VALUE=DATE
	if _, err := e.put("bad.ics", bad, caldav.PutCalendarObjectOptions{}); httpCode(err) != http.StatusBadRequest {
		t.Errorf("unparsable DTSTART: %v, want 400", err)
	}
	if _, err := e.put("t.ics", todo("t1"), caldav.PutCalendarObjectOptions{}); err != nil {
		t.Errorf("VTODO into default calendar: %v", err)
	}

	// A calendar restricted to events refuses tasks.
	rec, _ := e.app.FindFirstRecordByFilter(schema.Calendars, "slug = 'personal'")
	rec.Set("components", []string{"VEVENT"})
	if err := e.app.Save(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := e.put("t2.ics", todo("t2"), caldav.PutCalendarObjectOptions{}); httpCode(err) != http.StatusForbidden {
		t.Errorf("VTODO into event-only calendar: %v, want 403", err)
	}
}

func (e *env) sync(token string, limit int) *caldav.SyncResponse {
	e.t.Helper()
	res, err := e.st.CalDAV().SyncCalendar(e.ctx, e.home, &caldav.SyncQuery{SyncToken: token, Limit: limit})
	if err != nil {
		e.t.Fatalf("sync(%q): %v", token, err)
	}
	return res
}

func paths(objs []caldav.CalendarObject) []string {
	var out []string
	for _, o := range objs {
		out = append(out, o.Path[strings.LastIndex(o.Path, "/")+1:])
	}
	return out
}

func TestSync(t *testing.T) {
	e := setup(t)
	t0 := e.sync("", 0)
	if len(t0.Updated) != 0 || t0.SyncToken != "https://nexus/ns/sync/0" {
		t.Fatalf("empty initial sync: %+v", t0)
	}
	for _, n := range []string{"a", "b", "c"} {
		if _, err := e.put(n+".ics", event(n, "20261201T100000Z"), caldav.PutCalendarObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	t1 := e.sync(t0.SyncToken, 0)
	if got := strings.Join(paths(t1.Updated), ","); got != "a.ics,b.ics,c.ics" {
		t.Errorf("incremental = %s", got)
	}

	e.put("a.ics", event("a", "20261203T100000Z"), caldav.PutCalendarObjectOptions{})
	if err := e.st.CalDAV().DeleteCalendarObject(e.ctx, e.home+"b.ics"); err != nil {
		t.Fatal(err)
	}
	t2 := e.sync(t1.SyncToken, 0)
	if got := strings.Join(paths(t2.Updated), ","); got != "a.ics" {
		t.Errorf("after edit+delete, updated = %s, want a.ics only", got)
	}
	if len(t2.Deleted) != 1 || !strings.HasSuffix(t2.Deleted[0], "/b.ics") {
		t.Errorf("deleted = %v", t2.Deleted)
	}
	if t3 := e.sync(t2.SyncToken, 0); len(t3.Updated)+len(t3.Deleted) != 0 {
		t.Errorf("no-op sync returned %+v", t3)
	}

	for _, bad := range []string{"https://nexus/ns/sync/999", "garbage", "https://nexus/ns/sync/-1"} {
		_, err := e.st.CalDAV().SyncCalendar(e.ctx, e.home, &caldav.SyncQuery{SyncToken: bad})
		if !errors.Is(err, webdav.ErrInvalidSyncToken) {
			t.Errorf("token %q: %v, want ErrInvalidSyncToken", bad, err)
		}
	}

	// Recreating a deleted href shows up as an update, not a deletion.
	e.put("b.ics", event("b", "20261201T100000Z"), caldav.PutCalendarObjectOptions{})
	if t4 := e.sync(t2.SyncToken, 0); strings.Join(paths(t4.Updated), ",") != "b.ics" || len(t4.Deleted) != 0 {
		t.Errorf("recreated b.ics: updated %v deleted %v", paths(t4.Updated), t4.Deleted)
	}
}

func TestSyncLimitTruncates(t *testing.T) {
	e := setup(t)
	base := e.sync("", 0).SyncToken
	for _, n := range []string{"a", "b", "c"} {
		e.put(n+".ics", event(n, "20261201T100000Z"), caldav.PutCalendarObjectOptions{})
	}

	for name, start := range map[string]string{"incremental": base, "initial": ""} {
		page1 := e.sync(start, 2)
		if !page1.Truncated || strings.Join(paths(page1.Updated), ",") != "a.ics,b.ics" {
			t.Errorf("%s page 1: truncated=%v %v", name, page1.Truncated, paths(page1.Updated))
		}
		page2 := e.sync(page1.SyncToken, 2)
		if page2.Truncated || strings.Join(paths(page2.Updated), ",") != "c.ics" {
			t.Errorf("%s page 2: truncated=%v %v", name, page2.Truncated, paths(page2.Updated))
		}
	}
}

func TestPruneInvalidatesOldTokens(t *testing.T) {
	e := setup(t)
	old := e.sync("", 0).SyncToken
	e.put("a.ics", event("a", "20261201T100000Z"), caldav.PutCalendarObjectOptions{})
	mid := e.sync(old, 0).SyncToken
	if err := e.st.PruneChanges(time.Now().Add(time.Hour)); err != nil { // prune everything
		t.Fatal(err)
	}
	if _, err := e.st.CalDAV().SyncCalendar(e.ctx, e.home, &caldav.SyncQuery{SyncToken: old}); !errors.Is(err, webdav.ErrInvalidSyncToken) {
		t.Errorf("token older than pruned history: %v, want ErrInvalidSyncToken", err)
	}
	if res := e.sync(mid, 0); len(res.Updated)+len(res.Deleted) != 0 {
		t.Errorf("current token after prune should still be valid and empty, got %+v", res)
	}
}

func TestTimeRangeQuery(t *testing.T) {
	e := setup(t)
	e.put("dec.ics", event("dec", "20261201T100000Z", "DTEND:20261201T110000Z"), caldav.PutCalendarObjectOptions{})
	e.put("jun.ics", event("jun", "20260601T100000Z", "DTEND:20260601T110000Z"), caldav.PutCalendarObjectOptions{})
	e.put("weekly.ics", event("weekly", "20250106T090000Z", "DTEND:20250106T093000Z", "RRULE:FREQ=WEEKLY"), caldav.PutCalendarObjectOptions{})
	e.put("allday.ics", event("allday", "20261215", "DTEND;VALUE=DATE:20261216"), caldav.PutCalendarObjectOptions{})
	e.put("task.ics", todo("task"), caldav.PutCalendarObjectOptions{})

	q := &caldav.CalendarQuery{CompFilter: caldav.CompFilter{Name: "VCALENDAR", Comps: []caldav.CompFilter{{
		Name: "VEVENT", Start: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}}}}
	got, err := e.st.CalDAV().QueryCalendarObjects(e.ctx, e.home, q)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(paths(got), ","); s != "allday.ics,dec.ics,weekly.ics" {
		t.Errorf("December events = %s, want allday, dec and the weekly recurrence", s)
	}
}

func TestIsolationAndCollectionDelete(t *testing.T) {
	e := setup(t)
	e.put("a.ics", event("a", "20261201T100000Z"), caldav.PutCalendarObjectOptions{})

	bob := principal.With(context.Background(), e.newUser("bob@example.com"))
	if _, err := e.st.CalDAV().GetCalendarObject(bob, e.home+"a.ics", nil); httpCode(err) != http.StatusForbidden {
		t.Errorf("bob reading ada's event: %v, want 403", err)
	}
	if _, err := e.st.CalDAV().PutCalendarObject(bob, e.home+"x.ics", event("x", "20261201T100000Z"), &caldav.PutCalendarObjectOptions{}); httpCode(err) != http.StatusForbidden {
		t.Errorf("bob writing ada's calendar: %v, want 403", err)
	}
	if _, err := e.st.CalDAV().GetCalendarObject(context.Background(), e.home+"a.ics", nil); httpCode(err) != http.StatusUnauthorized {
		t.Errorf("no principal: %v, want 401", err)
	}
	if err := e.st.CalDAV().DeleteCalendarObject(e.ctx, e.home); httpCode(err) != http.StatusForbidden {
		t.Errorf("DELETE on a calendar: %v, want 403", err)
	}

	// Deleting the calendar (admin UI) removes its change log too.
	rec, _ := e.app.FindFirstRecordByFilter(schema.Calendars, "owner = {:u}", dbx.Params{"u": e.uid})
	if err := e.app.Delete(rec); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.app.CountRecords(schema.DAVChanges, dbx.HashExp{"collection": rec.Id}); n != 0 {
		t.Errorf("%d change rows left for deleted calendar", n)
	}
	if n, _ := e.app.CountRecords(schema.CalendarObjects); n != 0 {
		t.Errorf("%d objects left for deleted calendar", n)
	}
}

func TestCardDAV(t *testing.T) {
	e := setup(t)
	book := "/dav/" + e.uid + "/contacts/contacts/"
	cd := e.st.CardDAV()
	base, err := cd.SyncAddressBook(e.ctx, book, &carddav.SyncQuery{})
	if err != nil {
		t.Fatal(err)
	}
	card := vcard.Card{}
	card.SetValue(vcard.FieldVersion, "3.0")
	card.SetValue(vcard.FieldUID, "c1")
	card.SetValue(vcard.FieldFormattedName, "Ada Lovelace")
	card.SetValue(vcard.FieldEmail, "ada@example.com")
	if _, err := cd.PutAddressObject(e.ctx, book+"c1.vcf", card, &carddav.PutAddressObjectOptions{IfNoneMatch: "*"}); err != nil {
		t.Fatal(err)
	}
	rec, err := e.app.FindFirstRecordByFilter(schema.Contacts, "href = 'c1.vcf'")
	if err != nil || rec.GetString("fn") != "Ada Lovelace" || rec.GetString("email") != "ada@example.com" {
		t.Errorf("indexed fields: %v fn=%q email=%q", err, rec.GetString("fn"), rec.GetString("email"))
	}
	res, err := cd.SyncAddressBook(e.ctx, book, &carddav.SyncQuery{SyncToken: base.SyncToken})
	if err != nil || len(res.Updated) != 1 {
		t.Fatalf("sync: %v %+v", err, res)
	}
	noUID := vcard.Card{}
	noUID.SetValue(vcard.FieldVersion, "3.0")
	noUID.SetValue(vcard.FieldFormattedName, "Nobody")
	if _, err := cd.PutAddressObject(e.ctx, book+"x.vcf", noUID, &carddav.PutAddressObjectOptions{}); httpCode(err) != http.StatusBadRequest {
		t.Errorf("vCard without UID: %v, want 400", err)
	}
	if err := cd.DeleteAddressBook(e.ctx, book); httpCode(err) != http.StatusForbidden {
		t.Errorf("DeleteAddressBook: %v, want 403", err)
	}
}
