# 3D Library

A private library for 3D-printable models: upload the STLs and the slicer
project that produced them, keep them together, and find them again later. See
[`docs/brief.md`](docs/brief.md) for what it is for.

It is a SvelteKit frontend embedded in a single Go binary, with Postgres behind
it. The auth, session, and web-push plumbing lives in
[`go-home-server`][foundation], which this repo imports as a Go module rather
than vendoring. The library itself - models, files, and the upload path - is in
`internal/library`.

[foundation]: https://github.com/robert-crandall/go-home-server

See [`docs/tech-stack.md`](docs/tech-stack.md) for why each piece was chosen,
and [`AGENTS.md`](AGENTS.md) for the working commands, conventions, and gotchas
(coding agents read that one).

## Prerequisites

- Go 1.26+
- Bun 1.3+ - the frontend toolchain. Node is not required.
- Postgres 14+ - this repo doesn't run one for you. Point `DATABASE_URL` at
  whatever you already have.

## Getting started

```sh
git clone https://github.com/robert-crandall/3d-library
cd 3d-library
make setup    # deps, ./uploads, .env, and a first frontend build
```

Edit `.env` (at minimum `DATABASE_URL`), create that database, then:

```sh
make build && make run     # http://localhost:8080
```

## Developing

```sh
make dev
```

Vite serves the frontend on `:5173` with hot module reloading and proxies `/api`
and `/healthz` to the Go binary on `:8080`. Cookies key on host rather than
port, so a session set through the proxy comes back.

Editing Go means restarting `make dev`. There's no file watcher for that on
purpose - it's one keystroke, and a watcher is a whole extra thing to debug when
the dev loop misbehaves.

Other targets: `make test` (Go tests), `make check` (frontend type check),
`cd web && bun run test` (component tests), `make spec` (regenerate the API contract),
`make install-mcp` (install the MCP server), `make mcp-token` (mint its API
token), `make mcp-config` (print client configuration), `make clean`, and
`make help`.

## MCP server

The template includes a zero-tool MCP server. It is deliberately empty, but it
already loads an API token, verifies it against the running app, and can speak
MCP over stdio. Start the app and create an account first; on a fresh database,
open `/login` and register the first user. Then set up MCP in three steps:

```sh
make install-mcp
# installed /Users/you/bin/my-app-mcp

APP_EMAIL=you@example.com make mcp-token
# prompts for your password, then writes ~/.config/my-app.json

make mcp-config
# prints the JSON block to paste into your MCP client's config
```

Here and below, `my-app` stands for `<app>`: the last element of the Go module
path, with a `/vN` suffix dropped. The unrenamed template uses
`3d-library`. `make install-mcp` writes `~/bin/<app>-mcp`, and
`make mcp-token` writes the matching app connection config. If `~/bin` is not
on `PATH`, use the absolute path or add it for the current shell:

```sh
export PATH="$HOME/bin:$PATH"
```

Put that export in your shell profile if you want it on future shells too.

The MCP server uses a personal API token, not the browser's session cookie.
`make mcp-token` logs in through the public HTTP API, creates a token named
`<app>-mcp`, and writes the app URL and token to the config with mode `0600`.
It reads the password from `APP_PASSWORD` when set; otherwise it prompts on the
terminal with echo disabled. For an app at another origin, set `MCP_APP_URL`:

```sh
MCP_APP_URL=https://my-app.example.com \
  APP_EMAIL=you@example.com \
  make mcp-token
```

`appUrl` is optional and defaults to `http://localhost:8080`. The real
`MCP_APP_URL` and `MCP_APP_TOKEN` environment variables take precedence over
this file; the file takes precedence over a local `.env`. The config path is
`$XDG_CONFIG_HOME/<app>.json` when `XDG_CONFIG_HOME` is set, and
`~/.config/<app>.json` otherwise.

The main module's basename controls the MCP handshake name, installed binary, and
config filename together. If you ever rename the module, move the config to the
new `<app>.json`, rerun `make install-mcp`, and remove the old binary.

