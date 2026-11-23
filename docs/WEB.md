# Nexus — Web UI design

Status: the user and admin dashboards are built (§9 steps 1 and 2); the rest is design · 2026-11-23

Nexus gets a web app with three parts: a user dashboard, an admin dashboard
and a file browser with share links. It replaces the current `/account`
page and lets Nexus replace a separate FileStash. Calendars and contacts
are still read and edited in client apps; the dashboard only manages them.

Product scope: [PRODUCT.md](PRODUCT.md) §3.5. Server architecture:
[ARCHITECTURE.md](ARCHITECTURE.md).

## 1. Decisions

| Decision | Choice |
|---|---|
| Framework | React, React Router, shadcn/ui on Tailwind, TanStack Query; built with Vite. shadcn/ui is React-first; the Vue port lags behind it |
| Shipping | `npm run build` output is embedded in the Go binary (`go:embed`), so Nexus stays one binary and one image |
| Raw files | `/files/{uid}/…` stays the raw content (WebDAV, as now) |
| File browser | `/browser/{uid}/…`: the same path under a different prefix |
| Published folders | Share links at `/s/{token}/…` on the main domain. A folder serves its `index.html`; relative paths are the author's job. No subdomains |
| Isolation | Share responses are sandboxed without `allow-same-origin` (§6.4) |
| Editor | Monaco for every text file, loaded only when a file is opened |
| Deferred | Thumbnails, search, large-file and folder uploads |

## 2. URLs

```
/account/                     user dashboard: overview (replaces the /account page)
/account/app-passwords        App Passwords
/account/calendars            calendars
/account/contacts             address books
/account/shares               share links
/admin/                       admin dashboard: storage accounts and mounts
/browser/                     302 → /browser/{uid}/ (the signed-in user)
/browser/{uid}/               file browser: the user's mounts
/browser/{uid}/{mount}/path/  a folder
/browser/{uid}/{mount}/file   a file, opened in the editor or a preview
/files/{uid}/{mount}/file     the raw file (WebDAV, unchanged)
/s/{token}                    a shared file, or 301 → /s/{token}/ for a folder
/s/{token}/path               a file inside a shared folder
/ui/assets/…                  the web app's built assets (hashed names)
```

- `/browser/…` and `/files/…` mirror each other, so "open raw" and "open in
  browser" swap the prefix.
- The two serve different purposes and never redirect to each other:
  `/files/…` is always raw WebDAV, for clients and raw links; `/browser/…`
  is always the app.
- The Go server answers every `/account*`, `/admin*` and `/browser*` path
  with the app's `index.html`; React Router does the rest.

## 3. Signing in

- **Users** sign in with their email and account password (PocketBase
  `users` auth). **Admins** sign in as PocketBase superusers on `/admin/`.
  The account password still never works for DAV.
- The app keeps the PocketBase token in `sessionStorage` and sends it as
  `Authorization: Bearer` to `/api/*` and `/files/*`.
- **`/files` accepts the dashboard token** as well as App Passwords: a
  `Bearer` token is checked with PocketBase (`FindAuthRecordByToken`) and
  gives the user full access to their own files, like an App Password with
  the `files` scope and no path limit. Browsers never send this header on
  their own, so it opens no CSRF hole.
- **A session cookie for raw links.** Links to `/files/…` opened in a new
  tab carry no header. After sign-in the app calls `POST /api/nexus/session`,
  which sets `nexus_session` (the same token; `HttpOnly`, `Secure`,
  `SameSite=Lax`, `Path=/files/`, expiring with the token). `/files` accepts
  the cookie **only for `GET` and `HEAD`**; writes need the header or an App
  Password. Signing out calls `DELETE /api/nexus/session`.
- User tokens last 5 days (the `users` auth token setting). PocketBase can't
  revoke one token; changing the account password invalidates all of them.

## 4. User dashboard

- **Overview:** server address for calendar and contacts apps, the files
  address, the user's mounts.
- **App Passwords:** create; edit name, scopes and paths in place; revoke.
  The secret is shown once, at creation. Path rules are as now
  (ARCHITECTURE §6), including pasted addresses with the user id.
- **Calendars and address books:** list, create (name, colour, events
  and/or tasks), rename, recolour, delete. Delete asks for confirmation and
  shows how many items will go. Deleting from DAV clients stays refused;
  the dashboard is the user's way to do it.
- **Share links:** list the user's links with their target, expiry and
  whether they have a password; edit expiry and password; revoke.

