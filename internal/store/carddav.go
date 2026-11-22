package store

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/emersion/go-vcard"

	"nexus/internal/dav/carddav"
)

// CardDAV is the carddav.Backend (plus SyncBackend and UpdateBackend).
type CardDAV struct{ s *Store }

func (s *Store) CardDAV() *CardDAV { return &CardDAV{s} }

var (
	_ carddav.Backend       = (*CardDAV)(nil)
	_ carddav.SyncBackend   = (*CardDAV)(nil)
	_ carddav.UpdateBackend = (*CardDAV)(nil)
)

func (b *CardDAV) CurrentUserPrincipal(ctx context.Context) (string, error) {
	uid, err := userID(ctx)
	return principalPath(uid), err
}

func (b *CardDAV) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	uid, err := userID(ctx)
	return homePath(uid, cardKind), err
}

func (b *CardDAV) toBook(uid string, rec *core.Record) carddav.AddressBook {
	return carddav.AddressBook{
		Path:            collectionPath(uid, cardKind, rec.GetString("slug")),
		Name:            rec.GetString("name"),
		Description:     rec.GetString("description"),
		MaxResourceSize: MaxResourceSize,
		SyncToken:       syncToken(rec.GetInt("sync_seq")),
	}
}

func (b *CardDAV) ListAddressBooks(ctx context.Context) ([]carddav.AddressBook, error) {
	uid, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	recs, err := b.s.listCollections(cardKind, uid)
	if err != nil {
		return nil, err
	}
	out := make([]carddav.AddressBook, 0, len(recs))
	for _, r := range recs {
		out = append(out, b.toBook(uid, r))
	}
	return out, nil
}

func (b *CardDAV) GetAddressBook(ctx context.Context, p string) (*carddav.AddressBook, error) {
	uid, t, err := parse(ctx, cardKind, p)
	if err != nil {
		return nil, err
	}
	if t.href != "" {
		return nil, notFound(p)
	}
	rec, err := b.s.findCollection(b.s.app, cardKind, uid, t.slug)
	if err != nil {
		return nil, err
	}
	ab := b.toBook(uid, rec)
	return &ab, nil
}

func (b *CardDAV) CreateAddressBook(ctx context.Context, ab *carddav.AddressBook) error {
	uid, t, err := parse(ctx, cardKind, ab.Path)
	if err != nil {
		return err
	}
	if t.href != "" {
		return httpErr(http.StatusForbidden, "store: address books can only be created in the address book home")
	}
	if _, err := b.s.findCollection(b.s.app, cardKind, uid, t.slug); err == nil {
		return httpErr(http.StatusMethodNotAllowed, "store: %s already exists", ab.Path)
	}
	books, err := b.s.app.FindCollectionByNameOrId(cardKind.collections)
	if err != nil {
		return err
	}
	rec := core.NewRecord(books)
	rec.Set("owner", uid)
	rec.Set("slug", t.slug)
	rec.Set("name", ab.Name)
	rec.Set("description", ab.Description)
	if err := b.s.app.Save(rec); err != nil {
		return httpErr(http.StatusBadRequest, "store: create address book: %v", err)
	}
	return nil
}

// DeleteAddressBook is refused: collections are deleted in the admin UI.
func (b *CardDAV) DeleteAddressBook(ctx context.Context, p string) error {
	return httpErr(http.StatusForbidden, "store: delete address books in the admin UI")
}

func (b *CardDAV) toObject(uid string, coll, rec *core.Record) (carddav.AddressObject, error) {
	data := rec.GetString("vcf")
	card, err := vcard.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return carddav.AddressObject{}, err
	}
	return carddav.AddressObject{
		Path:          objectPath(uid, cardKind, coll, rec),
		ModTime:       rec.GetDateTime("updated").Time(),
		ContentLength: int64(len(data)),
		ETag:          rec.GetString("etag"),
		Card:          card,
	}, nil
}