With the app running and the token valid, the shell mode proves the harness is
live:

```sh
my-app-mcp list
# (no tools registered)
```

No arguments (or `serve`) starts the stdio MCP transport for a desktop client.
`make mcp-config` prints the `mcpServers` block with the binary's absolute path
because `~` is not expanded when a client executes a command. It does not write
the client config: Claude Desktop, Copilot CLI, Cursor, and other clients keep
that file in different places, and it may already contain other servers.
`list` and `call` verify the token with `GET /api/auth/me`, so a missing,
garbage, or revoked token fails clearly.
Stdio startup loads the config but waits for a tool call before contacting the
app. Missing config is static and cannot repair itself, so it still stops
startup. App availability and token validity are transient, and each tool's own
HTTP call reports either failure without extra lazy-validation machinery.
Desktop clients often start before the app and may not retry a process that
exits, so this keeps a temporary outage from disabling the integration until
the client restarts.

### Adding a tool

Keep MCP handlers thin: call the app's HTTP API so auth, validation, and domain
logic stay in one place. For example, add the foundation `auth` package to
`cmd/mcp/main.go`'s imports, then replace the empty `registerTools` function
with:

```go
func registerTools(srv *foundationmcp.Server, client *apiclient.Client) {
	foundationmcp.AddTool(
		srv,
		"current_user",
		"Get the authenticated app user.",
		func(ctx context.Context, _ struct{}) (auth.User, error) {
			var user auth.User
			err := client.Do(ctx, http.MethodGet, currentUserPath, nil, &user)
			return user, err
		},
	)
}
```

The input and output are structs, so the harness can infer their JSON schemas.
For an app-specific tool, add the API route in `internal/app` first, regenerate
the contract with `make spec`, then call that route from the tool.

## Auth and the screens

The template ships the smallest thing that proves auth works end to end:
`/login` (log in, plus register while registration is open) and a guarded `/`
that greets you, names the account you're signed in as, and offers **Log out**.
There is no navigation, no theme, and no chrome of any kind - the SPA has no
opinion about what your app looks like. That's it; the rest is yours.

Three pieces make it work, and they're all small enough to read in a sitting:

- **`web/src/lib/api/client.ts`** - `createClient<paths>()` and nothing else. No
  base URL, no headers, no interceptors. The session is an HttpOnly cookie and
  `fetch` sends those on same-origin requests, so there's nothing for the client
  to attach. The integration tests exercise this cookie boundary.
- **`web/src/lib/auth.svelte.ts`** - a `$state` user and a cached boot promise.
  `ensure()` resolves once, from `GET /api/auth/me`; `signedIn()` and
  `signOut()` update it directly so a login doesn't cost a second round trip.
- **The guard lives in a `load`, not a component** - `web/src/routes/(app)/+layout.ts`,
  which covers every page in the group. SvelteKit doesn't render a page until
  its `load` resolves, so a signed-out visitor gets a redirect instead of a
  flash of the greeting. `/login` guards the other way, in its own `+page.ts`,
  and bounces anyone already signed in.

Errors are the server's words: `apiErrorMessage` reads huma's `errors[]` when
they're there and its `detail` otherwise, so there's no status-code-to-message
table in the frontend - that would be a second copy of the API's error copy,
drifting quietly. Only a failure to reach the server at all gets local wording,
because in that case there is no server response to render.

Registration is gated by `ALLOW_OPEN_REGISTRATION`. It defaults to `false`,
which means "the first account only" - fine for a single-user app, which is what
this template is shaped for. Set it to `true` if you want anyone to sign up.

`/login` reads that state rather than guessing at it. `GET /api/app` returns a
`registrationOpen` boolean (alongside `googleLoginEnabled`) from
`auth.Service.RegistrationOpen`, which runs the same predicate the register
handler runs, and the page shows the Log in /
Register pair only when the answer is yes - opening on Register, since that's
the thing to do at that moment. By default that means the pair is gone once a
non-deleted account exists, and `/login` is just a login form, instead of a
Register tab that can only ever answer "registration is closed". With
`ALLOW_OPEN_REGISTRATION=true` the answer is always yes, so the pair stays for
good.

