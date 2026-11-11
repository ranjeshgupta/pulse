# 2. Configuration reference

Pulse reads all of its settings from `PULSE_*` environment variables when a process starts. There
is no configuration file and nothing is reloaded at runtime: change a variable, restart the
process. This page lists every variable (from `internal/config/config.go`), the process roles you
can run (from `cmd/pulse/main.go`), the ports involved, and the limits that are fixed in the code.

Installing and deploying are covered in [1. Installation](01-installation.md); this page is the
reference you look things up in.

---

## How values are read

`config.Load()` runs once at start-up, in every role:

- **Strings** (addresses, URLs, tokens): an unset or **empty** variable means "use the default". You
  can't set a variable to an empty string to blank out a default.
- **Booleans**: `true`, `1`, `yes` or `on` turn a switch on; `false`, `0`, `no` or `off` turn it off
  (any case).
- **Numbers**: `PULSE_KAFKA_PARTITIONS` is a whole number greater than 0; `PULSE_EMAIL_RATE` is a
  number greater than 0 and may have a fraction (`12.5` emails a second).
- **Durations** use Go's syntax: `90s`, `5m`, `1h30m`, `24h`, and must be greater than 0.

A value that doesn't parse stops the process at start-up, before it connects to anything, with exit
code 2 and one log line naming every bad variable, e.g.
`"msg":"bad settings","err":"PULSE_EMAIL_RATE=\"fast\": must be a number greater than 0"`. A typo
therefore never runs silently with the default.

---

## Every variable

