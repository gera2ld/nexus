package carddav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"

	webdav "nexus/internal/dav"
)

// syncBookBackend is a minimal backend with one address book that
// implements the Nexus optional interfaces.
type syncBookBackend struct {
	updates []AddressBookUpdate
}

const syncBook = "/user/contacts/a/"

func (*syncBookBackend) CurrentUserPrincipal(context.Context) (string, error) { return "/user/", nil }
func (*syncBookBackend) AddressBookHomeSetPath(context.Context) (string, error) {
	return "/user/contacts/", nil
}
func (*syncBookBackend) ListAddressBooks(context.Context) ([]AddressBook, error) {
	return []AddressBook{{Path: syncBook, SyncToken: "tok-3"}}, nil
}
func (*syncBookBackend) GetAddressBook(_ context.Context, p string) (*AddressBook, error) {
	return &AddressBook{Path: syncBook, SyncToken: "tok-3"}, nil
}
func (*syncBookBackend) CreateAddressBook(context.Context, *AddressBook) error { return nil }
func (*syncBookBackend) DeleteAddressBook(context.Context, string) error       { return nil }
func (*syncBookBackend) GetAddressObject(context.Context, string, *AddressDataRequest) (*AddressObject, error) {
	return nil, webdav.NewHTTPError(http.StatusNotFound, nil)
}
func (*syncBookBackend) ListAddressObjects(context.Context, string, *AddressDataRequest) ([]AddressObject, error) {
	return nil, nil
}
func (*syncBookBackend) QueryAddressObjects(context.Context, string, *AddressBookQuery) ([]AddressObject, error) {
	return nil, nil
}
func (*syncBookBackend) PutAddressObject(context.Context, string, vcard.Card, *PutAddressObjectOptions) (*AddressObject, error) {
	return nil, nil
}
func (*syncBookBackend) DeleteAddressObject(context.Context, string) error { return nil }

func (*syncBookBackend) SyncAddressBook(_ context.Context, _ string, q *SyncQuery) (*SyncResponse, error) {
	if q.SyncToken == "stale" {
		return nil, webdav.ErrInvalidSyncToken
	}
	return &SyncResponse{
		SyncToken: "tok-4",
		Updated:   []AddressObject{{Path: syncBook + "c1.vcf", ETag: "e1", Card: vcard.Card{"VERSION": {{Value: "3.0"}}, "FN": {{Value: "Ada"}}, "UID": {{Value: "c1"}}}}},
		Deleted:   []string{syncBook + "c0.vcf"},
	}, nil
}

func (b *syncBookBackend) UpdateAddressBook(_ context.Context, _ string, u *AddressBookUpdate) error {
	b.updates = append(b.updates, *u)
	return nil
}

func TestAddressBookSync(t *testing.T) {
	b := &syncBookBackend{}
	srv := httptest.NewServer(&Handler{Backend: b})
	defer srv.Close()
	c, err := NewClient(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.SyncCollection(context.Background(), syncBook, &SyncQuery{SyncToken: "tok-3"})
	if err != nil {
		t.Fatalf("SyncCollection: %v", err)
	}
	if res.SyncToken != "tok-4" || len(res.Updated) != 1 || res.Updated[0].ETag != "e1" || len(res.Deleted) != 1 {
		t.Errorf("got %+v", res)
	}

	if _, err := c.SyncCollection(context.Background(), syncBook, &SyncQuery{SyncToken: "stale"}); err == nil ||
		!strings.Contains(err.Error(), "403") {
		t.Errorf("stale token: err = %v, want 403", err)
	}
}

func TestAddressBookSyncPropsAndPropPatch(t *testing.T) {
	b := &syncBookBackend{}
	h := &Handler{Backend: b}

	req := httptest.NewRequest("PROPFIND", syncBook, strings.NewReader(
		`<propfind xmlns="DAV:" xmlns:CS="http://calendarserver.org/ns/"><prop><sync-token/><CS:getctag/><supported-report-set/></prop></propfind>`))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Depth", "0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Body)
	for _, want := range []string{">tok-3</sync-token>", ">tok-3</getctag>", "<sync-collection"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("PROPFIND missing %s:\n%s", want, out)
		}
	}

	req = httptest.NewRequest("PROPPATCH", syncBook, strings.NewReader(
		`<propertyupdate xmlns="DAV:"><set><prop><displayname>Friends</displayname></prop></set></propertyupdate>`))
	req.Header.Set("Content-Type", "application/xml")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMultiStatus || len(b.updates) != 1 || *b.updates[0].Name != "Friends" || b.updates[0].Description != nil {
		t.Fatalf("PROPPATCH: %d, updates %+v", rec.Code, b.updates)
	}
}
