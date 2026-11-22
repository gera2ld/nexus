# Nexus

A lightweight, self-hosted personal DAV server. It serves CalDAV, CardDAV and
WebDAV files from one Go binary with one authentication system, and is built
for Thunderbird, DAVx⁵ and ordinary WebDAV clients.

- [Product design](docs/PRODUCT.md): goals, features, scope, roadmap
- [Architecture](docs/ARCHITECTURE.md): components, data model, sync, auth,
  storage, operations
- [Vendored DAV library notes](internal/dav/UPSTREAM.md)

## Status

Phase 1 (DAV library) is done apart from testing with real clients. The
next phase is the PocketBase core service.

```bash
go test ./...
./scripts/probe.sh http://127.0.0.1:8080   # end-to-end checks
```
