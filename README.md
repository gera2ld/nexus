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
Phase 2 (core service) is in progress. CalDAV and CardDAV work end to end
on SQLite. DAV sign-in is a temporary development mode (account password)
until App Passwords land; without it, `/dav` answers 401.

```bash
go test ./...
go run ./cmd/nexus superuser upsert admin@example.com <password>
go run ./cmd/nexus serve                 # admin UI at http://127.0.0.1:8090/_/

# Development only: let DAV clients sign in with the account password.
NEXUS_DEV_ACCOUNT_PASSWORD_AUTH=1 go run ./cmd/nexus serve
# Probe nexus with a freshly created user (uid = the user's record id):
PROBE_AUTH='user@example.com:password' PROBE_UID=<uid> PROBE_CAL=personal \
  PROBE_BOOK=contacts ./scripts/probe.sh http://127.0.0.1:8090
./scripts/probe.sh http://127.0.0.1:8080   # end-to-end checks
```
