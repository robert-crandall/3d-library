#!/usr/bin/env bash
#
# Proves the container image actually behaves the way M4 says it does, against a
# throwaway Postgres on a private docker network. Every check below maps to an
# acceptance criterion in issue #7; nothing else belongs here.
#
# This is deliberately NOT in CI. D9's design has pull requests stop at
# `docker-build`, and a full container integration run would be a slow job
# duplicating what the Go integration job already proves about the app. The cost
# of that choice is that this
# script can rot without anyone noticing - so run it when you touch the
# Dockerfile, the healthcheck, or the upload wiring, and paste the output.
#
# Usage: make docker-smoke   (or scripts/docker-smoke.sh)
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE="${IMAGE:-3d-library:smoke}"
PORT="${PORT:-18080}"

NET="ghtsmoke-net-$$"
PG="ghtsmoke-pg-$$"
APP="ghtsmoke-app-$$"
NOUP="ghtsmoke-noup-$$"
RO="ghtsmoke-ro-$$"

# Two directories: one bind-mounted into the container, one for host-side
# scratch. Both are fresh per run, which is what lets check 2's "this directory
# is now non-empty" and check 3's byte comparison mean something.
UPLOADS="$(mktemp -d)"
WORK="$(mktemp -d)"

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok() { printf '    \033[32mok\033[0m %s\n' "$*"; }
die() {
	printf '    \033[31mFAIL\033[0m %s\n' "$*" >&2
	exit 1
}

cleanup() {
	docker rm -f "$APP" "$NOUP" "$RO" "$PG" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
	chmod -R u+w "$UPLOADS" 2>/dev/null || true
	rm -rf "$UPLOADS" "$WORK"
}
trap cleanup EXIT

# The app runs as uid 65532 and this directory is owned by whoever ran the
# script, so it has to be world-writable for the container to use it. On a real
# host you chown to 65532 instead - see the README.
chmod 0777 "$UPLOADS"

jar="$WORK/cookies.txt"
photo="$WORK/photo.png"
got="$WORK/downloaded.png"

# APP_ENV=development is not incidental: production sets Secure cookies, no
# login survives plain HTTP, and every upload check below would then fail for a
# reason that has nothing to do with what it's testing.
app_env=(
	-e "DATABASE_URL=postgres://app:app@$PG:5432/app?sslmode=disable"
	-e APP_ENV=development
	-e ADDR=:8080
)

# A short interval so the unhealthy check at the end takes seconds rather than a
# minute and a half. This overrides the Dockerfile's HEALTHCHECK timings, not
# its command.
health_flags=(--health-interval=2s --health-retries=2 --health-timeout=3s --health-start-period=2s)

wait_for_health() {
	local name="$1" want="$2" status=unknown attempts=60
	while [ $((attempts--)) -gt 0 ]; do
		status="$(docker inspect --format '{{.State.Health.Status}}' "$name" 2>/dev/null || echo gone)"
		[ "$status" = "$want" ] && return 0
		sleep 1
	done
	# Best-effort: under `set -euo pipefail` a failing `docker logs` (the
	# container is gone, which is exactly the `status=gone` case above) would
	# abort the script here and swallow the die message below - the one piece of
	# output that says which check failed and why.
	docker logs "$name" 2>&1 | tail -20 >&2 || true
	die "$name never reached '$want' (last status: $status)"
}