func (b *CardDAV) GetAddressObject(ctx context.Context, p string, _ *carddav.AddressDataRequest) (*carddav.AddressObject, error) {
	uid, t, err := parse(ctx, cardKind, p)
	if err != nil {
		return nil, err
	}
	coll, err := b.s.findCollection(b.s.app, cardKind, uid, t.slug)
	if err != nil {
		return nil, err
	}
	rec, err := findObject(b.s.app, cardKind, coll, t.href)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, notFound(p)
	}
	o, err := b.toObject(uid, coll, rec)
	return &o, err
}

func (b *CardDAV) ListAddressObjects(ctx context.Context, p string, _ *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	uid, t, err := parse(ctx, cardKind, p)
	if err != nil {
		return nil, err
	}
	coll, err := b.s.findCollection(b.s.app, cardKind, uid, t.slug)
	if err != nil {
		return nil, err
	}
	recs, err := listObjects(b.s.app, cardKind, coll, "", nil)
	if err != nil {
		return nil, err
	}
	out := make([]carddav.AddressObject, 0, len(recs))
	for _, r := range recs {
		o, err := b.toObject(uid, coll, r)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (b *CardDAV) QueryAddressObjects(ctx context.Context, p string, q *carddav.AddressBookQuery) ([]carddav.AddressObject, error) {
	all, err := b.ListAddressObjects(ctx, p, nil)
	if err != nil {
		return nil, err
	}
	return carddav.Filter(q, all)
}

func (b *CardDAV) PutAddressObject(ctx context.Context, p string, card vcard.Card, opts *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	uidVal := card.Value(vcard.FieldUID)
	if uidVal == "" {
		return nil, httpErr(http.StatusBadRequest, "store: vCard without UID")
	}
	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return nil, httpErr(http.StatusBadRequest, "store: invalid vCard: %v", err)
	}
	uid, coll, rec, err := b.s.put(ctx, cardKind, p, putReq{
		data: buf.Bytes(), uid: uidVal,
		extra: map[string]any{
			"fn":    truncate(card.PreferredValue(vcard.FieldFormattedName), 1024),
			"email": truncate(card.PreferredValue(vcard.FieldEmail), 1024),
		},
		ifMatch: opts.IfMatch, ifNone: opts.IfNoneMatch,
	})
	if err != nil {
		return nil, err
	}
	o, err := b.toObject(uid, coll, rec)
	return &o, err
}

func (b *CardDAV) DeleteAddressObject(ctx context.Context, p string) error {
	return b.s.delete(ctx, cardKind, p)
}

func (b *CardDAV) SyncAddressBook(ctx context.Context, p string, q *carddav.SyncQuery) (*carddav.SyncResponse, error) {
	uid, t, err := parse(ctx, cardKind, p)
	if err != nil {
		return nil, err
	}
	coll, err := b.s.findCollection(b.s.app, cardKind, uid, t.slug)
	if err != nil {
		return nil, err
	}
	res, err := b.s.changesSince(cardKind, coll, q.SyncToken, q.Limit)
	if err != nil {
		return nil, err
	}
	out := &carddav.SyncResponse{SyncToken: res.token, Truncated: res.truncated}
	for _, r := range res.updated {
		o, err := b.toObject(uid, coll, r)
		if err != nil {
			return nil, err
		}
		out.Updated = append(out.Updated, o)
	}
	for _, h := range res.deleted {
		out.Deleted = append(out.Deleted, collectionPath(uid, cardKind, t.slug)+h)
	}
	return out, nil
}

func (b *CardDAV) UpdateAddressBook(ctx context.Context, p string, u *carddav.AddressBookUpdate) error {
	uid, t, err := parse(ctx, cardKind, p)
	if err != nil {
		return err
	}
	rec, err := b.s.findCollection(b.s.app, cardKind, uid, t.slug)
	if err != nil {
		return err
	}
	if u.Name != nil {
		rec.Set("name", *u.Name)
	}
	if u.Description != nil {
		rec.Set("description", *u.Description)
	}
	if err := b.s.app.Save(rec); err != nil {
		return httpErr(http.StatusBadRequest, "store: update address book: %v", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
