# Nexus — Architecture

Status: draft · Last updated 2026-11-22 · Product scope:
[PRODUCT.md](PRODUCT.md).

## 1. Overview

Nexus is a single Go binary. PocketBase is used as a library and supplies
users, authentication, the SQLite database and the admin UI. Nexus adds its
own HTTP handlers for the three DAV protocols:

```
                      ┌──────────────────────── nexus (one process) ───────────────────────┐
 Thunderbird ─┐       │                                                                     │
 DAVx⁵ ───────┼─HTTPS─┤  /_/ , /api/*            PocketBase admin UI + REST API             │
 Finder ──────┘       │  /.well-known/cal|carddav  → 301 /dav/                              │
                      │  /dav/*    ─┐                                                       │
                      │  /files/*  ─┤ DAV auth middleware (Basic + App Password, scopes)    │
                      │             ├─► DAV router ─┬─ principal  (webdav.ServePrincipal)   │
                      │             │               ├─ calendars/ (internal/dav/caldav) ─┐  │
                      │             │               └─ contacts/  (internal/dav/carddav) ┤  │
                      │             │                                                    ▼  │
                      │             │                        DAV store ──► SQLite (PocketBase)
                      │             └─► file handler (x/net/webdav) ─► Storage Manager     │
                      │                                                 └─► rclone vfs/fs.Fs ─► S3 / local
                      └─────────────────────────────────────────────────────────────────────┘
```

rclone runs **in-process**, as a library. It isn't a separate HTTP server, and
the old auth proxy goes away.

## 2. Technology choices

| Area | Choice | Notes |
|---|---|---|
| Language / runtime | Go | |
| Application framework | PocketBase v0.42, used as a Go library | Module path `ganigeorgiev.com/pocketbase/pocketbase` (the GitHub path stops at v0.40.6). Users, auth, admin UI |
| Database | SQLite via PocketBase | |
| CalDAV / CardDAV | `internal/dav`: a copy of `emersion/go-webdav` v0.8.0 plus Nexus extensions | See §9 |
| WebDAV files | `golang.org/x/net/webdav` | The same approach as `rclone serve webdav` |
| File storage | rclone Go API (`fs.Fs`, `vfs.VFS`) | S3 first; any rclone backend |
| iCalendar / vCard | `emersion/go-ical`, `emersion/go-vcard` | |
| Calendar and contact data | Raw ICS / VCF text in SQLite, plus indexed fields | |
| File contents | rclone backend | Never in SQLite |
| Admin UI | PocketBase dashboard, plus a small account page | |
| Deployment | One Docker container, plus persistent data and config volumes | |

## 3. HTTP surface

```
/_/ , /api/*                              PocketBase (admin dashboard, REST API)
/.well-known/caldav, /.well-known/carddav 301 → /dav/
/dav/                                     current-user-principal discovery
/dav/{uid}/                               principal (advertises both home sets)
/dav/{uid}/calendars/                     calendar-home-set
/dav/{uid}/calendars/{cal}/{name}.ics     calendar object
/dav/{uid}/contacts/                      addressbook-home-set
/dav/{uid}/contacts/{book}/{name}.vcf     address object
/files/{uid}/{mount}/path/...             WebDAV files
```

- go-webdav works out what a path is from how deep it is under `/dav`:
  principal, then home set, then collection, then object. That fixes this
  shape. The DAV router sends `/dav/{uid}/` to `webdav.ServePrincipal`,
  `calendars/…` to CalDAV and `contacts/…` to CardDAV.
- `{uid}` is the user's PocketBase record id: 15 lowercase letters and digits
  that never change. Users sign in with their email, but the email is never
  part of a URL, so changing it doesn't break any configured client. Clients
  find these URLs through discovery, so nobody has to type them. (Decided
  2026-11-22; email URLs were rejected because an email change would break
  every client, and `@` gets percent-encoded inconsistently.)
- The `{uid}` segment must equal the signed-in user. Anything else gets a 403
  before any backend code runs.
