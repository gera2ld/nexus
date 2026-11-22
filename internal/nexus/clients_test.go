package nexus

import (
	"net/http"
	"strings"
	"testing"
)

// TestPutStatusNewVersusReplaced: PUT answers 201 for a new event or
// contact and 204 when it replaced one (RFC 9110 §9.3.4). Thunderbird and
// DAVx⁵ edit items by PUT to the same URL.
func TestPutStatusNewVersusReplaced(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "laptop", "cal", "card")
	creds := basic("ada@example.com", pw.Secret)
	for _, c := range []struct{ path, ctype, body string }{
		{"/dav/" + u.Id + "/calendars/personal/e1.ics", "text/calendar",
			"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:e1\r\nDTSTAMP:20261123T100000Z\r\nDTSTART:20261201T100000Z\r\nSUMMARY:x\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"},
		{"/dav/" + u.Id + "/contacts/contacts/c1.vcf", "text/vcard",
			"BEGIN:VCARD\r\nVERSION:3.0\r\nUID:c1\r\nFN:Grace Hopper\r\nN:Hopper;Grace;;;\r\nEND:VCARD\r\n"},
	} {
		put := func(r *http.Request) { creds(r); r.Header.Set("Content-Type", c.ctype) }
		if r := s.do("PUT", c.path, c.body, put); r.code != http.StatusCreated || r.hdr.Get("ETag") == "" {
			t.Errorf("first PUT %s: %d etag %q, want 201 with an ETag", c.path, r.code, r.hdr.Get("ETag"))
		}
		if r := s.do("PUT", c.path, c.body, put); r.code != http.StatusNoContent || r.hdr.Get("ETag") == "" {
			t.Errorf("second PUT %s: %d etag %q, want 204 with an ETag", c.path, r.code, r.hdr.Get("ETag"))
		}
	}
}

