package nexus

import (
	"net/http"
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