- Paths are cleaned (`path.Clean`), and any `..` segment is rejected.
- **Mounting on PocketBase** (checked 2026-11-22 against PocketBase v0.42.3):
  - `/dav` and `/files` are router groups, each with a catch-all
    `Any("/{path...}")` route.
  - Every method reaches the handler intact: OPTIONS, PROPFIND, PROPPATCH,
    REPORT, MKCOL, MKCALENDAR, COPY, MOVE, LOCK, UNLOCK, plus the ordinary
    HTTP methods. Method, path, `Depth`, `Authorization` and body all arrive
    unchanged, and 207 responses and our `DAV:` header go out unchanged.
  - Each DAV group must **unbind four PocketBase global middlewares**:

    | Middleware id | Why it must go |
    |---|---|
    | `pbRejectUnknownToken` | **Critical.** New in v0.42: rejects any `Authorization` value PocketBase doesn't recognise, so every `Basic …` DAV request gets a JSON 401 before our handler runs. Without this unbind no client can sign in |
    | `pbCors` | Answers *every* OPTIONS with 204 as if it were a browser preflight, so clients never see our `DAV:` capability header |
    | `pbBodyLimit` | Rejects bodies over 32 MB with a JSON 413, which breaks file uploads. Nexus sets its own limits (§8) |
    | `pbLoadAuthToken` | Tries to parse `Basic …` as a PocketBase token on every request. Harmless but wasted work; DAV auth is ours (§6) |

  - PocketBase's security headers (`X-Frame-Options`,
    `X-Content-Type-Options`) stay on; they don't bother DAV clients.
  - The fallback plan (wrapping PocketBase's handler) isn't needed.

## 4. Data model

All of these are PocketBase collections.

**The schema is data, not migrations.** A collections snapshot
(`internal/schema/collections.json`) is embedded in the binary and applied in
full on every start with `ImportCollectionsByMarshaledJSON(snapshot, true)`:

- The snapshot is authoritative in both directions. A declared field or
  collection is created or restored. An undeclared one is **deleted along with
  its records**. PocketBase's own system collections (`_superusers` and the
  like) are never deleted.
- `users` is *not* a system collection, so the snapshot must declare it.
  Leaving it out would delete every account on the next start.
- **Never change the schema in the admin UI.** UI edits are overwritten on the
  next start. Schema changes are made in the snapshot and committed. The UI is
  for data and one-off inspection only.
- No project migrations. `migratecmd` is used only for `down` and one-off data
  fixes, and automigrate stays off, since it writes a migration for every UI
  edit.

| Collection | Key fields | Notes |
|---|---|---|
| `users` | built-in auth collection | Email is the DAV login name; the record id is the `{uid}` URL segment |
| `app_passwords` | user, label, `secret_hash`, scopes (`cal` / `card` / `files`), created, last_used_at, revoked_at | §6 |
| `calendars` | owner, slug, name, description, color, components (`VEVENT`, `VTODO`), `sync_seq` | Unique per (owner, slug); default calendar supports both |
| `calendar_objects` | calendar, href, uid, `ics`, etag, component, dtstart, dtend, `seq` | dtstart/dtend are indexed for date-range searches |
| `addressbooks` | owner, slug, name, description, `sync_seq` | Unique per (owner, slug) |
| `contacts` | addressbook, href, uid, `vcf`, etag, fn, email, `seq` | |
| `dav_changes` | collection, href, seq, deleted | Change log and deletion records for sync |
| `storage_accounts` | name, backend type, `options` (encrypted JSON) | Defined by the admin |
| `mounts` | user, account, name, remote_path, read_only | `/files/{uid}/{name}` |

A default calendar ("Personal") and a default address book ("Contacts") are
created when a user is created.

## 5. Calendars and contacts: sync, ETags and writes

- **ETag:** a hash of the stored raw text. It changes exactly when the bytes
  change.
- **Change counter:** each calendar and address book has a counter,
  `sync_seq`, that only ever goes up. Every PUT or DELETE is one SQLite
  transaction that:
  1. checks `If-Match` / `If-None-Match` against the current ETag (so
     conditional writes really are atomic);
  2. writes or deletes the object;
  3. increments `sync_seq` and stamps the object's `seq` with the new value;
  4. adds a `dav_changes` row (`deleted = true` for a DELETE).
- **Sync token:** `https://nexus/ns/sync/{seq}`, reported as `DAV:sync-token`.
  The same value is reported as `CS:getctag`, a calendar-server extension that
  clients use as a cheap "anything changed?" check.
