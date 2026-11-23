# syntax=docker/dockerfile:1
# Nexus container image. See docs/DEPLOY.md.
#
#   docker build -t nexus .                            # this machine's platform
#   docker buildx build --platform linux/amd64 -t nexus --load .
#   docker run -p 8090:8090 -v nexus-data:/pb_data -e NEXUS_ENC_KEY=... nexus

# The web app is plain files, so it's built once on the builder's platform.
FROM --platform=$BUILDPLATFORM node:lts-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY api /src/api
COPY web ./
# Writes to /src/internal/web/dist, which the Go build embeds.
RUN npm run build

# The Go build stage always runs on the builder's native platform and Go
# cross-compiles for the target; only the short RUN in the final stage runs
# under emulation when building for another platform.
FROM --platform=$BUILDPLATFORM golang:1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/internal/web/dist ./internal/web/dist
# Pure Go (PocketBase uses a CGo-free SQLite), so the binary is static and
# cross-compiles. BuildKit supplies TARGETOS/TARGETARCH; declaring them puts
# them in scope.
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/nexus ./cmd/nexus

FROM alpine:3
# Alpine has CA certificates (for S3 over TLS); add time zone data, and
# mailcap for /etc/mime.types, the full table Go reads file types from. The
# data and local-storage folders belong to the nexus user, so named volumes
# mounted there start out writable.
RUN apk add --no-cache tzdata mailcap \
 && adduser -D -H -u 65532 nexus \
 && mkdir /pb_data /files \
 && chown nexus:nexus /pb_data /files
COPY --from=build /out/nexus /nexus
USER nexus
# PocketBase keeps its data next to the binary: /pb_data.
VOLUME /pb_data
EXPOSE 8090
ENTRYPOINT ["/nexus"]
CMD ["serve", "--http=0.0.0.0:8090"]
