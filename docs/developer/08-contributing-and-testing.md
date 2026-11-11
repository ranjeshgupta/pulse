# 8. Contributing and testing

How the repository is laid out, how to run each kind of test, how to work on the web app, the
conventions the code follows, and how to keep these docs (and their screenshots) in step with it.

Everything runs from the `pulse/` directory. You need Docker (with Compose) and `make`; Go and
Node.js on the host are optional (see below).

---

## Repository layout

| Path | What |
|---|---|
| `cmd/pulse` | The server binary: one role per process (`pulse api`, `pulse dispatch`, …, `pulse all`) |
| `cmd/seed` | Creates a demo or docs workspace through the public API (`make seed`, `make docs-seed`) |
| `cmd/loadgen` | Load generator (`make load`) |
| `internal/…` | The server's packages; a map is in [7. Architecture and extending](07-architecture-and-extending.md#package-map) |
| `migrations/postgres`, `migrations/clickhouse` | SQL migrations, embedded into the binary |
| `web/` | The React + TypeScript web app (`src/`), the web SDK (`src/sdk/`), static SDK files (`public/sdk/`: test page, service worker, landing page loader), and `embed.go`, which embeds the build (`dist/`) into the Go binary |
| `sdks/flutter/pulse_sdk`, `sdks/android`, `sdks/ios` | Mobile SDKs, each with its own README and tests |
| `test/e2e` | End-to-end tests against a running stack |
| `deploy/` | `docker-compose.yml` for the dev stack, Keycloak realm, `dev-seed.md` (local test accounts) |
| `docs/` | Design notes (`01-…05-…`), `tutorials/`, `developer/` (this guide), `screenshots/` (capture tooling) |
| `Makefile`, `Dockerfile` | Every build and test command; the production image (web build, Go build, Alpine) |
| `.cache/` | Go build and module caches, the screenshot tooling's npm packages and saved sessions (git-ignored) |

---

## Go without Go

The Makefile runs Go in the `golang:1.26-alpine` image when `go` isn't installed, as your user,
with the caches in `.cache/` (so files you create stay yours and later runs are fast). `make it`,
`make e2e`, `make load`, `make seed` and `make docs-seed` always use the image, because they join
Docker networks. With Go installed, `make test`, `make build` and `make vet` use it directly.

**Fresh clone:** `web/embed.go` embeds `web/dist`, which is git-ignored and only created by the web
build. Build the web app once before `make build` or `make test`, or the `web` and `cmd/pulse`
packages don't compile:

```bash
cd web && npm ci && npm run build && cd ..
```

(The Docker image build does this itself, so `make up` works without it.)

---

## Make targets