The state is advisory. Under the default gate the register handler re-checks
inside its transaction, holding a database advisory lock, so a page that loaded
while registration was open can still lose the race - and it renders the
server's refusal rather than pretending that can't happen.

### Sign in with Google

Off unless you set `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` and
`GOOGLE_REDIRECT_URL`. Create an OAuth client in the Google Cloud console and
point the redirect URL at `https://your-app/api/auth/google/callback` in both
places - they have to match byte for byte.

The flow is entirely server-side, so the SPA side of it is a link, not a script
tag: `/api/auth/google/start` redirects to Google's consent screen, and the
callback sets the same session cookie a password login sets. Nothing downstream
of the cookie can tell the two apart.

One account, two doors: a Google sign-in matches an existing user by **verified
email**, case-insensitively, so the row `POST /api/auth/register` created is the
row Google finds. There is no linking step and no `google_sub` column, which is
also the assumption to be aware of - it takes for granted that the address is a
Gmail one or on a domain you control, so it can't be handed to someone else
later.

Who gets in follows `ALLOW_OPEN_REGISTRATION`, with one narrowing: **Google
never creates the first account.** With registration closed (the default) a
stranger's Google sign-in bounces to `/login?error=registration_closed`, and so
does the very first one on an app with no accounts at all - that door stays
password-only so a broken OAuth client can't lock the owner out. With
`ALLOW_OPEN_REGISTRATION=true` a Google sign-in creates the account, and that
account has no usable password until the app grows a set-password flow.

`/login` shows the button only when the server says it's configured -
`GET /api/app` returns `googleLoginEnabled` alongside `registrationOpen` -
because an unmounted `/api/auth/google/start` answers with the same JSON 404 as
any other unknown API path, which is not a thing to hand a browser. Failures
come back as `?error=<code>` from a fixed vocabulary rather than as JSON, and
the login page owns that wording; it's the one exception to "errors are the
server's words", because a code isn't a sentence.

## Adding a page

Every signed-in page lives under `web/src/routes/(app)/`, which is the route
group the auth guard wraps. Adding one is one thing: a `+page.svelte` under that
directory - say `notes/+page.svelte`, which serves `/notes`. There is no
navigation array to register it in, because there is no navigation.

`(app)/+layout.ts` is a guard with no `+layout.svelte` beside it, so pages in the
group inherit a session and nothing else. Nothing wraps your page, nothing sets
its background, and nothing adds a header. When you know what your app's chrome
looks like, write a `+layout.svelte` there and everything in the group gets it.

`(app)/+page.svelte` is the example, and it's meant to be deleted or replaced. It
renders **Log out** because sign-out has to live somewhere and a layout would be
this template deciding you have a sidebar; move `SignOutButton` into your own
layout once you have one.

## Styling

Tailwind 4 is wired up (`@tailwindcss/vite`, imported from `web/src/app.css`) and
that is the whole styling layer. No component library, no theme system, no
tokens, no `tailwind.config.js` - because a look is the one thing every app
built on this replaces first, and something you have to delete is worse than
nothing.

One thing to know before your first page: Tailwind's preflight is on, and it
resets every border to zero width and every form control to a transparent
background. An `<input>` with no classes is an invisible box. That's why
`/login` and the **Log out** button carry a few structural classes - a border,
some padding, a rounded corner, a max width. Two classes aren't structural:
`text-2xl font-bold` on the home page's heading, and `font-bold` on `/login`'s
selected mode button, which is the sighted-user half of its `aria-pressed`.
Nothing names a colour, a font family, or a breakpoint.

If you want daisyUI (which is what this template shipped through M7), install it
and add the plugin block:

```sh
cd web && bun add -d daisyui
```

```css
/* web/src/app.css */
@import 'tailwindcss';
@plugin "daisyui" {
  themes: light --default, dark --prefersdark;
}
```