Defaults suit `pulse all` running on your machine against the dev stack's host ports (see
[Ports](#ports)). Inside Docker Compose the dev stack overrides them with service names
(`deploy/docker-compose.yml`).

### Data services

| Variable | Default | Used by | Meaning | Change it when |
|---|---|---|---|---|
| `PULSE_PG_DSN` | `postgres://pulse:pulse@localhost:55432/pulse?sslmode=disable` | every role except `chsink` | Postgres connection string (any form `pgx` accepts) | Always outside local development. Use `sslmode=require` or `verify-full` when the connection crosses a network |
| `PULSE_CH_ADDR` | `localhost:19000` | `migrate`, `api`, `chsink`, `segments`, `journeys` | ClickHouse **native protocol** address (port 9000 in a normal install, not the 8123 HTTP port) | Always outside local development |
| `PULSE_CH_DB` | `pulse` | same | ClickHouse database | You use another database name |
| `PULSE_CH_USER` | `pulse` | same | ClickHouse user | Always in production |
| `PULSE_CH_PASSWORD` | `pulse` | same | ClickHouse password | Always in production. The connection has no TLS option: keep ClickHouse on a private network |
| `PULSE_REDIS_ADDR` | `localhost:16379` | `api`, `profile`, `segments`, `journeys`, `dispatch`, `webhooks`, `integrations` | Redis `host:port`. There is no password or TLS setting | Always outside local development. Keep Redis on a private network |
| `PULSE_KAFKA_BROKERS` | `localhost:19092` | every role | Kafka bootstrap brokers, comma-separated (`kafka-1:9092,kafka-2:9092`). Plain connections only (no TLS or SASL settings yet) | Always outside local development |
| `PULSE_KAFKA_PARTITIONS` | `12` | `migrate` | Partitions for each topic `migrate` **creates**. Topics that already exist are left alone | Before the first `migrate` of a large installation: partitions cap how many instances of each consumer role do useful work. See [Scaling notes](01-installation.md#scaling-notes) |

### HTTP, URLs and sign-in

| Variable | Default | Used by | Meaning | Change it when |
|---|---|---|---|---|
| `PULSE_HTTP_ADDR` | `:7070` | `api` | Address the HTTP server listens on. The dev stack maps host port 7080 to it | You need another port, or to bind one interface (`127.0.0.1:7070`) |
| `PULSE_PUBLIC_URL` | `http://localhost:7080` | `api`, `dispatch` | Where browsers reach Pulse. Used for SSO redirects; tracking, unsubscribe and preference-center links in messages; provider webhook URLs and the bounce/reply URLs shown in Settings. An `https://` value makes the session cookie `Secure`, and is used as the web push contact (VAPID subject) | Always in production: set the public `https://` address, without a trailing path |
| `PULSE_PAGES_URL` | none | `api` | A separate origin for workspace-authored content: landing pages (`/p/`), asset downloads (`/a/`) and hosted forms (`/f/`). When set, that host serves only those paths plus `/sdk/`, `/v1/web/` and `/healthz`, and the app host redirects page paths to it | In production, so pages your users write never share an origin or cookies with the app. See [Two origins](01-installation.md#two-origins-public_url-and-pages_url) |
| `PULSE_TRUST_PROXY` | `false` | `api` | Take the client IP from the **right-most** `X-Forwarded-For` entry (rate limits, sign-in throttling, audit log, GeoIP) and the host from `X-Forwarded-Host` (for the pages split) | Only when Pulse can be reached **only** through your reverse proxy. Otherwise clients could pick their own IP |
| `PULSE_ALLOW_SIGNUP` | `true` | `api` | Anyone may create a workspace at `/signup` (`POST /v1/auth/signup`) | Set `false` on a private installation and create workspaces with the admin token |
| `PULSE_ADMIN_TOKEN` | none | `api` | Platform operator token for `POST /v1/tenants` (create a workspace without users). Unset turns that route off (it answers 401) | You create workspaces from your own tooling. Use a long random value: `openssl rand -hex 32` |
| `PULSE_ALLOW_PRIVATE_SSO` | `false` | `api` | Lets workspace SSO discovery and token calls reach private and loopback addresses | Your identity provider is on an internal network. The dev stack turns it on for its Keycloak |
| `PULSE_GEOIP_DB` | none | `api` | Path to a MaxMind GeoLite2-City or GeoIP2-City `.mmdb` file. Fills `geo_*` attributes of web and app visitors (when the workspace allows it in Settings → Data & privacy). If the file can't be opened, `api` exits with `geoip database …` | You want location attributes. See [GeoIP](01-installation.md#geoip-optional) |

### Secrets and outbound calls

| Variable | Default | Used by | Meaning | Change it when |
|---|---|---|---|---|
| `PULSE_SECRET_KEY` | none | `api`, `journeys`, `dispatch`, `webhooks`, `integrations` | 64 hex characters (32 bytes, AES-256-GCM). Encrypts stored credentials (SSO client secrets, provider and integration credentials, webhook signing secrets, bounce mailbox passwords), SSO state, email tracking and unsubscribe links, and signed download links. A value that isn't 64 hex characters stops the role with `secret key must be 64 hex characters (32 bytes)` | Always in production: `openssl rand -hex 32`, kept in a secret store and backed up. Without it the log warns `PULSE_SECRET_KEY not set`, SSO, provider credentials and outbound webhooks don't work, and emails go out without open/click tracking |
| `PULSE_ALLOW_PRIVATE_WEBHOOKS` | `false` | `api`, `journeys`, `dispatch`, `webhooks`, `integrations` | Lets every user-entered URL reach private and loopback addresses: journey webhook steps, outbound webhooks and their tests, the custom HTTP gateway and other provider endpoints, integrations (CRM, Shopify, ad platform base URLs) and bounce mailboxes (IMAP) | Local development and tests only (the dev stack turns it on so the e2e receivers on the Compose network can be called). Leave it off in production: any workspace admin can type these URLs |

### Workers

| Variable | Default | Used by | Meaning | Change it when |
|---|---|---|---|---|
| `PULSE_SMTP_ADDR` | `localhost:11025` | `dispatch` | SMTP relay (`host:port`) for workspaces that haven't added an email provider of their own. The dev default is Mailpit | Always in production (`smtp.example.com:587`) |
| `PULSE_SMTP_FROM` | `Pulse <noreply@pulse.local>` | `dispatch` | Default sender for that relay | Always in production |
| `PULSE_EMAIL_RATE` | `200` | `dispatch` | Emails per second **per workspace** (a Redis token bucket). Fractions are allowed (`0.5` is one every 2 seconds) | Your relay or provider allows more or less. The other channels have fixed rates, see [Limits](#limits-fixed-in-the-code) |
| `PULSE_SWEEP_INTERVAL` | `5m` | `segments` | How often segments with event windows ("within the last N days") or relative dates are re-checked in ClickHouse, so membership decays without new events | Shorter for fresher "within N days" segments, longer to take load off ClickHouse |
| `PULSE_MAINTENANCE_INTERVAL` | `1h` | `journeys` | How often retention cleanup runs (anonymous visitors, events, audit log, per each workspace's Settings → Data & privacy) | Rarely |
| `PULSE_AUDIENCE_RECONCILE_INTERVAL` | `24h` | `integrations` | How often each ad audience sync (Facebook Custom Audiences, Google Ads Customer Match) re-checks its whole segment. Entries and exits stream in between | You want drift corrected sooner, or fewer API calls to the ad platforms |

### Variables that aren't server settings

These appear in the repository but are read by tools, not by the `pulse` binary:

| Variable | Read by | Meaning |
|---|---|---|
| `PULSE_API`, `PULSE_MAILPIT`, `PULSE_ADMIN_TOKEN`, `PULSE_HOOK_HOST` | end-to-end tests (`test/e2e`) | Where the tests find Pulse and Mailpit, the admin token they use, and the host name their webhook receivers listen on. Set by `make e2e` |
| `PULSE_CH_ADDR`, `PULSE_REDIS_ADDR` | integration tests (`make it`) | Same meaning as for the server; `make it` points them at the Compose services |
| `PULSE_URL`, `PULSE_DOCS_WORKSPACE`, `PULSE_DOCS_EMAIL`, `PULSE_DOCS_PASSWORD`, `PULSE_MAILPIT` | screenshot scripts (`docs/screenshots`) | Which Pulse and docs workspace `make docs-shots` captures |
| `PULSE_SITE_KEY`, `PULSE_HOST` | Flutter SDK's optional integration test | See [5. Mobile SDKs](05-mobile-sdks.md) |

---

## Process roles

One binary, `pulse <role>`. Without a valid role it prints
`usage: pulse migrate|api|profile|chsink|segments|journeys|dispatch|webhooks|integrations|all|replay-dlq` and
exits with status 2. Each role logs JSON lines to stdout with a `role` field, stops cleanly on
`SIGTERM` or Ctrl+C, and exits with status 1 if it fails.

| Role | Runs | Consumer group | Connects to |
|---|---|---|---|
| `migrate` | Postgres and ClickHouse migrations, then creates the Kafka topics (retrying for up to 30 s while the broker starts). Exits | | Postgres, ClickHouse, Kafka |
| `api` | The HTTP server: web app, REST API, SDK endpoints, tracking links, hosted pages and forms, provider webhooks | | Postgres, ClickHouse, Redis, Kafka (producer) |
| `profile` | `events.raw` → identity resolution, profile merge, event counters → `events.enriched` | `pulse-profile` | Postgres, Redis, Kafka |
| `chsink` | `events.enriched` and `segment.membership` → ClickHouse | `pulse-chsink` | ClickHouse, Kafka |
| `segments` | Real-time membership from `events.enriched`; backfills of new and edited segments; sweeps every `PULSE_SWEEP_INTERVAL`; segments that depend on other segments (from `segment.membership`) | `pulse-segments`, `pulse-segments-deps` | Postgres, ClickHouse, Redis, Kafka |
| `journeys` | Journey enrollment and steps (from `segment.membership` and `events.enriched`), durable timers, scheduled broadcasts (every second), background imports (every 2 s), retention cleanup, journey A/B winners (every 5 s) | `pulse-journeys` | Postgres, ClickHouse, Redis, Kafka |
| `dispatch` | `messages.<channel>` and `delivery.receipts` → consent, caps, quiet hours, rate limits, providers, SMS fallback; held (quiet hours) messages and retries of temporary provider failures | `pulse-dispatch` | Postgres, Redis, Kafka |
| `webhooks` | Outbound webhooks and Zapier / Make subscriptions, from `events.enriched` and `segment.membership`; retries | `pulse-webhooks` | Postgres, Redis, Kafka |
| `integrations` | CRM pulls (due pulls are claimed every 10 s), ad audience sync, bounce mailboxes (each read every 5 minutes; only when `PULSE_SECRET_KEY` is set, since mailbox passwords are sealed with it) | `pulse-integrations` | Postgres, Redis, Kafka |
| `all` | `migrate`, then every role above in one process | all of the above | everything |
| `replay-dlq` | A command, not a worker: `pulse replay-dlq [--limit N] [--dry-run] <topic or channel>` moves records from `<topic>.dlq` back to `<topic>`, then exits ([how](01-installation.md#dead-letter-topics-and-replaying-them)) | `pulse-replay-dlq` | Kafka |

A role opens only the connections it uses (`deps` in `cmd/pulse/main.go` connects lazily), but
it's simplest to give every role the same environment. `migrate` takes a Postgres advisory lock, so
several `migrate` or `all` processes starting together migrate one at a time.

What each role reads and writes on Kafka is in
[the architecture document](../02-architecture.md#kafka-topics); [7. Architecture and
extending](07-architecture-and-extending.md) walks through the code.

---

## Ports

| Port | Who listens | Notes |
|---|---|---|
| 7070 | `pulse api` (`PULSE_HTTP_ADDR`) | The only port Pulse itself opens. Plain HTTP: put TLS in front ([reverse proxy](01-installation.md#tls-and-the-reverse-proxy)) |
| 7080 (host) | dev stack | Mapped to the container's 7070 (`deploy/docker-compose.yml`), because 7070 is often taken on the dev machine |

Ports Pulse **connects to** (dev stack host ports first, the container ports after the arrow):

| Service | Dev host port → container | Variable |
|---|---|---|
| Postgres | 55432 → 5432 | `PULSE_PG_DSN` |
| ClickHouse native | 19000 → 9000 | `PULSE_CH_ADDR` |
| ClickHouse HTTP (not used by Pulse; handy for queries) | 18123 → 8123 | |
| Redis | 16379 → 6379 | `PULSE_REDIS_ADDR` |
| Kafka | 19092 (`EXTERNAL` listener) / 9092 inside Compose | `PULSE_KAFKA_BROKERS` |
| SMTP (Mailpit) | 11025 → 1025 | `PULSE_SMTP_ADDR` |
| Mailpit web UI and API | 18025 → 8025 | (tests and docs only) |
| Keycloak | 8180 | (configured per workspace in Settings → Sign-in & keys) |

The defaults in `config.go` are the **host** ports, so `go run ./cmd/pulse all` on your machine
talks to the dev stack's databases without any variables. The Compose file sets the in-network
addresses (`postgres:5432`, `clickhouse:9000`, `redis:6379`, `kafka:9092`, `mailpit:1025`).

---

## Limits fixed in the code

Not configurable today; listed so you know what to expect.

| What | Limit | Where |
|---|---|---|
| Request body | 4 MB; `POST /v1/contacts/import` 200 MB; `POST /v1/imports` 10 GB; `POST /v1/assets` 26 MB (25 MB per file) | `bodyLimit` in `internal/api/server.go` |
| Events per `POST /v1/events` | 1-1,000 | `maxBatch` |
| Events per `POST /v1/web/events` (site key) | 1-50, and 600 events per minute per client IP and workspace | `internal/api/web.go` |
| Form submissions | 10 per minute per client IP and workspace; faster than 2 s after the form was shown (or without the time it was shown: `_ts` on the hosted page, `shown` from the SDK), or with the honeypot filled, is treated as a bot | `internal/api/forms.go` |
| Sign-in attempts | 10 per workspace and email in 15 minutes (successful ones count too) | `internal/api/auth.go` |
| Session lifetime | 14 days (`pulse_session` cookie) | `internal/api/server.go` |
| API key cache | Each `api` instance caches a key's workspace for up to 1 minute, so a replaced key keeps working for up to a minute | `authCacheTTL` |
| Send rates per workspace | email `PULSE_EMAIL_RATE` (200/s), SMS 100/s, WhatsApp 80/s, RCS 50/s, push 500/s, app inbox 2,000/s | `DefaultRates` in `internal/dispatch/dispatcher.go` |
| Outbound webhook retries | 30 s, 2 min, 10 min, 1 h, 6 h after the first attempt (about 7 hours), then given up | `Backoff` in `internal/webhooks/webhooks.go` |
| Message retries (provider network error, timeout, 429, 5xx) | 30 s, 1 min, 2 min, 5 min, 10 min, 30 min, 1 h (at least the provider's `Retry-After`, capped at 1 h), then `<channel>_failed` | `RetryBackoff` in `internal/dispatch/retry.go` |
| Contacts list page | `limit` 1-200 (default 50) | `internal/api/contactview.go` |
| Segment rules | 8 levels deep, 200 nodes, event windows 1-365 days, `in` lists up to 1,000 values | `internal/segment/ast.go` |
| Journeys | 500 steps, waits up to 365 days, 25 jumps per enrollment | `internal/journey` |

Workspace-level settings (delivery rules, frequency caps, quiet hours, retention, anonymous tracking,
identity verification) are not environment variables: admins change them in the app under
**Settings**, or through the API (`/v1/settings/*`, see the [API guide](03-api.md)).

Next: [3. API guide](03-api.md).
