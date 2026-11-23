package nexus

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nexus/internal/account"
)

func decode[T any](t *testing.T, r resp) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
	return v
}

// TestDashboardEditAppPassword: label, scopes and paths change in place and
// take effect at once; the secret stays the same.
func TestDashboardEditAppPassword(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "phone", "cal", "card")
	creds := basic("ada@example.com", pw.Secret)
	cal := "/dav/" + u.Id + "/calendars/personal/"
	patch := func(id string, body any, tok string) resp {
		return s.do("PATCH", "/api/nexus/app-passwords/"+id, body, bearer(tok))
	}

	r := patch(pw.ID, map[string]any{"label": "  Pixel  ", "scopes": []string{"card"}}, tok)
	got := decode[account.AppPassword](t, r)
	if r.code != http.StatusOK || got.Label != "Pixel" || strings.Join(got.Scopes, ",") != "card" || got.Secret != "" {
		t.Fatalf("PATCH label and scopes: %d %s", r.code, r.body)
	}
	if code := s.propfind(cal, creds); code != http.StatusForbidden {
		t.Errorf("calendar after removing cal: %d, want 403", code)
	}
	if code := s.propfind("/dav/"+u.Id+"/contacts/contacts/", creds); code != http.StatusMultiStatus {
		t.Errorf("contacts with the same secret: %d, want 207", code)
	}

	r = patch(pw.ID, map[string]any{"scopes": []string{"cal", "card"}, "paths": []string{"/dav/" + u.Id + "/contacts/"}}, tok)
	if got := decode[account.AppPassword](t, r); r.code != http.StatusOK || strings.Join(got.Paths, ",") != "/dav/contacts/" {
		t.Fatalf("PATCH paths (pasted with the user id): %d %s", r.code, r.body)
	}
	if code := s.propfind(cal, creds); code != http.StatusForbidden {
		t.Errorf("calendar outside the new path limit: %d, want 403", code)
	}
	r = patch(pw.ID, map[string]any{"paths": []string{}}, tok)
	if got := decode[account.AppPassword](t, r); len(got.Paths) != 0 || strings.Join(got.Scopes, ",") != "cal,card" {
		t.Fatalf("PATCH paths [] removes the limit, scopes untouched: %s", r.body)
	}
	if code := s.propfind(cal, creds); code != http.StatusMultiStatus {
		t.Errorf("calendar after removing the limit: %d, want 207", code)
	}

	for name, body := range map[string]map[string]any{
		"blank label":   {"label": "  "},
		"no scopes":     {"scopes": []string{}},
		"unknown scope": {"scopes": []string{"admin"}},
		"bad path":      {"paths": []string{"/api/"}},
	} {
		if r := patch(pw.ID, body, tok); r.code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422", name, r.code)
		}
	}
	_, bobTok := s.user("bob@example.com")
	if r := patch(pw.ID, map[string]any{"label": "mine"}, bobTok); r.code != http.StatusNotFound {
		t.Errorf("another user's password: %d, want 404", r.code)
	}
	s.do("POST", "/api/nexus/app-passwords/"+pw.ID+"/revoke", nil, bearer(tok))
	if r := patch(pw.ID, map[string]any{"label": "again"}, tok); r.code != http.StatusConflict {
		t.Errorf("revoked password: %d, want 409", r.code)
	}
}

