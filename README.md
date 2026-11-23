# Nexus

A small self-hosted server for your calendars, contacts and files. One Go
binary serves CalDAV, CardDAV and WebDAV, with one sign-in for all of
them.

- **Calendars and tasks** (CalDAV) for Thunderbird, DAVx⁵ and other apps.
- **Contacts** (CardDAV).
- **Files** (WebDAV) for Finder, rclone and other WebDAV clients, stored on
  local disk or S3. Each user sees the folders the admin gives them.
- **App Passwords:** each device gets its own password, limited to
  calendars, contacts or files, and revocable at any time. Users manage
  them on their account page.
- **Admin UI, logs and backups** from [PocketBase](https://pocketbase.io).

## Get started

1. Run the Docker image `ghcr.io/gera2ld/nexus` behind your reverse proxy:
   [docs/DEPLOY.md](docs/DEPLOY.md) covers the image, the encryption key,
   Caddy, backups and logs.
2. In the admin UI at `https://dav.example.com/_/`, create a user. There is
   no public sign-up.
3. The user signs in at `https://dav.example.com/account` and creates an
   App Password for each device.

## Connect your apps

**Calendars and contacts:** add a CalDAV or CardDAV account with the server
address `https://dav.example.com/`, your email and an App Password. The app
finds your calendars and address books by itself.

- Thunderbird: New Calendar → On the Network; New Address Book → CardDAV.
- DAVx⁵: Add account → Login with URL and user name.

**Files:** connect to the address shown on your account page,
`https://dav.example.com/files/<your id>/`, with an App Password that has
Files access.

- macOS: Finder → Go → Connect to Server.
- rclone: a `webdav` remote with vendor `other`.

See [docs/CLIENTS.md](docs/CLIENTS.md) for what has been checked with each
app.

## Limits

- Apps can create calendars and address books, but not delete them;
  deleting one (and everything in it) is done in the admin UI.
- File modification times set by apps aren't stored.

## Development

```bash
go test ./...
export NEXUS_ENC_KEY=$(openssl rand -base64 32)  # encrypts storage secrets
go run ./cmd/nexus superuser upsert admin@example.com <password>
go run ./cmd/nexus serve                         # http://127.0.0.1:8090/_/
```

`scripts/probe.sh` checks a running server end to end with curl.

- [Product design](docs/PRODUCT.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Web app design](docs/WEB.md)
- [Vendored DAV library notes](internal/dav/UPSTREAM.md)