| Target | Does | Needs |
|---|---|---|
| `make up` / `make down` | Build the image and start the dev stack / stop it (volumes kept) | Docker |
| `make ps`, `make logs` | Service status; follow Pulse's log | the stack |
| `make build`, `make vet`, `make tidy` | `go build ./...`, `go vet ./...`, `go mod tidy` | `web/dist` (above) |
| `make test` | Go unit tests, `go test -count=1 ./...`. No services: stores are faked | `web/dist` |
| `make it` | Integration tests (`-tags integration ./internal/...`) in a container on the Compose network, against the stack's ClickHouse and Redis. The main one is `TestSQLAgreesWithEval`: the two segment compilers must agree | `make up` |
| `make e2e` | End-to-end tests (`-tags e2e ./test/e2e/...`), see below | `make up` |
| `make load ARGS="-rate 10000 -duration 45s"` | Load test from the Compose network. Flags: `-contacts`, `-rate`, `-batch`, `-workers`, `-duration`, `-journey`. Results: [docs/03-load-test.md](../03-load-test.md) | `make up` |
| `make seed ARGS="-contacts 1000"` | A "Demo Retail" workspace with data. Flags: `-workspace`, `-email`, `-password`, `-contacts`, `-days`, `-if-exists` | `make up` |
| `make docs-seed` | The "Acme Retail" workspace (`acme-retail`) the screenshots use, once | `make up` |
| `make docs-shots [SHOT=dev-03,04]` | Retake documentation screenshots, see [below](#screenshots) | `make up` |

Test accounts for the local stack (demo and docs workspaces, Keycloak, the admin token
`dev-admin-token`) are in [deploy/dev-seed.md](../../deploy/dev-seed.md). They are test values:
never reuse them anywhere real.

### Unit tests

`make test` runs every package's `_test.go` files. They use in-memory fakes of the narrow store
interfaces each package declares (`api.Store`, `dispatch.Store`, `journey.Data`, …) and `httptest`
servers for provider and connector APIs, so they run in seconds with no services. Run one package
or test while you work:

```bash
go test ./internal/journey/ -run TestSplit -count=1
# without Go:
docker run --rm -u $(id -u):$(id -g) -v $PWD:/src -w /src -e GOCACHE=/src/.cache/go-build \
  -e GOMODCACHE=/src/.cache/mod -e CGO_ENABLED=0 golang:1.26-alpine go test ./internal/journey/ -run TestSplit -count=1
```

### End-to-end tests and the Compose network rule

`make e2e` drives the running stack through its public API exactly as a customer would: it creates
workspaces (with the admin token, or by signing up), sends events, waits for segments, journeys and
emails (read back from Mailpit), and checks latencies such as event → segment in under 2 s and
event → email in under 5 s. Every test makes its own workspace, so tests don't interfere and can
run against a database with other data in it (the workspaces stay behind in your local database).

It runs in **two passes**:

1. On the **host network** (`PULSE_API=http://localhost:7080`), because the SSO test needs the same
   Keycloak address a browser uses (`localhost:8180`).
2. In a container named `pulse-e2e-hooks` on the **Compose network** (`pulse_default`), with
   `PULSE_API=http://pulse:7070` and `PULSE_HOOK_HOST=pulse-e2e-hooks`, for the tests in which
   **Pulse calls the test**: outbound webhooks, the custom gateway, REST hooks, HubSpot, audience sync
   and the bounce mailbox.

The rule for tests that receive calls from Pulse:

- Start receivers with `newReceiver(t)` (or `newMock`), which listen on the container's own address
  and build URLs from `PULSE_HOOK_HOST`. They **skip** themselves in the host pass, where
  `PULSE_HOOK_HOST` is unset, so nothing ever listens on your machine's network.
- Add the test's name to the `-run` list of the second command in the `e2e` target, or it never
  runs.
- They rely on `PULSE_ALLOW_PRIVATE_WEBHOOKS=true`, which the dev Compose file sets: receivers have
  private addresses.
- Allow for the webhook worker's 10-second cache: the tests sleep 11 s after creating a webhook or
  subscription.

To run a single e2e test against your stack:

```bash
docker run --rm --network host -u $(id -u):$(id -g) -v $PWD:/src -w /src -e GOCACHE=/src/.cache/go-build \
  -e GOMODCACHE=/src/.cache/mod -e CGO_ENABLED=0 -e PULSE_API=http://localhost:7080 \
  -e PULSE_MAILPIT=http://localhost:18025 -e PULSE_ADMIN_TOKEN=dev-admin-token \
  golang:1.26-alpine go test -count=1 -tags e2e -v -run TestForms ./test/e2e/...
```

### SDK tests

| SDK | Command |
|---|---|
| Web SDK | `cd web && npm test` (part of the web app's tests) |
| Flutter | `cd sdks/flutter/pulse_sdk && flutter test` |
| Android core | `cd sdks/android && docker run --rm -u $(id -u):$(id -g) -v "$PWD":/w -w /w -e GRADLE_USER_HOME=/w/.gradle gradle:8-jdk17 gradle :pulse-core:test --no-daemon` |
| iOS core | `cd sdks/ios && docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -v "$PWD":/w -w /w swift:5.10 swift test` |

What each SDK's tests do and don't cover: [5. Mobile SDKs](05-mobile-sdks.md#what-has-been-tested).

---

## Working on the web app

The app is React + TypeScript with Vite, TanStack Query for server state, and TipTap for rich
text. In production Go serves the build from the binary; in development use Vite's dev server,
which proxies `/v1` to the Pulse on port 7080, so the session cookie and CSRF header behave as in
production:

```bash
make up                      # the API, on :7080
cd web
npm install
npm run dev                  # http://localhost:5173, live reload
npm test                     # vitest: the pure-logic modules (*.test.ts)
npm run typecheck            # tsc --noEmit
npm run build                # what the image runs: typecheck, the app, then the SDK (dist/sdk/pulse.js)
```

Only `/v1` is proxied: the SDK test page (`/sdk/…`), hosted forms (`/f/…`) and landing pages
(`/p/…`) are served by the Go binary, so look at them on port 7080 after `make up` rebuilds the image.

Conventions in `web/src`:

- `api.ts` is the only HTTP client; it adds `X-Pulse-CSRF` to every change and turns error bodies
  into messages.
- Logic is kept out of components in plain modules with unit tests next to them:
  `segmentRules.ts`, `segmentCatalog.ts`, `journeyGraph.ts`, `journeyCatalog.ts`,
  `contactFilters.ts`, `importMapping.ts`, `reportBuilder.ts`, …. Test those; components are thin.
- Pages live in `pages/`, one per area; settings pages are listed in `SettingsLayout.tsx` with the
  permission each needs; old addresses redirect (`main.tsx`).
- UI text is plain and specific: say what happens ("Pulse stops sending to it right away"), and
  make error messages say what to do.

---

## Conventions in the Go code

Visible throughout the code base; follow them in new code.

- **Standard library first.** HTTP routing is `net/http`'s `ServeMux` with method patterns, logging
  is `log/slog` JSON. Dependencies are few (pgx, clickhouse-go, go-redis, franz-go, a handful more).
- **Tenancy everywhere.** Every store function takes the tenant ID first; every key starts with it.
  Never trust a tenant ID from a request body.
- **Narrow interfaces where they're used.** Handlers and workers declare the few store methods they
  need (`FormStore`, `WebhookStore`, `journey.Data`) and tests fake them.
- **Idempotent consumers.** Anything a Kafka consumer does may happen twice: derive IDs
  deterministically and make writes upserts.
- **Errors people can act on.** Validation errors say what to change ("wait 1 minute to 365 days
  for it"); several are joined with `errors.Join`. Store errors go through `s.fail`, which never
  leaks details.
- **Security defaults.** User-entered URLs go through `safehttp` (no private addresses unless
  configured); secrets are sealed with `auth.Box` and never returned or logged; the audit log
  redacts them; browsers get less than servers (site key, reserved attributes, server-only
  events); new routes need settings edit until listed in `perms.go`.
- **Comments explain why.** Every package starts with a doc comment saying what it is for; exported
  identifiers say what they guarantee. Read the package comment before changing a package.
- **Names.** Event names and attribute keys are `snake_case`; past tense for things that happened
  (`email_opened`, `segment_entered`).
- **Tests at the right level.** Unit tests for logic; `make it` where Go and ClickHouse must agree;
  `make e2e` for behaviour across roles, with timing assertions where speed is the feature.

Before sending a change: `make vet test`, `cd web && npm run typecheck && npm test`, and with the
stack up, `make it e2e`.

---

## Keeping the docs in sync

The docs are part of the change. When behaviour changes, update in the same commit:

| Changed | Update |
|---|---|
| A `PULSE_*` variable (`internal/config/config.go`) | [2. Configuration](02-configuration.md), the settings table in the [README](../../README.md#settings), and [1. Installation](01-installation.md) if it matters in production |
| A route, request or response (`internal/api`) | [3. API guide](03-api.md): the task section and the [route index](03-api.md#route-index) (list the router with `grep -rn 'mux.HandleFunc' internal/api`), and `perms.go` |
| The web SDK (`web/src/sdk`, `web/public/sdk`) | [4. Web SDK](04-web-sdk.md) |
| A mobile SDK | Its README first, then [5. Mobile SDKs](05-mobile-sdks.md) |
| Webhook payloads, signing, provider webhooks, the gateway | [6. Webhooks and integrations](06-webhooks-and-integrations.md); the signature samples must keep matching `webhooks.Sign` |
| Topics, roles, stores, extension points | [7. Architecture and extending](07-architecture-and-extending.md) and [docs/02-architecture.md](../02-architecture.md) |
| Something users see | The [tutorial](../tutorials/README.md) that covers it, and its screenshots |
| A feature or a known gap | "What's built" in the README and [docs/05-feature-parity.md](../05-feature-parity.md) |

Code samples in the docs come from the code and the end-to-end tests; when you change one of those,
search the docs for the old field or route name.

### Screenshots

Every picture in `docs/tutorials/` and `docs/developer/` is taken from the running app by a script
in `docs/screenshots/shots/`, so it can be retaken after a UI change instead of going stale:

```bash
make up
make docs-shots                     # every script (creates the docs workspace first if needed)
make docs-shots SHOT=dev-06         # one guide's script (prefix match)
make docs-shots SHOT=03,dev-04      # several
git diff --stat docs/*/img          # then look at what changed before committing
```

Scripts run Playwright in its official image (`mcr.microsoft.com/playwright:v1.49.1-jammy`) on the
host network, against `http://localhost:7080`, signed in to the "Acme Retail" docs workspace. The
developer guides' scripts are `dev-NN-<guide>.mjs` and write `docs/developer/img/dev-NN-*.png`; the
tutorials' are `NN-<guide>.mjs`. `make docs-shots` exits non-zero if any script fails: check the exit
code.

When you change a page that a picture shows, retake that guide's pictures and check them. When you
add a guide, add one script for it (scripts don't share state, so several people can work at
once). The rules, mainly: no secrets in pictures (`maskSecrets`), only test values, don't change the
docs workspace for good (cancel forms; delete what you create, named with `runId()`), clip to what
the text talks about. Helpers and rules: [docs/screenshots/README.md](../screenshots/README.md).
Command-line output goes in code blocks, not screenshots.

Next: back to the [developer docs index](README.md).