D5 in [`docs/tech-stack.md`](docs/tech-stack.md) has the two things worth
knowing before you do - where an override has to sit in the cascade to beat it,
and the contrast floor for muted text.

## Install metadata

`web/static/` carries `manifest.webmanifest` and two PNG icons, so a home-screen
shortcut gets a real icon. Regenerate the PNGs from `icon.svg` if you change the
artwork - the `rsvg-convert` command is in a comment at the top of that file.

The manifest deliberately declares no `theme_color` or `background_color`. Both
are optional, and both are this template picking your installed app's title bar
and splash screen, which is the same opinion the CSS stopped having. Add them
back once you know your palette.

There's deliberately no service worker, so there's no offline support and no
service-worker-managed asset cache (ordinary HTTP caching of the hashed assets
still applies). The app is still installable from the browser menu - Chrome
dropped the fetch-handler requirement for that - but nothing will offer to
install it unprompted, because that heuristic does still want a service worker.
See D6 in [`docs/tech-stack.md`](docs/tech-stack.md) for why the service worker
is the piece left out.

Picking up a deploy needs no code. `index.html` is served `no-cache` and every
script filename contains a content hash, so a cold launch fetches fresh HTML that
points at the new build. `TestSPACacheHeaders` in `internal/app/cache_test.go`
pins that, since it's the only thing holding the behaviour up.

"Cold" is the load-bearing word. A client that's already running won't notice a
deploy at all: a tab left open, or an installed PWA the system restores rather
than relaunches, keeps the old build until something forces a real document load.
Following an in-app link doesn't count - that's a client-side navigation. D6 in [`docs/tech-stack.md`](docs/tech-stack.md)
covers what to add if that isn't good enough for your app, and why it isn't here.

## Testing

```sh
make test                  # Go tests
cd web && bun run test     # Component tests
```

The frontend suite renders components with fixture data and no server, database,
or browser. The Go suite covers the HTTP and database boundary, and
`web/dist_test.go` checks the embedded build output.

The database-backed Go test needs a database, which this repo doesn't create for
you:

```sh
createdb 3d-library_test    # the Go API test
```

The Go test reads `TEST_DATABASE_URL` and skips entirely when it's unset.

One case can't be reached from a browser: registering a duplicate email. The
default gate is checked *before* the duplicate check, so a second registration
is always `403 registration is closed`, never `409 email already registered`.
Standing up a second server and database to reach it would be more machinery
than one assertion is worth, so `internal/app/api_test.go` pins all three
refusal strings against a real database instead:

```sh
TEST_DATABASE_URL=postgres://localhost:5432/3d-library_test?sslmode=disable \
  go test ./internal/app/ -run TestAuthRefusalStrings
```

`403 registration is closed` moved half a step in the same direction. Now that
`/login` hides the Register control once an account exists, the component suite
asserts that closed-registration state directly.

### Git worktrees

`.env` is gitignored, so a new worktree starts without one and `make setup`
would recreate it from `.env.example` - handing you default config instead of
the config you were actually running. `.worktreeinclude` lists the ignored files
a new worktree should inherit from the main checkout, and `.env` is on it. Add
anything else your app keeps outside git and expects to be there.

Nothing in this repo reads `.worktreeinclude` - it's a manifest for external
worktree tooling that looks for one. Plain `git worktree add` ignores it, so if
your setup doesn't use such tooling, copy `.env` across by hand and the file
costs you nothing.

## Deploying

The app ships as one container image: a distroless runtime with a single static
binary in it, the SPA already embedded. No shell, no package manager, nothing to
`docker exec` into.

```sh
docker build -t myapp .
```

`Dockerfile` builds the SPA and the binary in their own stages, so the image is
reproducible from a clean checkout - you don't need to have run `make build`
first. The published image is `linux/amd64` only, which is what a plain
`docker build` gives you on an amd64 host anyway. On an arm64 machine (an Apple
Silicon Mac, say) ask for the target explicitly:

```sh
# Both build stages are pinned to $BUILDPLATFORM and Go cross-compiles, so this
# is a native build with no QEMU in it.
docker buildx build --platform linux/amd64 --load -t myapp .

# The pullable form, once you have somewhere to push to.
docker buildx build --platform linux/amd64 -t ghcr.io/you/myapp:latest --push .
```

### Published images

You don't have to run either of those by hand. Merging to `main` publishes the
image to GHCR, and `.github/workflows/publish.yml` does it:

| Tag | When | Moves? |
| --- | --- | --- |
| `ghcr.io/<owner>/<repo>:main` | every green merge to `main` | yes - this is the one to deploy |
| `ghcr.io/<owner>/<repo>:sha-<full commit sha>` | every green merge to `main` | never |
| `ghcr.io/<owner>/<repo>:v1.2.3` | pushing a `v*` git tag | never |

The image path comes from `${{ github.repository }}`, lowercased, so a repo made
from this template publishes under its own name with nothing to edit. Deploy
`:main` and pin an incident to a `:sha-...` when you need to know exactly what
was running.

Pushing a `v*` tag publishes `:v1.2.3` only. It deliberately does not move
`:main`, so tagging a release doesn't redeploy anything on its own.

### Pulling it from a machine that isn't logged in

**GHCR packages start private, and the denial looks like a typo.** A pull
without credentials reports `denied` or a 404 - not "log in" - so the first
thing to check when a fresh host can't find an image that plainly exists is
whether the host is authenticated at all. Pick one:

```sh
# Option A: make the package public. Repo -> Packages -> the package ->
# Package settings -> Change visibility. Nothing to configure on the host.

# Option B: keep it private and log the host in with a PAT that has read:packages.
echo "$GHCR_PAT" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

If you keep it private, **Watchtower needs those credentials too** - it pulls on
its own schedule, not through your shell. Mount the config you just created:

```yaml
volumes:
  - /root/.docker/config.json:/config.json:ro
```

(That path is the one root's `docker login` writes. Use whichever home directory
the daemon user actually has.)

### Automatic updates

The host pulls new images itself; GitHub Actions holds no key to your homelab.
Add Watchtower to the same compose stack:

```yaml
  watchtower:
    image: containrrr/watchtower
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      # Only if your GHCR package is private - see above.
      # - /root/.docker/config.json:/config.json:ro
    command: --cleanup --interval 300
    # Uncomment to accept the publish workflow's webhook as well as polling.
    # ports:
    #   - '8080:8080'
    # environment:
    #   WATCHTOWER_HTTP_API_UPDATE: 'true'
    #   WATCHTOWER_HTTP_API_TOKEN: your-token-here
```

Polling every five minutes is enough on its own. To make a merge land in
seconds instead, uncomment the HTTP API lines above and set two **optional**
repository secrets - `WATCHTOWER_WEBHOOK_URL` (the `/v1/update` endpoint of
whatever you exposed, e.g. `https://watchtower.example.com/v1/update`) and
`WATCHTOWER_TOKEN` (the same value as `WATCHTOWER_HTTP_API_TOKEN`). The publish
workflow pings the URL after it moves `:main`. Don't expose that port to the
internet without something in front of it: a valid token there triggers a pull
and restart.

Both are optional in the real sense: with neither set, the workflow logs that it
had nothing to notify and stays green. Same for `SLACK_WEBHOOK_URL`, which the
notification workflow uses to report a broken `main`. A fork of this template
with no secrets configured works; it just doesn't tell anyone anything.