// TestDashboardCalendars: list, create, update and delete through the API,
// seen the same way over DAV.
func TestDashboardCalendars(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	creds := basic("ada@example.com", s.newPassword(tok, "laptop", "cal").Secret)
	api := func(method, path string, body any) resp {
		return s.do(method, "/api/nexus"+path, body, bearer(tok))
	}

	if r := s.do("GET", "/api/nexus/calendars", nil, nil); r.code != http.StatusUnauthorized {
		t.Errorf("without a token: %d, want 401", r.code)
	}
	list := decode[[]account.Collection](t, api("GET", "/calendars", nil))
	if len(list) != 1 || list[0].Slug != "personal" || list[0].Items != 0 ||
		!strings.HasSuffix(list[0].URL, "/dav/"+u.Id+"/calendars/personal/") {
		t.Fatalf("default calendars: %+v", list)
	}

	r := api("POST", "/calendars", map[string]any{"name": "Work Stuff!", "color": "#FF8800", "components": []string{"VEVENT"}})
	work := decode[account.Collection](t, r)
	if r.code != http.StatusCreated || work.Slug != "work-stuff" || work.Color != "#FF8800" || strings.Join(work.Components, ",") != "VEVENT" {
		t.Fatalf("create: %d %s", r.code, r.body)
	}
	if again := decode[account.Collection](t, api("POST", "/calendars", map[string]any{"name": "Work stuff"})); again.Slug != "work-stuff-2" {
		t.Errorf("second calendar with the same name: slug %q, want work-stuff-2", again.Slug)
	}
	if body := s.do("PROPFIND", "/dav/"+u.Id+"/calendars/work-stuff/", nil, func(r *http.Request) { creds(r); r.Header.Set("Depth", "0") }).body; !strings.Contains(body, "Work Stuff!") {
		t.Errorf("DAV doesn't see the new calendar's name:\n%s", body)
	}
	for name, body := range map[string]map[string]any{
		"blank name": {"name": " "},
		"bad colour": {"name": "x", "color": "orange"},
		"journals":   {"name": "x", "components": []string{"VJOURNAL"}},
	} {
		if r := api("POST", "/calendars", body); r.code != http.StatusUnprocessableEntity {
			t.Errorf("create with %s: %d, want 422", name, r.code)
		}
	}

	r = api("PATCH", "/calendars/work-stuff", map[string]any{"name": "Work", "color": ""})
	if got := decode[account.Collection](t, r); r.code != http.StatusOK || got.Name != "Work" || got.Color != "" || got.Slug != "work-stuff" {
		t.Fatalf("rename and remove colour: %d %s", r.code, r.body)
	}

	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:e1\r\nDTSTAMP:20261123T100000Z\r\nDTSTART:20261201T100000Z\r\nSUMMARY:x\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	s.do("PUT", "/dav/"+u.Id+"/calendars/work-stuff/e1.ics", ics, func(r *http.Request) { creds(r); r.Header.Set("Content-Type", "text/calendar") })
	if got := decode[account.Collection](t, api("GET", "/calendars/work-stuff", nil)); got.Items != 1 {
		t.Errorf("items after one PUT: %d", got.Items)
	}

	_, bobTok := s.user("bob@example.com")
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		if r := s.do(m, "/api/nexus/calendars/work-stuff", map[string]any{"name": "x"}, bearer(bobTok)); r.code != http.StatusNotFound {
			t.Errorf("%s another user's calendar: %d, want 404", m, r.code)
		}
	}

	if r := api("DELETE", "/calendars/work-stuff", nil); r.code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.code, r.body)
	}
	if code := s.propfind("/dav/"+u.Id+"/calendars/work-stuff/", creds); code != http.StatusNotFound {
		t.Errorf("DAV after delete: %d, want 404", code)
	}
	if n, _ := s.app.CountRecords("calendar_objects"); n != 0 {
		t.Errorf("%d events left after deleting their calendar", n)
	}
}

func TestDashboardAddressbooks(t *testing.T) {
	s := newServer(t)
	_, tok := s.user("ada@example.com")
	api := func(method, path string, body any) resp {
		return s.do(method, "/api/nexus"+path, body, bearer(tok))
	}
	r := api("POST", "/addressbooks", map[string]any{"name": "Work", "description": "Colleagues"})
	if got := decode[account.Collection](t, r); r.code != http.StatusCreated || got.Slug != "work" || got.Description != "Colleagues" || got.Color != "" {
		t.Fatalf("create: %d %s", r.code, r.body)
	}
	r = api("PATCH", "/addressbooks/work", map[string]any{"description": ""})
	if got := decode[account.Collection](t, r); got.Name != "Work" || got.Description != "" {
		t.Errorf("clear description: %s", r.body)
	}
	if list := decode[[]account.Collection](t, api("GET", "/addressbooks", nil)); len(list) != 2 {
		t.Errorf("list: %+v", list)
	}
	if r := api("DELETE", "/addressbooks/work", nil); r.code != http.StatusNoContent {
		t.Errorf("delete: %d", r.code)
	}
	if r := api("GET", "/addressbooks/work", nil); r.code != http.StatusNotFound {
		t.Errorf("after delete: %d", r.code)
	}
}

func TestOpenAPIDocumentIsPublic(t *testing.T) {
	s := newServer(t)
	r := s.do("GET", "/api/nexus/openapi.json", nil, nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"create-calendar"`) {
		t.Errorf("openapi.json: %d", r.code)
	}
}
