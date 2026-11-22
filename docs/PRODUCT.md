# Nexus — Product Design

Status: draft · Last updated 2026-11-22 · Supersedes the Notion page
"Nexus — Personal DAV Server".

How it's built: [ARCHITECTURE.md](ARCHITECTURE.md).

## 1. What Nexus is

Nexus is a lightweight, self-hosted personal DAV server. It serves
**CalDAV, CardDAV and plain WebDAV files** from one service, with one set of
users and one authentication system.

It is built for existing clients, not as a web app. Users read and edit their
calendars, contacts and files in the apps they already use. Nexus's own web UI
covers only administration.

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

1. **Replace three services with one.** The current setup is Baïkal (CalDAV and
   CardDAV), rclone's WebDAV server (files) and a separate auth proxy. Nexus
   replaces all three.
2. **Stay standards-compatible and client-focused.** Implement what the target
   clients actually use, correctly, rather than every optional RFC feature.
3. **Reuse rclone's storage backends**, S3 first, instead of writing our own
   cloud storage drivers.
4. **Keep the web UI minimal.** The clients provide the main user experience.
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
- Out of scope unless a target client needs it: incremental file sync
  (RFC 6578 on file trees). Whether WebDAV locking (LOCK/UNLOCK) is in v1 is
  still open (see §6).

### 3.4 Administration

Nexus offers a minimal web interface for:

- **Users:** create, disable and delete them, and reset their passwords.
- **Calendars and address books:** create, rename and delete them for any
  user. Clients can't create collections themselves yet.
- **Storage:** configure storage accounts (S3 credentials, local paths) and
  mount them into a user's file space.
- **App Passwords:** generate and revoke one password per device or app. Each
  can be limited to calendars, contacts and/or files. The account password
  never works for DAV.
- **Logs:** request, sync and error logs, to answer "why isn't my phone
  syncing?".

There is no web calendar or contacts app in the initial release.

## 4. Out of scope for the initial release

- Mail: no SMTP or IMAP.
- A rich web UI for calendars or contacts.
- Team collaboration, complex permissions (ACLs) and public sharing.
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
| WebDAV LOCK/UNLOCK in v1 | **Yes.** It costs almost nothing in our file stack, and macOS Finder and Windows Explorer won't write to a WebDAV share without it. The original spec ruled it out. | Needs sign-off |
| Tasks (VTODO) in v1 | **Yes.** We store raw iCalendar, so tasks cost almost nothing to add. | **Decided** 2026-11-22 |
| Who owns storage accounts | Only the admin defines storage accounts; users get mounts assigned to them. | Needs sign-off |
| Storage backends in v1 | Any rclone backend works underneath. The admin UI offers forms only for S3 and local disk. | Proposed |
| Where users manage their own App Passwords | A small account page served by Nexus at `/account`. The PocketBase dashboard is for admins only. | **Decided** 2026-11-22 (built) |

## 7. Roadmap

| Phase | Scope | Status |
|---|---|---|
| 1. DAV library | Pick the Go DAV library and check it against Thunderbird and DAVx⁵; confirm the CalDAV/CardDAV methods and sync behaviour we need | Library chosen and extended. Checked with curl; **real-client testing still to do** |
| 2. Core service | PocketBase users, App Passwords, calendars and contacts stored in SQLite, CalDAV and CardDAV | Next |
| 3. Storage | rclone integration, S3 configuration, per-user mounts, WebDAV file operations | — |
| 4. Hardening | Real-client testing, conditional updates, error handling, backup and restore, Docker image | — |

**v1 is done when** the Baïkal + rclone + auth-proxy setup can be switched
off, with Thunderbird and DAVx⁵ syncing both ways through Nexus.