> **Leave `WATCHTOWER_WEBHOOK_URL` unset until you've seen your first `:main`
> promotion.** A fresh fork has no secrets, so that first run exercises the
> unset-secret path against the live workflows - the only free, unmocked evidence
> that a fork of this template isn't broken by the secrets its author never
> configured. Two guards, and they're proven at different moments:
>
> - The **Watchtower** guard is proven the first time `promote` actually moves
>   `:main`. Anchor on the log line, not on a green run: a green Publish can also
>   mean the guard declined or `promote` no-opped because `main` moved, and in
>   both of those the ping step never executes. What you're looking for is
>   `WATCHTOWER_WEBHOOK_URL is unset - nothing to notify` in `promote`.
> - The **Slack** guard isn't proven by any healthy merge, first or otherwise. It
>   sits inside the `Post to Slack` step, which only runs when there's something
>   to report. If you want that evidence too, leave `SLACK_WEBHOOK_URL` unset
>   until the first time `main` genuinely breaks - which is a different and much
>   later moment than the Watchtower one, so don't hold up your setup for it.
>
> `WATCHTOWER_TOKEN` is exempt - it's never read unless the URL is set, so
> configuring it early costs nothing.

### Configuration

| Variable | Required | What it does |
| --- | --- | --- |
| `DATABASE_URL` | **yes** | Postgres connection string. No default. |
| `ADDR` | no | Listen address, default `:8080`. |
| `APP_ENV` | no | `production` sets `Secure` on the session cookie. |
| `UPLOAD_DIR` | **yes** | Where uploaded files are written. See below. |
| `ALLOW_OPEN_REGISTRATION` | no | Default false: the first user registers, then registration closes. |
| `VAPID_PUBLIC_KEY` / `VAPID_PRIVATE_KEY` / `VAPID_SUBJECT` | no | Web push, off unless set. All three together or none: one key alone is a startup error, and so is a missing or malformed subject (it must be a `mailto:` or `https:` URL). |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` / `GOOGLE_REDIRECT_URL` | no | Sign in with Google, off unless set. All three together or none - set one and the app won't start. See "Sign in with Google" above. |

`APP_ENV=production` means **nobody can log in over plain HTTP** - a `Secure`
cookie is dropped by the browser. Put TLS termination in front of it, or leave
`APP_ENV` unset behind a trusted network.

### The upload directory

`UPLOAD_DIR` is required. The template it came from treated uploads as optional;
here they are the entire point, so there is nothing to run without one.

Create the directory **before the first run** and give it to uid `65532`, which
is who the distroless `nonroot` image runs as:

```sh
sudo mkdir -p /srv/3d-library/uploads
sudo chown 65532:65532 /srv/3d-library/uploads
```

Both halves matter. Docker creates a missing bind-mount source itself, owned by
root, and the app then can't write to it. And the app *checks*: at startup it
stats `UPLOAD_DIR` and write-probes it, and refuses to boot if either fails. That
crash is the feature. The alternative is uploads quietly landing in the
container's own filesystem, where the next `docker pull` throws them away.

### Compose

This repo ships no compose file on purpose - your homelab already has one, and a
second half-configured stack is worse than none. Here's the part to paste in:

```yaml
services:
  app:
    image: ghcr.io/you/myapp:main
    restart: unless-stopped
    ports:
      - '8080:8080'
    environment:
      DATABASE_URL: postgres://app:app@db:5432/app?sslmode=disable
      APP_ENV: production
      # Uploads: delete this line AND the volumes block below if you have none.
      UPLOAD_DIR: /data/uploads
    volumes:
      - /srv/3d-library/uploads:/data/uploads
    depends_on:
      db:
        condition: service_healthy

  db:
    image: postgres:18
    restart: unless-stopped
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: app
      POSTGRES_DB: app
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ['CMD-SHELL', 'pg_isready -U app']
      interval: 10s
      retries: 5

volumes:
  pgdata:
