// Package store implements the CalDAV and CardDAV backends on top of
// PocketBase collections (see docs/ARCHITECTURE.md §4–5).
//
// Both protocols share one write path: every PUT or DELETE is a single
// transaction that checks the ETag condition, writes the object, bumps the
// collection's sync_seq and appends a dav_changes row.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"

	webdav "nexus/internal/dav"
	"nexus/internal/principal"
	"nexus/internal/schema"
)

// Prefix is where the DAV tree is mounted.
const Prefix = "/dav"

// MaxResourceSize bounds one calendar object or vCard (matches the schema).
const MaxResourceSize = 1 << 20

const tokenPrefix = "https://nexus/ns/sync/"

// Store serves both CalDAV and CardDAV. Use CalDAV() and CardDAV() to get
// the protocol-specific backends.
type Store struct {
	app core.App
}

func New(app core.App) *Store { return &Store{app: app} }

// kind describes one collection type: calendars or address books.
type kind struct {
	name        string // dav_changes.kind
	collections string
	objects     string
	parent      string // relation field on objects
	data        string // raw text field on objects
	seg         string // URL segment under /dav/{uid}/
}

var (
	calKind = kind{name: "cal", collections: schema.Calendars, objects: schema.CalendarObjects,
		parent: "calendar", data: "ics", seg: "calendars"}
	cardKind = kind{name: "card", collections: schema.Addressbooks, objects: schema.Contacts,
		parent: "addressbook", data: "vcf", seg: "contacts"}
)

// ---- errors ----

func httpErr(code int, format string, a ...any) error {
	return webdav.NewHTTPError(code, fmt.Errorf(format, a...))
}

func notFound(p string) error { return httpErr(http.StatusNotFound, "store: %s not found", p) }

// ---- identity and paths ----

func userID(ctx context.Context) (string, error) {
	id, ok := principal.ID(ctx)
	if !ok {
		return "", httpErr(http.StatusUnauthorized, "store: no authenticated user")
	}
	return id, nil
}

func principalPath(uid string) string { return Prefix + "/" + uid + "/" }
func homePath(uid string, k kind) string {
	return principalPath(uid) + k.seg + "/"
}
func collectionPath(uid string, k kind, slug string) string { return homePath(uid, k) + slug + "/" }

// target is a parsed /dav/{uid}/{seg}/{slug}[/{href}] path.
type target struct {
	slug string
	href string // "" for the collection itself
}

// parse checks that p lies in the authenticated user's tree for kind k.
func parse(ctx context.Context, k kind, p string) (uid string, t target, err error) {
	if uid, err = userID(ctx); err != nil {
		return "", t, err
	}
	rest, ok := strings.CutPrefix(p, homePath(uid, k))
	if !ok {
		return "", t, httpErr(http.StatusForbidden, "store: %s is outside your %s", p, k.seg)
	}
	rest = strings.TrimSuffix(rest, "/")
	parts := strings.Split(rest, "/")
	switch {
	case rest == "" || len(parts) > 2:
		return "", t, notFound(p)
	case len(parts) == 2:
		t.href = parts[1]
		if t.href == "" || t.href == "." || t.href == ".." || len(t.href) > 255 {
			return "", t, httpErr(http.StatusBadRequest, "store: invalid resource name %q", t.href)
		}
	}
	t.slug = parts[0]
	return uid, t, nil
}

// ---- tokens and etags ----

func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

func syncToken(seq int) string { return tokenPrefix + strconv.Itoa(seq) }

func parseSyncToken(tok string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(tok, tokenPrefix))
	if err != nil || !strings.HasPrefix(tok, tokenPrefix) || n < 0 {
		return 0, webdav.ErrInvalidSyncToken
	}
	return n, nil
}

// checkCond applies If-Match / If-None-Match against cur ("" = absent).
func checkCond(ifMatch, ifNone webdav.ConditionalMatch, cur string) error {
	fail := httpErr(http.StatusPreconditionFailed, "store: precondition failed")
	if ifNone.IsSet() {
		if ifNone.IsWildcard() && cur != "" {
			return fail
		}
		if e, _ := ifNone.ETag(); e != "" && e == cur {
			return fail
		}
	}
	if ifMatch.IsSet() {
		if cur == "" {
			return fail
		}
		if e, _ := ifMatch.ETag(); !ifMatch.IsWildcard() && e != cur {
			return fail
		}
	}
	return nil
}

// ---- record lookups ----

