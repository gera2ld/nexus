# Client compatibility

What has been checked against real clients, what each one does, and how
to check a new one.

| Client | Used for | Status |
|---|---|---|
| Thunderbird 160 (macOS) | Calendars, contacts | Works |
| rclone v1.76.0 | Files | Works |
| macOS Finder (WebDAVFS 3.0.0) | Files | Works |
| DAVx⁵ | Android calendars and contacts | Not checked yet |
| Windows Explorer | Files | Not checked yet |

## Thunderbird

Checked 2026-11-23 against a local server. Set up with only the server
address, the email and an App Password: New Calendar → On the Network →
Find Calendars, and New Address Book → CardDAV. Both were found on the
first try.

- **Discovery:** it first tries `HEAD`, `GET`, `PROPFIND` and `PUT` on `/`
  (`GET` and `HEAD` get the dashboard's page, `PROPFIND` and `PUT` a 404;
  harmless), then follows `/.well-known/caldav` or
  `/.well-known/carddav` to `/dav/`, finds the principal and the home set,
  and lists the collections. Each new connection starts with one
  unauthenticated request (401, `signin: none` in the log).
- **Both ways:** an event and a contact PUT on the server showed up after a
  sync. Creating, editing and deleting in Thunderbird all reached the
  server, and the server ended with exactly what Thunderbird showed.
- **Found and fixed:** a PUT that replaced an event or contact answered
  `201 Created`; it now answers `204 No Content`.

## rclone

Checked 2026-11-23 with the `webdav` remote, vendor `other`, on local-disk
mounts: copy, check, sync with deletes, a second copy transferring
nothing; unicode, `%`, `#`, spaces, empty files and empty folders
byte-identical; 300 MB up and down; server-side move and directory move
within a mount and between mounts; read-only and top-level writes refused
with Nexus's messages.

Found and fixed: moves between mounts failed outright, and MKCOL on an
existing mount gave 403 instead of 405.

## macOS Finder

Checked 2026-11-23 with WebDAVFS 3.0.0, which Finder's Connect to Server
uses (driven with `mount_webdav`). It mounts read-write (it needs LOCK);
create, append, overwrite, nested folders with accents, a 50 MB copy,
rename, and moves of files and folders between mounts all work. A
read-only mount and the top level give "Operation not permitted".

- `touch` doesn't change modification times: properties are accepted but
  not stored.
- Extended attributes such as Finder tags are stored by macOS as `._name`
  files next to the file, as on any non-Apple WebDAV server, and removed
  with it.

## Known limits

- **Creating collections:** clients can create calendars (MKCALENDAR or
  extended MKCOL) and address books (extended MKCOL), with a name,
  description, colour and component set. This is covered by tests but no
  real client has done it yet. Calendars hold events and tasks only; a
  request for journals alone is refused.
- **Deleting collections** from a client is refused (403); the user
  dashboard deletes a calendar or address book and everything in it.
- **S3** hasn't been checked against a real bucket yet. Nexus passes
  options to rclone's s3 backend, which rclone maintains and tests. Until
  2026-11-23 Nexus didn't pass rclone's defaults, so S3 accounts couldn't
  open ("chunk size: 0 is less than 5Mi"); the admin dashboard's Browse
  button is the quickest check after deploying.

## Checking a client

1. Set it up with the server address (`https://dav.example.com/`), the
   email and an App Password. For files, use the address the dashboard
   shows.
2. Create, edit and delete something on each side, and sync.
3. Admin UI → Logs, filtered by `data.meta.user` or `data.meta.appPassword`,
   shows every request the client made, its status and how sign-in went.

For DAVx⁵: Add account → "Login with URL and user name".

`scripts/probe.sh` runs the same checks with curl, without a client:

```bash
PROBE_AUTH='ada@example.com:<app-password>' PROBE_UID=<user id> \
  PROBE_FILES_MOUNT=<empty writable mount> ./scripts/probe.sh https://dav.example.com
```

It needs a user with an untouched default calendar and address book;
`PROBE_FILES_MOUNT` is optional.