// TestClientsCreateCollections: calendar apps create calendars with
// MKCALENDAR (RFC 4791) or extended MKCOL (RFC 5689), and address books
// with extended MKCOL. The properties in the request body are kept.
func TestClientsCreateCollections(t *testing.T) {
	s := newServer(t)
	u, tok := s.user("ada@example.com")
	pw := s.newPassword(tok, "phone", "cal", "card")
	creds := basic("ada@example.com", pw.Secret)
	cals := "/dav/" + u.Id + "/calendars/"
	books := "/dav/" + u.Id + "/contacts/"
	withBody := func(r *http.Request) { creds(r); r.Header.Set("Content-Type", "application/xml") }
	props := func(path string) string {
		return s.do("PROPFIND", path, `<propfind xmlns="DAV:"><allprop/></propfind>`, func(r *http.Request) { withBody(r); r.Header.Set("Depth", "0") }).body
	}

	mkcalendar := `<?xml version="1.0"?>
<C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:A="http://apple.com/ns/ical/">
  <D:set><D:prop>
    <D:displayname>Work</D:displayname>
    <C:calendar-description>Office hours</C:calendar-description>
    <A:calendar-color>#FF8800FF</A:calendar-color>
    <C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set>
  </D:prop></D:set>
</C:mkcalendar>`
	if r := s.do("MKCALENDAR", cals+"work/", mkcalendar, withBody); r.code != http.StatusCreated {
		t.Fatalf("MKCALENDAR: %d %s", r.code, r.body)
	}
	for _, want := range []string{"Work", "Office hours", "#FF8800FF", `name="VEVENT"`} {
		if body := props(cals + "work/"); !strings.Contains(body, want) {
			t.Errorf("new calendar's properties lack %q:\n%s", want, body)
		}
	}
	if body := props(cals + "work/"); strings.Contains(body, `name="VTODO"`) {
		t.Errorf("calendar asked for events only also accepts tasks:\n%s", body)
	}
	if r := s.do("MKCALENDAR", cals+"work/", nil, creds); r.code != http.StatusMethodNotAllowed {
		t.Errorf("MKCALENDAR on an existing calendar: %d, want 405", r.code)
	}

	// Apple-style name and no body: a calendar for events and tasks.
	apple := cals + "8F0E2A1C-3B4D-4E5F-9A0B-1C2D3E4F5A6B/"
	if r := s.do("MKCALENDAR", apple, nil, creds); r.code != http.StatusCreated {
		t.Fatalf("MKCALENDAR without a body: %d %s", r.code, r.body)
	}
	if body := props(apple); !strings.Contains(body, `name="VEVENT"`) || !strings.Contains(body, `name="VTODO"`) {
		t.Errorf("calendar without a component set should take events and tasks:\n%s", body)
	}
	todo := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:t1\r\nDTSTAMP:20261123T100000Z\r\nSUMMARY:x\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	if r := s.do("PUT", apple+"t1.ics", todo, func(r *http.Request) { creds(r); r.Header.Set("Content-Type", "text/calendar") }); r.code != http.StatusCreated {
		t.Errorf("PUT a task into the new calendar: %d %s", r.code, r.body)
	}

	mkcolCal := `<?xml version="1.0"?>
<D:mkcol xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:set><D:prop>
    <D:resourcetype><D:collection/><C:calendar/></D:resourcetype>
    <D:displayname>Family</D:displayname>
  </D:prop></D:set>
</D:mkcol>`
	if r := s.do("MKCOL", cals+"family/", mkcolCal, withBody); r.code != http.StatusCreated {
		t.Fatalf("extended MKCOL calendar: %d %s", r.code, r.body)
	}
	if body := props(cals + "family/"); !strings.Contains(body, "Family") {
		t.Errorf("MKCOL calendar lost its name:\n%s", body)
	}

	mkcolBook := `<?xml version="1.0"?>
<D:mkcol xmlns:D="DAV:" xmlns:CR="urn:ietf:params:xml:ns:carddav">
  <D:set><D:prop>
    <D:resourcetype><D:collection/><CR:addressbook/></D:resourcetype>
    <D:displayname>Work contacts</D:displayname>
    <CR:addressbook-description>Colleagues</CR:addressbook-description>
  </D:prop></D:set>
</D:mkcol>`
	if r := s.do("MKCOL", books+"work/", mkcolBook, withBody); r.code != http.StatusCreated {
		t.Fatalf("extended MKCOL address book: %d %s", r.code, r.body)
	}
	for _, want := range []string{"Work contacts", "Colleagues"} {
		if body := props(books + "work/"); !strings.Contains(body, want) {
			t.Errorf("new address book's properties lack %q:\n%s", want, body)
		}
	}
	vcf := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:c1\r\nFN:Grace Hopper\r\nN:Hopper;Grace;;;\r\nEND:VCARD\r\n"
	if r := s.do("PUT", books+"work/c1.vcf", vcf, func(r *http.Request) { creds(r); r.Header.Set("Content-Type", "text/vcard") }); r.code != http.StatusCreated {
		t.Errorf("PUT a contact into the new address book: %d %s", r.code, r.body)
	}

	// The new collections show up in the home listings clients sync from.
	list := s.do("PROPFIND", cals, `<propfind xmlns="DAV:"><prop><displayname/></prop></propfind>`, func(r *http.Request) { withBody(r); r.Header.Set("Depth", "1") }).body
	for _, want := range []string{"/work/", "/family/", "/8F0E2A1C-3B4D-4E5F-9A0B-1C2D3E4F5A6B/", "/personal/"} {
		if !strings.Contains(list, want) {
			t.Errorf("calendar home lacks %s:\n%s", want, list)
		}
	}

	other, _ := s.user("bob@example.com")
	if r := s.do("MKCALENDAR", "/dav/"+other.Id+"/calendars/mine/", nil, creds); r.code != http.StatusForbidden {
		t.Errorf("MKCALENDAR in another user's home: %d, want 403", r.code)
	}
	if r := s.do("MKCALENDAR", cals+"work/inner/", nil, creds); r.code < 400 {
		t.Errorf("MKCALENDAR inside a calendar: %d, want an error", r.code)
	}
	cardOnly := basic("ada@example.com", s.newPassword(tok, "contacts only", "card").Secret)
	if r := s.do("MKCALENDAR", cals+"blocked/", nil, cardOnly); r.code != http.StatusForbidden {
		t.Errorf("MKCALENDAR with a contacts-only App Password: %d, want 403", r.code)
	}
}
