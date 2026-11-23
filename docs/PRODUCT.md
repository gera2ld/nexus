# Nexus — Product Design

Status: draft · Last updated 2026-11-23 · Supersedes the Notion page
"Nexus — Personal DAV Server".

How it's built: [ARCHITECTURE.md](ARCHITECTURE.md).

## 1. What Nexus is

Nexus is a self-hosted personal server for calendars, contacts and files. It
serves **CalDAV, CardDAV and plain WebDAV files** from one service, with one
set of users and one authentication system, plus a web app for managing it
and for working with files.

Calendars and contacts are read and edited in the apps people already use;
the web app only manages them. Files work both ways: in WebDAV clients, and
in the web app's file browser and editor, with share links for anything
that should be public.

### Who it's for

One person or a household running their own server. The person who deploys it
is also its admin. There are a handful of users, each with their own data.

### Primary clients

| Client | Used for |
|---|---|
| Thunderbird | Desktop calendars and contacts |
| DAVx⁵ | Android system calendars and contacts |
| Generic WebDAV clients (Finder, Windows Explorer, rclone, mobile file managers) | Files |

Every release is checked against these clients. If a client and the spec
disagree, the client wins, as long as the spec still allows what we do.

## 2. Goals

1. **Replace four services with one.** The current setup is Baïkal (CalDAV and
   CardDAV), rclone's WebDAV server (files), a separate auth proxy and
   FileStash (web file browser). Nexus replaces all four.
2. **Stay standards-compatible and client-focused.** Implement what the target
   clients actually use, correctly, rather than every optional RFC feature.
3. **Reuse rclone's storage backends**, S3 first, instead of writing our own
   cloud storage drivers.
4. **A focused web app:** user and admin dashboards, a file browser and
   share links. No web calendar or contacts app; client apps do that best.
5. **Keep it simple to run:** one Docker container, one database file and one
   config.

## 3. Features (initial release)

### 3.1 Calendars (CalDAV)

- Multiple calendars per user. A default calendar is created with every new
  user.
- Stores and serves iCalendar resources: events (VEVENT) and tasks (VTODO).
  A calendar can hold events, tasks or both.
- Standard create, read, update and delete, plus discovery: clients find
  everything from the server URL, the account email and an App Password.
- Reliable incremental sync. Every object has an ETag and every calendar has a
  sync token, so a client only downloads what changed.
- Clients can rename and recolour calendars.
- Supports date-range searches, which clients use to load a visible week or
  month.

### 3.2 Contacts (CardDAV)

- Multiple address books per user. A default one is created with every new
  user.
- Stores and serves vCard resources (vCard 3.0 and 4.0).
- Standard create, read, update and delete, plus discovery.
- ETags and sync tokens, as for calendars.
- Clients can rename address books.

### 3.3 Files (WebDAV)

- Browse directories; upload, download, delete and move or copy files; create
  directories.
- Files live in S3 or any other rclone backend, including the local
  filesystem.
- Uses the same users, App Passwords and admin as calendars and contacts.
- ETags and conditional requests where the storage backend supports them.
- WebDAV locking (LOCK/UNLOCK), so macOS Finder and Windows Explorer can
  write. Locks are kept in memory and lost on restart.
- Out of scope unless a target client needs it: incremental file sync
  (RFC 6578 on file trees).

### 3.4 Administration

Nexus offers a minimal web interface for:

- **Users:** create, disable and delete them, and reset their passwords.
- **Calendars and address books:** create, rename and delete them for any
  user. Clients can create them too, but only the admin deletes them.
- **Storage:** configure storage accounts (S3 credentials, local paths) and
  mount them into a user's file space.
- **App Passwords:** generate and revoke one password per device or app. Each
  can be limited to calendars, contacts and/or files, and optionally to
  specific folders (e.g. one calendar or one file mount). The account
  password never works for DAV.
- **Logs:** request, sync and error logs, to answer "why isn't my phone
  syncing?".

There is no web calendar or contacts app in the initial release.

### 3.5 Web app (dashboards built; file browser and share links designed)

Full design: [WEB.md](WEB.md).

- **User dashboard** (replaces `/account`): App Passwords with editable
  name, scopes and paths; calendars and address books (create, rename,
  recolour, delete); the user's share links.
- **Admin dashboard:** storage accounts with proper forms for local disk
  and S3 and a connection test; mounts per user. Users, logs and backups
  stay in the PocketBase admin UI.
- **File browser** at `/browser/…`, mirroring the raw `/files/…` paths: a
  file tree, uploads, rename, move, copy and delete, previews, and every
  text file in a code editor (Monaco) with conflict detection on save.
- **Raw links** (`/files/…`) open in a browser tab with the right content
  type. They're sandboxed, so an HTML file's scripts don't run there.
- **Share links** (`/s/{token}/…`) for a file or a folder, public or with a
  password, optionally expiring. A shared folder with an `index.html` is a
  published website; its relative paths are its author's job.
- Not yet: thumbnails, search, large-file and folder uploads.

## 4. Out of scope for the initial release

- Mail: no SMTP or IMAP.
- A rich web UI for calendars or contacts.
- Team collaboration and complex permissions (ACLs). Share links (§3.5)
  are the only way to give others access.
- Groupware: meeting invitations, scheduling (iTIP/iMIP), free-busy and
  notifications.
- Incremental sync of file trees, unless client testing shows a need.

## 5. Quality requirements

- **Security:** HTTPS is required in production. DAV clients sign in only with
  revocable App Passwords. No user can reach another user's data. Credentials
  and storage secrets are never stored in plain text.
- **Data safety:** conditional writes (ETags) stop two clients silently
  overwriting each other's edits. S3 operations such as move are documented as
  not atomic.
- **Recoverability:** a documented backup and restore covers the database and
  configuration. Remote object storage needs its own, separately documented
  backup strategy.
- **Compatibility:** Thunderbird and DAVx⁵ each set up an account from just the
  server URL, the account email and an App Password, with no hand-written paths.

## 6. Open product decisions

| Decision | Proposal | Status |
|---|---|---|
| WebDAV LOCK/UNLOCK in v1 | **Yes**, with in-memory locks. It costs almost nothing in our file stack, and macOS Finder and Windows Explorer won't write to a WebDAV share without it. The original spec ruled it out. | **Decided** 2026-11-22 |
| Tasks (VTODO) in v1 | **Yes.** We store raw iCalendar, so tasks cost almost nothing to add. | **Decided** 2026-11-22 |
| Who owns storage accounts | Only the admin defines storage accounts; users get mounts assigned to them. Users never see or enter storage credentials. | **Decided** 2026-11-22 |
| Storage backends in v1 | Any rclone backend works underneath. The admin UI offers forms only for S3 and local disk. | Proposed |
| Where users manage their own App Passwords | The user dashboard served by Nexus at `/`. The PocketBase dashboard is for admins only. | **Decided** 2026-11-22 (built) |

## 7. Status

Everything in this document's v1 scope is built: calendars and contacts
on SQLite, App Passwords and the user dashboard, file mounts on local disk
or S3 through rclone, request logs, backup and restore, and a Docker
image. Thunderbird, rclone and Finder are checked; DAVx⁵ and Windows
Explorer are still to check ([CLIENTS.md](CLIENTS.md)). Of the web app
(§3.5), the user and admin dashboards are built; the file browser and
share links are designed.

**v1 is done when** the Baïkal + rclone + auth-proxy setup can be switched
off, with Thunderbird and DAVx⁵ syncing both ways through Nexus.