API (PocketBase user token; all under `/api/nexus`). It is built with
[Huma](https://huma.rocks), which validates requests from the Go types and
generates the OpenAPI 3.1 document (`/api/nexus/openapi.json`, checked in
as `api/openapi.json`). The app's TypeScript types are generated from that
file, so a server change that breaks the app fails `npm run build`. Errors
are RFC 9457 problem documents; validation errors are 422.

| Method and path | Does |
|---|---|
| `GET /me` | user, server addresses, mounts (exists) |
| `GET, POST /app-passwords` | list, create (exists) |
| `PATCH /app-passwords/{id}` | change label, scopes, paths |
| `POST /app-passwords/{id}/revoke` | revoke (exists) |
| `GET, POST /calendars` · `PATCH, DELETE /calendars/{slug}` | manage calendars, through the store's existing create and update code |
| `GET, POST /addressbooks` · `PATCH, DELETE /addressbooks/{slug}` | the same for address books |
| `GET, POST /shares` · `PATCH, DELETE /shares/{id}` | manage share links |
| `POST, DELETE /session` | set or clear the raw-link cookie |

Deleting a calendar or address book goes through the same record delete as
the admin UI, so its change log goes with it.

## 5. Admin dashboard

- **Storage accounts:** list, create, edit, delete (refused while a mount
  uses it, as now).
  - Local: no options.
  - S3: provider, region, endpoint, access key id, and the secret key as a
    write-only field that is never shown again; other rclone options under
    "Advanced" as key/value pairs. The server's existing save hook still
    validates every option against rclone and encrypts the secrets.
  - **Test connection:** lists the top of the account (or a path in it)
    and shows rclone's error if that fails.
- **Mounts:** per user: account, name, remote path, read-only. A remote
  path picker uses the same listing as the connection test.
- Users, logs and backups stay in the PocketBase admin UI (`/_/`), linked
  from the dashboard.
- **The settings form comes from rclone:** the account's main options for
  the chosen S3 provider, with rclone's suggested values (regions,
  endpoints), and every other option under "Advanced" by name. Options
  rclone marks sensitive or passwords (including `access_key_id`) are
  write-only: the dashboard shows only that they are stored.
- Sign-in is a PocketBase superuser at `/admin`, kept apart from the user
  sign-in at `/account` (each page load knows which from its URL).

API (superuser token; all under `/api/nexus/admin`, in the same Huma API
and OpenAPI document as the user dashboard). Saves go through the same
record hooks as the PocketBase admin UI, so option checks and secret
encryption stay in `internal/storage`:

| Method and path | Does |
|---|---|
| `GET /storage-types/{type}/options` | rclone's options for `local` or `s3`: help, secret, advanced, provider filter, examples |
| `GET, POST /storage-accounts` · `GET, PATCH, DELETE /storage-accounts/{id}` | manage accounts. Secrets are returned by name only; a PATCH merges them (an empty value removes one). Delete is refused (409) while a mount uses the account |
| `POST /storage-accounts/{id}/browse` | list a folder (the top when the path is empty: `/` for local disk, the buckets for S3); the connection test and the path picker. Backend errors come back as 422 with rclone's message, without retries |
| `GET /users` | users for the mount form |
| `GET, POST /mounts` · `PATCH, DELETE /mounts/{id}` | manage mounts; a mount's user is fixed at creation |

## 6. Files

### 6.1 File browser

- A tree of mounts and folders on the left, the folder or file on the
  right. Read-only mounts show no write actions.
- Create folders; rename, move, copy and delete (with multi-select);
  upload files by picking or dropping them. Uploads are one `PUT` per file,
  with progress; there are no resumable or folder uploads yet.
- Download a file; "copy raw link" and "open raw" use `/files/…`; "share"
  creates a share link (§6.3).
- It talks WebDAV to `/files` with the dashboard token: `PROPFIND Depth 1`
  to list, `PUT`, `MKCOL`, `MOVE`, `COPY`, `DELETE`. All existing checks
  (read-only mounts, the top level, cross-mount moves) apply unchanged.

### 6.2 Editor and previews

- **Text files open in Monaco**: any `text/*` type, the usual code and
  config types (JSON, YAML, TOML, XML, JS, …), and anything else whose
  first 8 KB contains no NUL byte. Files over 5 MB open read-only with a
  warning. The language comes from the file extension.
- **Saving** is a `PUT` with `If-Match` set to the ETag from when the file
  was opened. A 412 means the file changed on the server: the editor offers
  to reload or overwrite.
- **Previews:** images, PDF, audio and video use the browser's own viewers,
  loaded from the raw link with the session cookie.
- Monaco and its workers are a separate bundle loaded on first use, served
  from Nexus (no CDN).

### 6.3 Raw files

Changes to `GET` and `HEAD` on `/files`:

- **Content types:** the image adds Alpine's `mailcap` package, so Go's
  `mime` package reads the full `/etc/mime.types` table instead of its
  built-in 20 or so types. Text types get `charset=utf-8`. Types still come
  from the backend or the extension, never from the file's contents.
- **Sandboxed:** `Content-Security-Policy: sandbox` and
  `X-Content-Type-Options: nosniff`. A raw HTML or SVG file displays, but
  its scripts don't run, so a file can't act with the viewer's session.
  Running a page's scripts is what share links are for.
- `?download` adds `Content-Disposition: attachment`.
- `Range` requests work as now.

### 6.4 Share links

A share link gives anyone with the link read access to one file or one
folder, optionally with an expiry and a password. Publishing a folder as a
website is a share of a folder with an `index.html`.

**Data:** a `shares` collection:

