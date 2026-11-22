# Nexus

A lightweight, self-hosted personal DAV server. It serves CalDAV, CardDAV and
WebDAV files from one Go binary with one authentication system, and is built
for Thunderbird, DAVx⁵ and ordinary WebDAV clients.

- [Product design](docs/PRODUCT.md): goals, features, scope, roadmap
- [Architecture](docs/ARCHITECTURE.md): components, data model, sync, auth,
  storage, operations
- [Vendored DAV library notes](internal/dav/UPSTREAM.md)

## Status

Phase 1 (DAV library) is done apart from testing with real clients.
Phase 2 (core service) is done: CalDAV and CardDAV work end to end on
SQLite, and DAV clients sign in with an App Password created on the account
page. Phase 3 (file storage) is in progress: WebDAV files work on local disk
at `/files/{uid}/`; testing with real clients (Finder, Windows Explorer, rclone) is next.

```bash
go test ./...
go run ./cmd/nexus superuser upsert admin@example.com <password>
export NEXUS_ENC_KEY=$(openssl rand -base64 32)  # encrypts storage secrets; keep it safe
go run ./cmd/nexus serve                 # admin UI at http://127.0.0.1:8090/_/
# Create a user in the admin UI (there is no public sign-up), then sign in
# as that user at http://127.0.0.1:8090/account and create an App Password.

# For files, add a storage account (type local) and a mount for the user in
# the admin UI, and give the App Password Files access.

# Probe nexus as that user (uid = the user's record id, shown in the admin UI;
# PROBE_FILES_MOUNT is optional and must name an empty, writable mount):
PROBE_AUTH='user@example.com:<app-password>' PROBE_UID=<uid> PROBE_CAL=personal \
  PROBE_BOOK=contacts PROBE_FILES_MOUNT=home ./scripts/probe.sh http://127.0.0.1:8090
./scripts/probe.sh http://127.0.0.1:8080   # end-to-end checks
```
