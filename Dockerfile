# The image the homelab pulls: one binary, no shell, no package manager.
#
# Three stages, per D8 in docs/tech-stack.md. The published image is linux/amd64
# only, but both build stages are still pinned to $BUILDPLATFORM and the runtime
# stage still has no RUN - which is what lets an arm64 dev machine cross-build
# the amd64 image with no QEMU: Go cross-compiles, and nothing ever executes a
# target-arch binary at build time. D8 has the numbers. Add a RUN to the runtime
# stage and that stops being true.

# --- 1. the SPA -------------------------------------------------------------
FROM --platform=$BUILDPLATFORM oven/bun:1.3-alpine AS web-build
WORKDIR /src/web

# Dependencies first so a source-only edit doesn't reinstall them.
COPY web/package.json web/bun.lock web/bunfig.toml ./
RUN bun install --frozen-lockfile

COPY web/ ./
RUN bun run build

# --- 2. the binary ----------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.26 AS go-build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# .dockerignore excludes web/build, so this COPY is the only way the SPA gets
# here - which is the point: the embedded frontend is always the one this build
# just produced, never a stale local one. web/dist.go embeds all:build, so
# without this line the build fails outright rather than shipping something old.
COPY --from=web-build /src/web/build ./web/build

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/server

# --- 3. the runtime ---------------------------------------------------------
# static-debian12 has no shell, no package manager, and nothing to exploit that
# isn't the app itself. :nonroot runs as uid 65532 - the number the README tells
# operators to chown the upload directory to.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /out/app /app

EXPOSE 8080

# Exec form, not the string form: there is no shell here to parse it. The binary
# probes itself - see cmd/server/healthcheck.go.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app", "healthcheck"]

ENTRYPOINT ["/app"]