# True when the upload directory has at least one file in it.
#
# Deliberately not a byte comparison against the host file: the foundation
# writes blobs mode 0600 as uid 65532, so on a Linux bind mount the user running
# this script cannot read them at all and `cmp` would fail on correct behavior.
# (On Docker Desktop and OrbStack the VM remaps ownership to the invoking user
# and it would pass, which is worse - it means the failure only shows up on the
# homelab this template is for.) Listing works either way because the directory
# itself is 0777.
#
# Byte identity is proven by check 3 instead, and proven harder: it reads the
# file back through a brand new container, which can only work if the bytes are
# on the mount rather than in the old container's layer.
host_dir_has_a_blob() {
	local f
	for f in "$UPLOADS"/*; do
		[ -e "$f" ] && return 0
	done
	return 1
}

step "Building $IMAGE for the native architecture"
docker build -q -t "$IMAGE" . >/dev/null
ok "built"

step "Starting a throwaway Postgres"
docker network create "$NET" >/dev/null
docker run -d --name "$PG" --network "$NET" \
	-e POSTGRES_USER=app -e POSTGRES_PASSWORD=app -e POSTGRES_DB=app \
	--health-cmd='pg_isready -U app' --health-interval=1s --health-retries=30 \
	postgres:17-alpine >/dev/null
wait_for_health "$PG" healthy
ok "postgres is up"

# --- 1 -----------------------------------------------------------------------
step "1/7  The container boots and Docker reports it healthy"
docker run -d --name "$APP" --network "$NET" "${app_env[@]}" "${health_flags[@]}" \
	-e UPLOAD_DIR=/data/uploads -v "$UPLOADS:/data/uploads" \
	-p "127.0.0.1:$PORT:8080" "$IMAGE" >/dev/null
wait_for_health "$APP" healthy
ok "healthy"

base="http://127.0.0.1:$PORT"

# --- 2 -----------------------------------------------------------------------
step "2/7  An uploaded photo lands in the host directory"
code="$(curl -sS -o /dev/null -w '%{http_code}' -c "$jar" -X POST "$base/api/auth/register" \
	-H 'Content-Type: application/json' \
	-d "{\"email\":\"smoke-$$@example.com\",\"password\":\"smoke-password\"}")"
[ "$code" = "200" ] || die "register returned $code, want 200"

# A real 1x1 PNG with random bytes appended after IEND: still decodable, so the
# thumbnailer has a genuine image to work with, but unique per run, so check 3's
# byte comparison can't be satisfied by anything but this upload.
base64 -d >"$photo" <<'PNG' || die "could not write the test photo"
iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==
PNG
head -c 64 /dev/urandom >>"$photo"

body="$(curl -sS -b "$jar" -X POST "$base/api/files" -F "file=@$photo;type=image/png")"
file_id="$(printf '%s' "$body" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')"
[ -n "$file_id" ] || die "upload response had no id: $body"

# The directory is a fresh mktemp -d, so anything in it got there through the
# bind mount during this run.
host_dir_has_a_blob ||
	die "$UPLOADS is empty after an upload - the blob went into the container layer"
ok "the upload wrote through to the host (file id $file_id)"

# --- 3 -----------------------------------------------------------------------
step "3/7  The photo survives replacing the container"
docker rm -f "$APP" >/dev/null
docker run -d --name "$APP" --network "$NET" "${app_env[@]}" "${health_flags[@]}" \
	-e UPLOAD_DIR=/data/uploads -v "$UPLOADS:/data/uploads" \
	-p "127.0.0.1:$PORT:8080" "$IMAGE" >/dev/null
wait_for_health "$APP" healthy

curl -sS -b "$jar" -o "$got" "$base/api/files/$file_id"
cmp -s "$photo" "$got" || die "the downloaded bytes differ - the blob only lived in the old container"
ok "same bytes back from a brand new container"

# --- 4 -----------------------------------------------------------------------
step "4/7  The healthcheck binary fails when the app isn't serving"
# Nothing is listening on port 9, so the probe must exit nonzero. Stopping
# Postgres instead would only prove "degraded while still serving".
if docker exec -e ADDR=:9 "$APP" /app healthcheck >/dev/null 2>&1; then
	die "healthcheck exited 0 with nothing listening on the probed port"
fi
ok "exits nonzero"

# --- 5 -----------------------------------------------------------------------
step "5/7  An unwritable UPLOAD_DIR refuses to start"
# This runs while Postgres is still up on purpose: main.go migrates and connects
# *before* files.NewService, so with the database down this container would die
# on `migrate:` and prove nothing about the upload directory.
#
# Detached rather than attached, because the regression this catches is "it
# started anyway" - and an attached `docker run` against a container that starts
# successfully never returns.
docker run -d --name "$RO" --network "$NET" "${app_env[@]}" \
	-e UPLOAD_DIR=/data/uploads -v "$UPLOADS:/data/uploads:ro" "$IMAGE" >/dev/null

ro_rc=""
attempts=30
while [ $((attempts--)) -gt 0 ]; do
	read -r running ro_rc < <(docker inspect --format '{{.State.Running}} {{.State.ExitCode}}' "$RO")
	[ "$running" = "false" ] && break
	ro_rc=""
	sleep 1
done
[ -n "$ro_rc" ] || die "the container was still running after 30s with a read-only upload directory"
[ "$ro_rc" != "0" ] || die "the container exited 0 with a read-only upload directory"

out="$(docker logs "$RO" 2>&1)"
printf '%s' "$out" | grep -q 'is not writable' ||
	die "exited $ro_rc but not for the upload directory: $out"
docker rm -f "$RO" >/dev/null
ok "refused to start: $(printf '%s' "$out" | tail -1)"

# --- 6 -----------------------------------------------------------------------
step "6/7  With UPLOAD_DIR unset it boots and serves no file routes"
noup_port=$((PORT + 1))
docker run -d --name "$NOUP" --network "$NET" "${app_env[@]}" "${health_flags[@]}" \
	-p "127.0.0.1:$noup_port:8080" "$IMAGE" >/dev/null
wait_for_health "$NOUP" healthy

# Both the status and the content type, because a missing /api route and the SPA
# fallback both answer 404 - the fallback's is text/html. The content type is
# what distinguishes "the route isn't registered" from "the SPA answered".
# Unauthenticated on purpose: if the route WERE still registered, the
# foundation's upload middleware returns 401 before it ever touches the nil
# service, so this can't pass for the wrong reason.
read -r code ctype < <(curl -sS -o /dev/null -w '%{http_code} %{content_type}\n' \
	-X POST "http://127.0.0.1:$noup_port/api/files")
[ "$code" = "404" ] || die "POST /api/files returned $code, want 404"
case "$ctype" in
application/problem+json*) ;;
*) die "POST /api/files answered 404 as '$ctype' - that's the SPA fallback, not a missing route" ;;
esac
ok "healthy, and POST /api/files is 404 application/problem+json"
docker rm -f "$NOUP" >/dev/null

# --- 7 -----------------------------------------------------------------------
step "7/7  Stopping Postgres turns the container unhealthy"
# Last, because it tears the environment down. This is the check that proves
# Docker's HEALTHCHECK wiring is real, rather than just that the binary can exit
# nonzero when asked.
docker stop "$PG" >/dev/null
wait_for_health "$APP" unhealthy
ok "Docker reports unhealthy"

printf '\n\033[32mAll checks passed.\033[0m\n'