```

`condition: service_healthy` is not politeness. The app runs migrations at
startup and has **no connection retry** - if Postgres isn't accepting connections
yet, it exits. `restart: unless-stopped` is the other half of that answer.

### Health

The image has a `HEALTHCHECK`, so Docker tracks it for you - ask the *container*,
not the image:

```sh
docker inspect --format '{{.State.Health.Status}}' "$(docker compose ps -q app)"
```

Under the hood the binary probes itself - `/app healthcheck` GETs `/healthz` on
`ADDR`'s port and exits nonzero on anything but a 200. Distroless has no shell
and no curl, so the usual `HEALTHCHECK CMD curl ...` can't work here. `/healthz`
pings the database, so a container that's up but has lost Postgres reports
unhealthy rather than healthy-and-broken. You can also just ask it:

```sh
curl -i http://localhost:8080/healthz     # 200 {"status":"ok"} / 503 {"status":"degraded"}
```

### Backups

There are **two** things to back up and they have to be restored together.
Restoring Postgres without its matching `UPLOAD_DIR` leaves rows pointing at
files that don't exist; the reverse leaves orphaned blobs nothing references.

### Verifying the image

`make docker-smoke` builds the image and runs every one of these claims against a
throwaway Postgres: it boots healthy, an upload lands on the host and survives
replacing the container, an unwritable or missing `UPLOAD_DIR` refuses to start,
and killing Postgres turns the container unhealthy. It
needs Docker and takes a couple of minutes. It's not in CI - CI stops at building
the image.

## The build order

`web/dist.go` embeds the built frontend:

```go
//go:embed all:build
var buildFS embed.FS
```

Two things follow from that, and both bite eventually:

- **The frontend has to be built before the Go compile.** On a clean checkout
  `go build ./...` fails with `pattern all:build: no matching files found`
  until `web/build` exists. `make build`, `make dev`, and CI all order
  themselves around this.
- **`all:` is load-bearing.** Without it, `go:embed` silently skips files and
  directories starting with `_`, which is exactly where SvelteKit puts every
  hashed chunk (`_app/immutable/...`). You get a binary that boots, serves the
  HTML shell, and 404s every script. `web/dist_test.go` checks for those chunks
  so the failure is a red test rather than a confusing afternoon.

## The API contract

Routes are registered in one place, `internal/app/routes.go`:

```go
func RegisterRoutes(api huma.API, deps Deps) error { ... }
```

`cmd/server` and `cmd/openapi` are both thin callers of it, which is what keeps
the committed contract honest. Two files are generated and committed:

| File | What it is |
| --- | --- |
| `docs/openapi.json` | the OpenAPI document, straight from the registered routes |
| `web/src/lib/api/schema.d.ts` | TypeScript types generated from that document |

The loop when you add or change a route:

```sh
make spec      # regenerate both, then commit them
```

CI's `spec` job runs the same command and fails if it produces a diff, so a
route change that skips the regeneration is a red build rather than a frontend
that type-checks against a contract the server stopped honoring.

Two things about that job are deliberate. It has **no `DATABASE_URL`** and it
does **not** wait for the frontend build: `cmd/openapi` imports `internal/app`
and never `web`, and `RegisterRoutes` only describes routes, so the generator
runs against nil database pools. If a route handler ever gets called during
registration, it panics on that nil pool - a loud failure in the one place
that's cheap to debug. `internal/app/spec_test.go` covers the same ground
locally, and additionally asserts that two consecutive generations are
byte-identical, because a drift check is only fair if generation is
deterministic.

`openapi-typescript` is pinned to an exact version (no caret) for the same
reason: a patch bump that reformats its output would otherwise show up as
mystery drift on an unrelated pull request.

## Adding your first migration

The foundation owns the `users`, `sessions`, `api_tokens`, `push_subscriptions`,
and `files` tables and migrates them itself. This repo ships no `migrations/`
directory, because an empty one is a thing to explain and a thing to get wrong.
Registering a second migration source at all makes goose create a version table
for it, so an app with no migrations of its own would get an extra table it
never asked for.

It's three small steps rather than uncommenting a line. Create
`migrations/migrations.go`:

```go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

const Dir = "."
```

Write `migrations/00001_whatever.sql` as a goose migration. Then add a second
source in `cmd/server/main.go`, next to the foundation's. It needs an import
alias, because `cmd/server` already imports the foundation's package under the
name `migrations`:

```go
import appmigrations "github.com/robert-crandall/3d-library/migrations"

db.MigrationSource{FS: appmigrations.FS, Dir: appmigrations.Dir},
```

Each source tracks its own goose version table, so your numbering starts at
00001 and never collides with the foundation's.