- **Sync requests** (RFC 6578 sync-collection): given a token's `seq`, the
  server returns the latest state of every path with `seq` greater than it.
  Deleted paths come back as 404 entries. An empty token means a full listing.
  An unknown token, or one older than the pruning point, gets
  `403 DAV:valid-sync-token`, and the client resyncs from scratch.
- **Pruning:** `dav_changes` rows older than a retention window (proposed: 90
  days) are deleted. The oldest surviving `seq` per collection is recorded, so
  older tokens can be rejected.
- **Date-range search:** a calendar query first narrows by the indexed
  dtstart/dtend columns, then runs go-webdav's `caldav.Filter` for exact
  matching. That step handles recurring events.
- **PROPPATCH** updates name, description and colour, all or nothing.
- **Validation on PUT** (`internal/store`):
  - one component type and one UID per resource;
  - the component must be one the calendar accepts;
  - UIDs are unique within a collection (409 otherwise);
  - DTSTART, DTEND, DUE, RECURRENCE-ID and DURATION must parse (400
    otherwise), so one malformed object can't break queries on its calendar;
  - resources are capped at 1 MiB (413).
- **Indexed date range:** the earliest start and latest end over all
  components. Recurring events (RRULE or RDATE) get an open end, so the
  database prefilter never drops an occurrence. Tasks without dates are
  always passed on to the exact matcher.
- **Deleting a whole collection** from a client is refused (403); the admin UI
  does it. Deleting a calendar or address book also deletes its `dav_changes`
  rows, via a hook.
- **Pruning** runs daily from PocketBase's cron (`nexusPruneChanges`, 03:17).

## 6. Authentication and authorization

- **DAV and file endpoints** use HTTP Basic: the account email plus an App
  Password. The account's own password is never accepted there; it only works
  for the account page and the admin UI.
- **App Password format:** a random 128-bit secret. Only `sha256(secret)` is
  stored. A fast hash is enough because the secret has high entropy, and
  bcrypt would add real cost to every request (DAVx⁵ makes dozens per sync).
  The secret is shown once, when it's created.
- **Scopes:** each password carries any of `cal`, `card` and `files`. The
  middleware checks the scope against the endpoint before running a handler.
- **`last_used_at`** is written at most once a minute per password.
- **Revoked passwords** fail immediately. Validated credentials may be cached
  in memory for a few seconds, and that cache is cleared on revoke.
- **Isolation:** a user can only reach their own `/dav/{uid}` and
  `/files/{uid}` trees. Public sharing and cross-user access control are out
  of scope.

## 7. File storage

- The file handler is `golang.org/x/net/webdav.Handler`. Its `FileSystem` is
  an adapter over an rclone `vfs.VFS` per mount, and its `LockSystem` is
  in-memory (`webdav.NewMemLS()`). This is the same arrangement as `rclone
  serve webdav`. It gives correct PROPFIND, MOVE and COPY behaviour, plus
  caching and write buffering on top of S3.
- **Storage Manager:** maps `{uid}/{mount}` to a mount, then to a storage
  account. It builds the rclone `fs.Fs` from the account's backend type and
  options, and caches the resulting `vfs.VFS`. The cache is cleared when the
  account or mount changes.
- **Generic backends:** accounts store `{type, options}` exactly as rclone
  takes them, so any rclone backend works. The admin UI provides forms for S3
  and local disk only.
- **Mount isolation:** the mount's `remote_path` is the root of everything
  that mount can reach. The vfs path is built only from the cleaned path below
  the mount name.
- **ETags and conflicts:**
  - The ETag is the backend's MD5 hash where one exists (S3 has it), else size
    plus modification time.
  - `If-Match` is checked before writing. On S3 this check can't be atomic, so
    the documented behaviour is "last writer wins".
  - MOVE and COPY on S3 are copy-then-delete, which isn't atomic either.
- **Read-only mounts** reject every write method with 403.

## 8. Security, reliability and operations

- **TLS:** required in production, ended either by Nexus or by a reverse proxy
  in front of it. Plain HTTP is allowed only on a loopback or dev address.
