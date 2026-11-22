package caldav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	webdav "nexus/internal/dav"
)

// syncTestBackend adds the Nexus optional interfaces to testBackend.
type syncTestBackend struct {
	testBackend
	syncResp  *SyncResponse
	gotToken  string
	updates   []CalendarUpdate
	updateErr error
}

func (b *syncTestBackend) SyncCalendar(ctx context.Context, path string, q *SyncQuery) (*SyncResponse, error) {
	b.gotToken = q.SyncToken
	if q.SyncToken == "stale" {
		return nil, webdav.ErrInvalidSyncToken
	}
	return b.syncResp, nil
}

func (b *syncTestBackend) UpdateCalendar(ctx context.Context, path string, u *CalendarUpdate) error {
	b.updates = append(b.updates, *u)
	return b.updateErr
}

func newSyncBackend() *syncTestBackend {
	return &syncTestBackend{
		testBackend: testBackend{calendars: []Calendar{{Path: "/user/calendars/a/", Name: "A", SyncToken: "https://nexus/ns/sync/7"}}},
		syncResp: &SyncResponse{
			SyncToken: "https://nexus/ns/sync/9",
			Updated:   []CalendarObject{{Path: "/user/calendars/a/e1.ics", ETag: "abc", Data: testEvent()}},
			Deleted:   []string{"/user/calendars/a/gone.ics"},
		},
	}
}

func testEvent() *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//nexus//test//EN")
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, "e1")
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC))
	ev.Props.SetDateTime(ical.PropDateTimeStart, time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC))
	cal.Children = append(cal.Children, ev.Component)
	return cal
}

func do(t *testing.T, h http.Handler, method, path, depth, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(b)
}

func TestPropFindSyncProps(t *testing.T) {
	const body = `<propfind xmlns="DAV:" xmlns:CS="http://calendarserver.org/ns/"><prop><sync-token/><CS:getctag/><supported-report-set/></prop></propfind>`

	code, out := do(t, &Handler{Backend: newSyncBackend()}, "PROPFIND", "/user/calendars/a/", "0", body)
	if code != http.StatusMultiStatus {
		t.Fatalf("status %d: %s", code, out)
	}
	for _, want := range []string{
		`<sync-token xmlns="DAV:">https://nexus/ns/sync/7</sync-token>`,
		`<getctag xmlns="http://calendarserver.org/ns/">https://nexus/ns/sync/7</getctag>`,
		`<sync-collection xmlns="DAV:">`,
		`<calendar-multiget xmlns="urn:ietf:params:xml:ns:caldav">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}

	// A backend without SyncBackend must not advertise sync-collection.
	_, out = do(t, &Handler{Backend: newSyncBackend().testBackend}, "PROPFIND", "/user/calendars/a/", "0", body)
	if strings.Contains(out, "sync-collection") {
		t.Errorf("plain backend advertises sync-collection:\n%s", out)
	}
}

func TestSyncCollectionRoundTrip(t *testing.T) {
	b := newSyncBackend()
	srv := httptest.NewServer(&Handler{Backend: b})
	defer srv.Close()
	c, err := NewClient(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.SyncCollection(context.Background(), "/user/calendars/a/", &SyncQuery{SyncToken: "https://nexus/ns/sync/7"})
	if err != nil {
		t.Fatalf("SyncCollection: %v", err)
	}
	if b.gotToken != "https://nexus/ns/sync/7" {
		t.Errorf("backend got token %q", b.gotToken)
	}
	if res.SyncToken != "https://nexus/ns/sync/9" {
		t.Errorf("SyncToken = %q", res.SyncToken)
	}
	if len(res.Updated) != 1 || res.Updated[0].Path != "/user/calendars/a/e1.ics" || res.Updated[0].ETag != "abc" {
		t.Errorf("Updated = %+v", res.Updated)
	}
	if len(res.Deleted) != 1 || res.Deleted[0] != "/user/calendars/a/gone.ics" {
		t.Errorf("Deleted = %v", res.Deleted)
	}
}

func TestSyncCollectionInvalidToken(t *testing.T) {
	const body = `<sync-collection xmlns="DAV:"><sync-token>stale</sync-token><sync-level>1</sync-level><prop><getetag/></prop></sync-collection>`
	code, out := do(t, &Handler{Backend: newSyncBackend()}, "REPORT", "/user/calendars/a/", "0", body)
	if code != http.StatusForbidden || !strings.Contains(out, "valid-sync-token") {
		t.Fatalf("got %d %s, want 403 valid-sync-token", code, out)
	}
}

func TestSyncCollectionNotSupported(t *testing.T) {
	const body = `<sync-collection xmlns="DAV:"><sync-token/><sync-level>1</sync-level><prop><getetag/></prop></sync-collection>`
	code, _ := do(t, &Handler{Backend: newSyncBackend().testBackend}, "REPORT", "/user/calendars/a/", "0", body)
	if code != http.StatusNotImplemented {
		t.Fatalf("got %d, want 501", code)
	}
}

func TestPropPatchCalendar(t *testing.T) {
	b := newSyncBackend()
	h := &Handler{Backend: b}

	code, out := do(t, h, "PROPPATCH", "/user/calendars/a/", "", `<propertyupdate xmlns="DAV:" xmlns:I="http://apple.com/ns/ical/">
		<set><prop><displayname>Work</displayname><I:calendar-color>#ff0000</I:calendar-color></prop></set>
		<remove><prop><calendar-description xmlns="urn:ietf:params:xml:ns:caldav"/></prop></remove>
	</propertyupdate>`)
	if code != http.StatusMultiStatus || strings.Contains(out, "403") {
		t.Fatalf("status %d: %s", code, out)
	}
	if len(b.updates) != 1 {
		t.Fatalf("UpdateCalendar called %d times", len(b.updates))
	}
	u := b.updates[0]
	if u.Name == nil || *u.Name != "Work" || u.Color == nil || *u.Color != "#ff0000" || u.Description == nil || *u.Description != "" {
		t.Errorf("update = name %v color %v desc %v", u.Name, u.Color, u.Description)
	}

	// An unknown property makes the whole update fail atomically.
	code, out = do(t, h, "PROPPATCH", "/user/calendars/a/", "", `<propertyupdate xmlns="DAV:" xmlns:X="urn:x">
		<set><prop><displayname>Nope</displayname><X:custom>1</X:custom></prop></set>
	</propertyupdate>`)
	if code != http.StatusMultiStatus || !strings.Contains(out, "403 Forbidden") || !strings.Contains(out, "424 Failed Dependency") {
		t.Fatalf("status %d: %s", code, out)
	}
	if len(b.updates) != 1 {
		t.Errorf("UpdateCalendar called on a failed PROPPATCH")
	}
}
