#!/usr/bin/env bash
#
# The dev loop: Vite on :5173 for hot module reloading, with the real Go binary
# on :8080 behind it. Vite proxies /api and /healthz across, and cookies key on
# host rather than port, so a session set through the proxy comes back.
#
# There is no Go hot reload here on purpose. Editing Go means restarting this
# script; adding a file watcher for that is machinery this loop doesn't need.
set -euo pipefail

cd "$(dirname "$0")/.."

# web/dist.go embeds web/build, so even the dev binary won't compile without it.
# Build it once if it's missing, so `make dev` survives a `make clean`. This
# still needs `make setup` to have installed web/node_modules; bootstrapping
# that here would just be a worse copy of `make setup`.
if [ ! -f web/build/index.html ]; then
  echo "==> web/build is missing; building the frontend once so the binary can embed it"
  (cd web && bun run build)
fi

echo "==> building the API server"
go build -o .bin/dev-server ./cmd/server

echo "==> starting the API server on :8080"
./.bin/dev-server &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true' EXIT

# Wait for the server to actually come up. Without this, a server that died on
# a missing DATABASE_URL or a Postgres that isn't running leaves Vite serving
# happily on 5173 while every proxied call 502s - a dev loop that looks fine and
# isn't. Bounded, so a server that starts but never gets healthy fails here
# rather than hanging.
ready=
for _ in $(seq 1 50); do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "the API server exited during startup - see its output above" >&2
    exit 1
  fi
  # No -S: connection-refused is the expected answer while it's still booting.
  if curl -fs -o /dev/null http://127.0.0.1:8080/healthz; then
    ready=yes
    break
  fi
  sleep 0.2
done

if [ -z "$ready" ]; then
  echo "the API server never became healthy on :8080 after 10s" >&2
  exit 1
fi

echo "==> starting Vite on :5173"
# Not exec: that would replace this shell and the EXIT trap would never fire,
# orphaning the API server every time you ctrl-C out of the dev loop.
cd web && bun run dev