- **Secrets at rest:**
  - App Passwords are stored as hashes.
  - Storage account options are encrypted with AES-256-GCM, using a key from
    `NEXUS_ENC_KEY`.
  - PocketBase's own settings encryption covers only its settings, not our
    collections.
- **Limits:** a maximum request body size for calendar and contact objects
  (reported to clients as `max-resource-size`), and per-IP rate limits on
  failed sign-ins.
- **Logs:**
  - PocketBase's request log covers raw requests.
  - Nexus adds structured DAV log lines: user, App Password label, method,
    path, Depth, status, duration and client User-Agent. They're tagged so the
    admin UI can filter for sync and error lines.
- **Backup:**
  - SQLite plus config is the primary backup. Use PocketBase's built-in
    backups; check that they include our collections and can be uploaded to
    S3.
  - Object storage is backed up separately, with the provider's versioning or
    an `rclone sync` to a second bucket.
  - The restore procedure goes in the deployment docs and gets tested in
    phase 4.
- **Deployment:** one container image. It has a data volume (the PocketBase
  `pb_data` folder: SQLite, backups, logs) and config set through environment
  variables (`NEXUS_ENC_KEY`, listen address, public URL). A local-disk
  storage account needs its own volume.

## 9. DAV library

- `emersion/go-webdav` v0.8.0 is copied into `internal/dav`. It's MIT
  licensed, and the license file is kept.
- **What upstream provides:** discovery, CRUD, calendar and address-book
  queries, multiget, and conditional PUT.
- **What Nexus added**, as optional backend interfaces:
  - sync-collection (`SyncBackend`);
  - `getctag` and `sync-token` (via `Calendar.SyncToken` and
    `AddressBook.SyncToken`);
  - `supported-report-set`;
  - PROPPATCH (`UpdateBackend`).
- Every local change is listed in `internal/dav/UPSTREAM.md`, so it can be
  offered upstream.
- **Why a copy and not a fork:** a fork means a second repo and a `replace`
  directive. Our changes also touch go-webdav's `internal` package, which
  outside code can't import.
- **Why not write our own:** about 2–3k lines and weeks of client-quirk
  debugging that upstream has already done. Recurring-event matching alone is
  a significant job.
- **Still missing:** MKCALENDAR. Extended MKCOL creates calendars and address
  books, but that hasn't been tried with real clients; the admin UI is the
  supported way to create them.

## 10. Repository layout

```
cmd/nexus/       main: PocketBase app; migratecmd registered with automigrate off
docs/            product and architecture docs (this file)
internal/dav/    the go-webdav copy and Nexus DAV extensions
internal/nexus/  wiring: schema on bootstrap, hooks, cron, DAV routes + unbinds
internal/schema/ embedded collections snapshot (collections.json) + Apply
internal/store/  CalDAV/CardDAV backends on PocketBase collections; shared write path and sync
internal/davserver/ /dav router: principal, calendars/, contacts/; per-user tree check
internal/auth/   DAV authentication (temporary dev account-password auth until 2c)
internal/principal/ authenticated user id in the request context
scripts/probe.sh end-to-end check of a running server (curl)
```

Still to come in phase 2: App Passwords with scopes in `internal/auth`, and
an account page.

## 11. Open technical questions

| Question | Status |
|---|---|
| Which Go DAV library, and does it cover CalDAV/CardDAV fully? | **Resolved:** a go-webdav copy plus our extensions (§9) |
| Exact Thunderbird / DAVx⁵ discovery and sign-in behaviour | **Partly done:** discovery checked with curl; real clients still to test |
| S3 only, or any rclone backend from day one? | **Proposed:** any backend underneath, UI forms for S3 and local only (§7) |
| Versioning and conflict behaviour for S3 files | **Proposed:** checked `If-Match`, last writer wins, documented (§7) |
| Does PocketBase's router pass DAV methods through? | **Resolved** on v0.42.3: yes, once `pbRejectUnknownToken`, `pbCors`, `pbBodyLimit` and `pbLoadAuthToken` are unbound on DAV routes (§3) |
| Does PocketBase's request log record DAV requests? | Open. The throwaway check found no `_logs` rows within a second; PocketBase may write logs in batches. Nexus writes its own DAV log lines anyway (§8) |
| Do PocketBase backups cover our collections and upload to S3? | Open; check in phase 4 (§8) |