| Field | |
|---|---|
| `owner` | the user (cascade delete) |
| `mount` | the mount (cascade delete: the link dies with the mount) |
| `path` | file or folder inside the mount |
| `token` | 128-bit random, base62, unique. Stored as it is, so the dashboard can show the link again. A leaked database leaks the links; the alternative was links that can only be shown once |
| `expires_at` | optional |
| `password_hash` | optional, bcrypt (a person types it, unlike App Passwords) |
| `listing` | for a folder without `index.html`: show a file list, or 404 |
| `created`, `last_accessed_at` | `last_accessed_at` throttled like App Passwords |

**Serving `/s/{token}/…`** (`GET` and `HEAD` only; no sign-in):

- Unknown, expired or revoked token: 404 (not 403, so tokens can't be
  probed).
- The rest of the path is cleaned and must stay inside the shared folder;
  `..` is rejected. A file share serves only `/s/{token}`.
- A folder: `/s/{token}` redirects to `/s/{token}/` so relative links
  resolve; a folder serves its `index.html`, else a listing or 404; a
  missing file serves the share's `404.html` if it has one.
- Files are served with `http.ServeContent` (types as in §6.3, `Range`,
  `If-None-Match`).
- **Headers on every response:**
  - `Content-Security-Policy: sandbox allow-scripts allow-forms allow-popups allow-downloads`.
    Without `allow-same-origin` the page gets an opaque origin: its
    scripts run, but they can't read the dashboard's storage, can't send
    the session cookie (it's `SameSite`), and can't use another share's
    password cookie. The cost: published pages can't use `localStorage`,
    IndexedDB or cookies. If that's ever needed, shares move to a second
    hostname.
  - `Referrer-Policy: no-referrer`, so the token doesn't leak to sites the
    page links to or loads from.
  - `X-Content-Type-Options: nosniff`.
  - `Cache-Control: private, no-cache` for password-protected shares.
- **Passwords:** without a valid cookie, `/s/{token}/…` shows a password
  form (Nexus's own page, strict CSP). `POST /s/{token}` checks the
  password and sets `nexus_share` with `Path=/s/{token}/`, `HttpOnly`,
  `Secure`, `SameSite=Lax`: an HMAC over the token and an expiry (12
  hours), keyed by a secret Nexus generates and keeps in its settings.
  Changing the password changes the HMAC input, so old cookies stop
  working. Failed attempts count towards the same per-address limit as DAV
  sign-ins.
- Share requests are logged with `meta.share` (the share id), so the admin
  log shows who downloads what.

## 7. Build and code layout

```
web/                 the React app (Vite, TypeScript); npm
api/openapi.json     the generated API document the app's types come from
internal/web/        go:embed of the build (internal/web/dist, not
                     committed), the SPA routes, asset caching
internal/account/    /api/nexus user API (grows: calendars, shares, session)
internal/shares/     the shares collection hooks and /s/ serving
```

- **Dockerfile:** a `node` stage on `$BUILDPLATFORM` runs `npm ci && npm
  run build`; the Go stage copies `internal/web/dist` in before `go build`. The
  output is plain JavaScript, so no emulation is involved.
- **Development:** `npm run dev` in `web/` (Vite on :5173, proxying `/api`,
  `/files` and `/dav` to Nexus on :8090). After changing the API, run
  `UPDATE_OPENAPI=1 go test ./internal/account` to refresh
  `api/openapi.json`; a test fails while it is stale. Without a build, the Go binary
  serves a placeholder page, so `go test` and `go run` need no Node.
- **Security headers for the app:** `default-src 'self'`; `script-src
  'self'`; `style-src 'self' 'unsafe-inline'` (Monaco injects styles);
  `worker-src 'self' blob:`; `img-src`, `media-src` and `frame-src` `'self'
  blob:` for previews; `frame-ancestors 'none'`.
- Hashed assets are cached for a year; `index.html` is `no-store`.

## 8. Testing

- **Go tests** for every server rule: Bearer and cookie on `/files` (the
  cookie refused for writes), content types and the
  sandbox header, the new APIs (each refusing other users' records), and
  share serving: expiry, password and cookie, path confinement, `index.html`,
  `404.html`, listing on and off, the security headers, and that a share
  can't reach outside its folder or another user's mount.
- **The app** is checked in a browser against a local Nexus for each
  feature; component tests only where logic lives in the front end (the
  WebDAV listing parser, text detection).

## 9. Build order

Each step is usable on its own; the riskiest comes last.

1. The app shell, sign-in and the user dashboard, replacing `/account`.
   **Built** (without share links, which come with step 4).
2. The admin dashboard. **Built.**
3. Raw-file changes (§6.3), the Bearer token and cookie on `/files`, and the
   file browser with Monaco.
4. Share links.

## 10. Open questions

| Question | Proposal |
|---|---|
| Let users delete calendars and address books from the dashboard? | Yes, with confirmation; DAV clients still can't |
| Store share tokens as they are, or only a hash? | As they are, so links can be shown again (§6.4) |
| Upload size limit | None on the server; the browser holds one file at a time, so very large files wait for resumable uploads |
| Share links that accept uploads | Not now |