func (s *Store) findCollection(app core.App, k kind, uid, slug string) (*core.Record, error) {
	rec, err := app.FindFirstRecordByFilter(k.collections, "owner = {:u} && slug = {:s}",
		dbx.Params{"u": uid, "s": slug})
	if isNoRows(err) {
		return nil, notFound(collectionPath(uid, k, slug))
	}
	return rec, err
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func (s *Store) listCollections(k kind, uid string) ([]*core.Record, error) {
	return s.app.FindRecordsByFilter(k.collections, "owner = {:u}", "slug", 0, 0, dbx.Params{"u": uid})
}

// findObject returns nil, nil when the object doesn't exist.
func findObject(app core.App, k kind, coll *core.Record, href string) (*core.Record, error) {
	rec, err := app.FindFirstRecordByFilter(k.objects, k.parent+" = {:c} && href = {:h}",
		dbx.Params{"c": coll.Id, "h": href})
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return rec, nil
}

func listObjects(app core.App, k kind, coll *core.Record, filter string, params dbx.Params) ([]*core.Record, error) {
	f := k.parent + " = {:c}"
	if filter != "" {
		f += " && (" + filter + ")"
	}
	if params == nil {
		params = dbx.Params{}
	}
	params["c"] = coll.Id
	return app.FindRecordsByFilter(k.objects, f, "href", 0, 0, params)
}

func objectPath(uid string, k kind, coll, obj *core.Record) string {
	return collectionPath(uid, k, coll.GetString("slug")) + obj.GetString("href")
}

// ---- the write path ----

// putReq is one object write, already validated and encoded by the caller.
type putReq struct {
	data    []byte
	uid     string // iCalendar / vCard UID
	extra   map[string]any
	ifMatch webdav.ConditionalMatch
	ifNone  webdav.ConditionalMatch
	allow   func(coll *core.Record) error // optional per-collection check
}

// put stores an object; replaced reports whether one was already at p.
func (s *Store) put(ctx context.Context, k kind, p string, req putReq) (uid string, coll, obj *core.Record, replaced bool, err error) {
	uid, t, err := parse(ctx, k, p)
	if err != nil {
		return "", nil, nil, false, err
	}
	if t.href == "" {
		return "", nil, nil, false, httpErr(http.StatusMethodNotAllowed, "store: cannot PUT a collection")
	}
	if len(req.data) > MaxResourceSize {
		return "", nil, nil, false, httpErr(http.StatusRequestEntityTooLarge, "store: resource exceeds %d bytes", MaxResourceSize)
	}

	err = s.app.RunInTransaction(func(tx core.App) error {
		var err error
		if coll, err = s.findCollection(tx, k, uid, t.slug); err != nil {
			return err
		}
		if req.allow != nil {
			if err := req.allow(coll); err != nil {
				return err
			}
		}
		if obj, err = findObject(tx, k, coll, t.href); err != nil {
			return err
		}
		cur := ""
		replaced = obj != nil
		if obj != nil {
			cur = obj.GetString("etag")
		}
		if err := checkCond(req.ifMatch, req.ifNone, cur); err != nil {
			return err
		}

		// UID must be unique within the collection (RFC 4791 no-uid-conflict,
		// RFC 6352 no-uid-conflict).
		clash, err := tx.FindFirstRecordByFilter(k.objects, k.parent+" = {:c} && uid = {:u} && href != {:h}",
			dbx.Params{"c": coll.Id, "u": req.uid, "h": t.href})
		if err != nil && !isNoRows(err) {
			return err
		}
		if clash != nil {
			return httpErr(http.StatusConflict, "store: UID %q is already used by %s", req.uid, clash.GetString("href"))
		}

		seq, err := bump(tx, coll)
		if err != nil {
			return err
		}
		if obj == nil {
			objs, err := tx.FindCollectionByNameOrId(k.objects)
			if err != nil {
				return err
			}
			obj = core.NewRecord(objs)
			obj.Set(k.parent, coll.Id)
			obj.Set("href", t.href)
		}
		obj.Set("uid", req.uid)
		obj.Set(k.data, string(req.data))
		obj.Set("etag", etagOf(req.data))
		obj.Set("seq", seq)
		for f, v := range req.extra {
			obj.Set(f, v)
		}
		if err := tx.Save(obj); err != nil {
			return err
		}
		return recordChange(tx, k, coll, t.href, seq, false)
	})
	return uid, coll, obj, replaced, err
}

func (s *Store) delete(ctx context.Context, k kind, p string) error {
	uid, t, err := parse(ctx, k, p)
	if err != nil {
		return err
	}
	if t.href == "" {
		return httpErr(http.StatusForbidden, "store: delete %s in the admin UI", k.seg)
	}
	return s.app.RunInTransaction(func(tx core.App) error {
		coll, err := s.findCollection(tx, k, uid, t.slug)
		if err != nil {
			return err
		}
		obj, err := findObject(tx, k, coll, t.href)
		if err != nil {
			return err
		}
		if obj == nil {
			return notFound(p)
		}
		if err := tx.Delete(obj); err != nil {
			return err
		}
		seq, err := bump(tx, coll)
		if err != nil {
			return err
		}
		return recordChange(tx, k, coll, t.href, seq, true)
	})
}

// bump increments the collection's sync_seq inside tx and returns it.
func bump(tx core.App, coll *core.Record) (int, error) {
	seq := coll.GetInt("sync_seq") + 1
	coll.Set("sync_seq", seq)
	return seq, tx.Save(coll)
}

func recordChange(tx core.App, k kind, coll *core.Record, href string, seq int, deleted bool) error {
	changes, err := tx.FindCollectionByNameOrId(schema.DAVChanges)
	if err != nil {
		return err
	}
	c := core.NewRecord(changes)
	c.Set("kind", k.name)
	c.Set("collection", coll.Id)
	c.Set("href", href)
	c.Set("seq", seq)
	c.Set("deleted", deleted)
	return tx.Save(c)
}

// ---- sync ----

type syncResult struct {
	token     string
	updated   []*core.Record // objects, ascending by seq
	deleted   []string       // hrefs
	truncated bool
}

// changesSince implements sync-collection for one collection. An empty
// token is an initial sync; limit 0 means no limit.
func (s *Store) changesSince(k kind, coll *core.Record, token string, limit int) (*syncResult, error) {
	cur := coll.GetInt("sync_seq")
	if token == "" {
		objs, err := listObjects(s.app, k, coll, "", nil)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(objs, func(i, j int) bool { return objs[i].GetInt("seq") < objs[j].GetInt("seq") })
		res := &syncResult{token: syncToken(cur), updated: objs}
		if limit > 0 && len(objs) > limit {
			res.updated, res.truncated = objs[:limit], true
			res.token = syncToken(objs[limit-1].GetInt("seq"))
		}
		return res, nil
	}

	n, err := parseSyncToken(token)
	if err != nil {
		return nil, err
	}
	if n > cur || n < coll.GetInt("sync_floor") {
		return nil, webdav.ErrInvalidSyncToken
	}

	changes, err := s.app.FindRecordsByFilter(schema.DAVChanges,
		"kind = {:k} && collection = {:c} && seq > {:n}", "seq", 0, 0,
		dbx.Params{"k": k.name, "c": coll.Id, "n": n})
	if err != nil {
		return nil, err
	}

	// Latest change per href, in ascending order of that latest seq.
	type latest struct {
		seq     int
		deleted bool
	}
	last := map[string]latest{}
	for _, c := range changes {
		last[c.GetString("href")] = latest{c.GetInt("seq"), c.GetBool("deleted")}
	}
	hrefs := make([]string, 0, len(last))
	for h := range last {
		hrefs = append(hrefs, h)
	}
	sort.Slice(hrefs, func(i, j int) bool { return last[hrefs[i]].seq < last[hrefs[j]].seq })

	res := &syncResult{token: syncToken(cur)}
	if limit > 0 && len(hrefs) > limit {
		hrefs, res.truncated = hrefs[:limit], true
		res.token = syncToken(last[hrefs[limit-1]].seq)
	}
	for _, h := range hrefs {
		if last[h].deleted {
			res.deleted = append(res.deleted, h)
			continue
		}
		obj, err := findObject(s.app, k, coll, h)
		if err != nil {
			return nil, err
		}
		if obj == nil { // deleted after the change row we saw; report as gone
			res.deleted = append(res.deleted, h)
			continue
		}
		res.updated = append(res.updated, obj)
	}
	return res, nil
}

// PruneChanges deletes change-log rows older than cutoff and raises each
// collection's sync_floor so older tokens get 403 valid-sync-token.
func (s *Store) PruneChanges(cutoff time.Time) error {
	return s.app.RunInTransaction(func(tx core.App) error {
		old, err := tx.FindRecordsByFilter(schema.DAVChanges, "created < {:t}", "seq", 0, 0,
			dbx.Params{"t": cutoff.UTC().Format("2006-01-02 15:04:05.000Z")})
		if err != nil {
			return err
		}
		floors := map[string]int{} // "kind/collection" -> highest pruned seq
		for _, c := range old {
			key := c.GetString("kind") + "/" + c.GetString("collection")
			if seq := c.GetInt("seq"); seq > floors[key] {
				floors[key] = seq
			}
			if err := tx.Delete(c); err != nil {
				return err
			}
		}
		for key, floor := range floors {
			kindName, id, _ := strings.Cut(key, "/")
			coll := schema.Calendars
			if kindName == cardKind.name {
				coll = schema.Addressbooks
			}
			rec, err := tx.FindRecordById(coll, id)
			if err != nil {
				continue // collection deleted meanwhile
			}
			if floor > rec.GetInt("sync_floor") {
				rec.Set("sync_floor", floor)
				if err := tx.Save(rec); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// DeleteChangesFor removes the change log of a deleted collection.
func DeleteChangesFor(app core.App, collectionID string) error {
	_, err := app.DB().Delete(schema.DAVChanges, dbx.HashExp{"collection": collectionID}).Execute()
	return err
}
