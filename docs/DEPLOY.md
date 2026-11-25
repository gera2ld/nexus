# Deploying Nexus

Nexus is one container behind a Caddy reverse proxy that handles TLS. This
guide was checked on 2026-11-23 with an image built from this repo, Docker
29.8 and Caddy 2.

## 1. Get the image

The image is published on GitHub Container Registry for amd64 and arm64:

```bash
docker pull ghcr.io/gera2ld/nexus:latest
```

The Image workflow builds on every push to `main` that changes more than
docs, so `latest` follows `main`; **Actions → Image → Run workflow** builds
by hand. If the package is private, the server needs `docker login
ghcr.io` with a token that can read packages first.

To build it yourself instead:

```bash
docker build -t nexus .                                      # for this machine
docker buildx build --platform linux/amd64 -t nexus --load .  # e.g. on an Apple Silicon Mac for an x86 server
```

The build stage always runs on the build machine's own platform and Go
cross-compiles for the target, so building for another architecture is
fast: only the final stage's one short `RUN` step runs under emulation.
Use `nexus` instead of the GHCR name below.

The image is about 49 MB: a static binary on Alpine with CA certificates
and time zone data, running as the `nexus` user (uid 65532). It has a
shell, so `docker compose exec nexus sh` works for poking around.

| Path in the container | What |
|---|---|
| `/nexus` | the binary |
| `/pb_data` | PocketBase data: SQLite databases, backups, uploaded files. **Mount a volume here.** |
| `/files` | an empty folder for local-disk storage; mount a volume here if you use local storage |

The container listens on port 8090.

## 2. The encryption key

`NEXUS_ENC_KEY` encrypts storage account secrets (S3 keys). Generate it once:

```bash
openssl rand -base64 32
```

Keep it somewhere safe **apart from** your backups. A backup without the key
can't open S3 mounts, and a malformed key stops Nexus at startup. Without
any key Nexus still runs, but saving storage secrets fails.

## 3. Run it

A Compose file next to your Caddy setup:

```yaml
services:
  nexus:
    image: ghcr.io/gera2ld/nexus:latest
    restart: unless-stopped
    environment:
      NEXUS_ENC_KEY: ${NEXUS_ENC_KEY}     # from an .env file that isn't committed
    volumes:
      - nexus-data:/pb_data
      - nexus-files:/files                # only for local-disk storage
    networks: [caddy]                     # the network Caddy is on; no published ports

volumes:
  nexus-data:
  nexus-files:

networks:
  caddy:
    external: true
```

Named volumes start out owned by the container user. With bind mounts
instead, make the host folders writable by uid 65532.

Create the first admin (superuser) account:

```bash
docker compose exec nexus /nexus superuser upsert you@example.com 'a long password'
```

## 4. Caddy

```caddy
dav.example.com {
	reverse_proxy nexus:8090
}
```

That is all Caddy needs. Its defaults are what WebDAV wants: it passes the
`Host` header through (WebDAV MOVE and COPY compare it with the
`Destination` header), sets `X-Forwarded-For` and `X-Forwarded-Proto`,
streams request bodies without a size limit, and handles TLS
certificates. Checked: calendars, a 50 MB upload, MOVE, the
`/.well-known/caldav` redirect, and the server address shown on the account
page.

## 5. Tell PocketBase about Caddy (required)

In the admin UI (`https://dav.example.com/_/`): **Settings → Application →
IP proxy headers**, add `X-Forwarded-For`, and leave "use leftmost IP"
off.

Without this, every request seems to come from Caddy's address. Nexus
blocks an address for 5 minutes after 10 failed DAV sign-ins, so one
device with an old or revoked App Password would lock out every device.
Nexus logs a warning the first time a proxied request arrives while this
setting is empty. Checked: without the setting, failures through one proxy
didn't block the same client through another; with it, they did.

## 6. First setup

1. Admin UI → `users` → New record: email and account password. There is
   no public sign-up.
2. Each user signs in at `https://dav.example.com/` and creates an
   App Password per device.
3. Calendar and contacts apps: server `https://dav.example.com/`, the
   email, an App Password. Discovery finds the rest.
4. Files: sign in at `https://dav.example.com/admin` as the superuser.
   Storage → New storage account (local disk; S3 with its provider,
   region, endpoint and keys; or a WebDAV server with its URL, vendor,
   user and password); Browse checks that it connects. Then Mounts →
   New mount gives a user a folder of it. Users connect to the address the
   dashboard shows,
   `https://dav.example.com/dav/files/{id}/`, with an App Password that has
   Files access, or browse them on the Files page
   (`https://dav.example.com/files`). Local-disk mounts use paths under
   `/files` in the container.

## 7. Backups and restore

- **Back up:** admin UI → Settings → Backups. Schedule them there, and
  optionally have PocketBase upload them to S3. The zip contains
  everything in `/pb_data`: accounts, App Password hashes, calendars,
  contacts and storage settings.
- **Not in the backup:** `NEXUS_ENC_KEY` (store it separately), and the
  contents of file mounts (use S3 versioning or an `rclone sync` to a
  second bucket; for local disk, back up the `nexus-files` volume).
- **Restore into the running server:** admin UI → Settings → Backups →
  Restore. PocketBase replaces `/pb_data` and restarts itself (checked).
- **Restore onto a new machine:** create the `nexus-data` volume, unzip the
  backup into it, start the container with the **same** `NEXUS_ENC_KEY`.

## 8. Logs

Admin UI → Logs. Every DAV and file request is one entry; Nexus adds who
signed in and how it went under `meta`. Useful filters:

- `data.meta.signin = "failed"`: wrong or revoked App Passwords (the email
  is recorded as typed, so typos show).
- `data.meta.user = "ada@example.com"`: one person's devices.
- `data.meta.appPassword = "Pixel phone"`: one device.
- `data.meta.signin = "scope"`: an App Password without access to what the
  app asked for.

A client's first request without credentials shows as a 401 with `signin`
`none`; that is normal.

## 9. Upgrading

`docker compose pull && docker compose up -d` (or rebuild the image and
recreate the container). The schema is applied from
the binary on every start, so there are no migrations to run. Take a
backup first.
