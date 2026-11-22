# Vendored go-webdav

Copied from github.com/emersion/go-webdav v0.8.0 (MIT, see LICENSE) on
2026-11-22. `cmd/` was dropped and import paths rewritten to
`nexus/internal/dav`. This is now Nexus code; local changes are listed below
so they can be offered upstream.

## Local changes

- Dropped upstream CI and repo files (`.build.yml`, `.github/`, `.gitignore`).
- `internal/elements.go`: `Status.UnmarshalText` error messages printed the receiver instead of the input (vet failure).
- `carddav/client.go`: `SyncCollection` detected deleted items with a type
  assertion that never matched the wrapped error, so deletions came back as
  errors. Now uses `errors.As`, matching the CalDAV client.
- **sync-collection REPORT (RFC 6578)** on calendars and address books, via
  the optional `caldav.SyncBackend` / `carddav.SyncBackend` interfaces.
  Backends return `webdav.ErrInvalidSyncToken` for unknown or expired tokens,
  which becomes `403 DAV:valid-sync-token`. `SyncResponse.Truncated` emits the
  RFC's 507 entry when a `DAV:limit` cuts the result short.
- **`DAV:sync-token` and `CS:getctag`** on collections, from the new
  `Calendar.SyncToken` / `AddressBook.SyncToken` fields.
- **`DAV:supported-report-set`** on collections; lists sync-collection only
  when the backend implements it.
- **PROPPATCH** on calendars (displayname, calendar-description,
  calendar-color) and address books (displayname, addressbook-description),
  via the optional `UpdateBackend` interfaces. All-or-nothing per RFC 4918:
  any unknown property fails the whole request (403 / 424).
- Shared helpers live in `internal/sync.go`; protocol glue in
  `caldav/sync.go` and `carddav/sync.go`; tests in `*/sync_test.go`.

- MKCALENDAR (RFC 4791 §5.3.1) through an optional `MkcalendarBackend`
  interface in `internal`; the CalDAV backend implements it with the same
  create path as extended MKCOL. Both now pass the body's
  calendar-description, calendar-color and supported-calendar-component-set
  to `CreateCalendar` (upstream kept only the display name).
