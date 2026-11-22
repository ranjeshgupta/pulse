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
- **Numbers**: `PULSE_KAFKA_PARTITIONS`, `PULSE_SIGNUP_RATE`, `PULSE_RFM_EVENTS_PER_SEC` and
  `PULSE_CH_QUERY_MAX_MEMORY` are whole numbers greater than 0; the send rates
  (`PULSE_EMAIL_RATE`, `PULSE_SMS_RATE`, `PULSE_WHATSAPP_RATE`, `PULSE_RCS_RATE`, `PULSE_PUSH_RATE`,
  `PULSE_INBOX_RATE`) are numbers greater than 0 and may have a fraction (`12.5` messages a second).
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
| `PULSE_PG_DSN` | `postgres://pulse:pulse@localhost:55432/pulse?sslmode=disable` | every role except `chsink` and `replay-dlq` | Postgres connection string (any form `pgx` accepts) | Always outside local development. Use `sslmode=require` or `verify-full` when the connection crosses a network |
| `PULSE_CH_ADDR` | `localhost:19000` | `migrate`, `api`, `chsink`, `segments`, `journeys` | ClickHouse **native protocol** address (port 9000 in a normal install, not the 8123 HTTP port) | Always outside local development |
| `PULSE_CH_DB` | `pulse` | same | ClickHouse database | You use another database name |
| `PULSE_CH_USER` | `pulse` | same | ClickHouse user | Always in production |
| `PULSE_CH_PASSWORD` | `pulse` | same | ClickHouse password | Always in production. The connection has no TLS option: keep ClickHouse on a private network |
| `PULSE_REDIS_ADDR` | `localhost:16379` | `api`, `profile`, `chsink`, `segments`, `journeys`, `dispatch`, `webhooks`, `integrations` | Redis `host:port`. There is no TLS setting | Always outside local development. Keep Redis on a private network |
| `PULSE_REDIS_PASSWORD` | empty | the same roles as `PULSE_REDIS_ADDR` | The Redis password (`requirepass`); empty connects without one | Whenever Redis has a password, as in `deploy/production` |
| `PULSE_KAFKA_BROKERS` | `localhost:19092` | every role | Kafka bootstrap brokers, comma-separated (`kafka-1:9092,kafka-2:9092`). Plain connections only (no TLS or SASL settings yet) | Always outside local development |
| `PULSE_CH_QUERY_TIMEOUT` | `60s` | `api` | Longest a ClickHouse query run for one API request may take (ClickHouse `max_execution_time`): reports, funnels, cohorts, paths, segment previews, dashboards. A query over it fails instead of holding the cluster. Contact exports (paths ending in `/export`) have no time limit. Use whole seconds: ClickHouse counts this limit in seconds | Your reports legitimately need longer, or you want a tighter bound on shared ClickHouse |
| `PULSE_CH_QUERY_MAX_MEMORY` | `4294967296` (4 GiB) | `api` | Most memory in bytes one such query may use (ClickHouse `max_memory_usage`), exports included | Your ClickHouse servers have more or less memory to spare per query |
| `PULSE_KAFKA_PARTITIONS` | `12` | `migrate` | Partitions for each topic `migrate` **creates**. Topics that already exist are left alone | Before the first `migrate` of a large installation: partitions cap how many instances of each consumer role do useful work. See [Scaling notes](01-installation.md#scaling-notes) |

### HTTP, URLs and sign-in

| Variable | Default | Used by | Meaning | Change it when |
|---|---|---|---|---|
| `PULSE_HTTP_ADDR` | `:7070` | `api` | Address the HTTP server listens on. The dev stack maps host port 7080 to it | You need another port, or to bind one interface (`127.0.0.1:7070`) |
| `PULSE_PUBLIC_URL` | `http://localhost:7080` | `api`, `dispatch`, `integrations` | Where browsers reach Pulse. Used for SSO redirects; tracking, unsubscribe and preference-center links in messages; provider webhook URLs and the bounce/reply URLs shown in Settings. An `https://` value names the session cookie `__Host-pulse_session` (which browsers only accept as `Secure`, for the whole host and no other), adds `Strict-Transport-Security` to every answer, and is used as the web push contact (VAPID subject) | Always in production: set the public `https://` address, without a trailing path |
| `PULSE_PAGES_URL` | none | `api` | A separate origin for workspace-authored content: landing pages (`/p/`), asset downloads (`/a/`) and hosted forms (`/f/`). When set, that host serves only those paths plus `/sdk/`, `/v1/web/` and `/healthz`, and the app host redirects page paths to it | Always in production, ideally on a separate registrable domain (for example `acme-pages.com` next to `pulse.acme.com`, not a sibling subdomain such as `pages.acme.com`), so pages your users write can never read or set the app's cookies. When `PULSE_PUBLIC_URL` is `https` and this is unset, `api` logs a warning at start. See [Two origins](01-installation.md#two-origins-public_url-and-pages_url) |
| `PULSE_TRUST_PROXY` | `false` | `api` | Take the client IP from the **right-most** `X-Forwarded-For` entry (rate limits, sign-in throttling, audit log, GeoIP) and the host from `X-Forwarded-Host` (for the pages split) | Only when Pulse can be reached **only** through your reverse proxy. Otherwise clients could pick their own IP |
| `PULSE_TRUST_CLOUDFLARE` | `false` | `api` | With `PULSE_TRUST_PROXY` also on, take the client IP from Cloudflare's `CF-Connecting-IP` header (falling back to `X-Forwarded-For` when it's missing or not an IP) | Pulse sits behind Cloudflare **and** your origin's firewall accepts connections only from [Cloudflare's IP ranges](https://www.cloudflare.com/ips/). Otherwise anyone who reaches the origin directly can send the header and choose their IP |
| `PULSE_ALLOW_SIGNUP` | `false` | `api` | When `true`, anyone who can reach Pulse may create a workspace at `/signup` (`POST /v1/auth/signup`); when `false` that page and route are refused and the operator creates workspaces with the admin token. The local dev stack turns it on | Off by default. Turn it on only behind a CAPTCHA (for example at your reverse proxy) or on a private network |
| `PULSE_SHARED_LOGINS` | none | `api` | Logins that several people share, such as a public demo: comma-separated `workspace-slug:email` pairs (`pulse-demo:demo@example.com`; a bare email is a startup error, so it can't match every workspace). Whoever is signed in with one can't change its password, set up or turn off two-step sign-in, or use "sign out everywhere" (each answers `403`); a workspace rule requiring two-step sign-in doesn't enrol it, though a setup already on still asks for its code; `GET /v1/auth/me` adds `"shared_login": true` and the Account page shows a note instead of those settings. An admin can still remove the user or set a new password by recreating it | You publish a demo login. Give it the **editor** or **viewer** role, and keep its workspace without a real email or SMS provider: visitors could otherwise message real people |
| `PULSE_SIGNUP_RATE` | `5` | `api` | With sign-up on: the most workspaces one client IP may create a minute; all sign-ups together are capped at ten times that a minute. Over it, `429` with `Retry-After: 60` | You expect bursts of real sign-ups from one address (an office behind one IP). The dev stack sets `1000` for the end-to-end tests |
| `PULSE_ADMIN_TOKEN` | none | `api` | Platform operator token for `POST /v1/tenants` (create a workspace without users). Unset turns that route off (it answers 401) | You create workspaces from your own tooling. Use a long random value: `openssl rand -hex 32`. The dev stack's `dev-admin-token` is refused unless `PULSE_PUBLIC_URL` is a local address |
| `PULSE_ALLOW_PRIVATE_SSO` | `false` | `api` | Lets workspace SSO discovery and token calls reach private and loopback addresses | Your identity provider is on an internal network. The dev stack turns it on for its Keycloak |
| `PULSE_GEOIP_DB` | none | `api` | Path to a MaxMind GeoLite2-City or GeoIP2-City `.mmdb` file. Fills `geo_*` attributes of web and app visitors (when the workspace allows it in Settings → Data & privacy). If the file can't be opened, `api` exits with `geoip database …` | You want location attributes. See [GeoIP](01-installation.md#geoip-optional) |

### Secrets and outbound calls

| Variable | Default | Used by | Meaning | Change it when |
|---|---|---|---|---|
| `PULSE_SECRET_KEY` | none | `api`, `journeys`, `dispatch`, `webhooks`, `integrations` | 64 hex characters (32 bytes, AES-256-GCM). Encrypts stored credentials (SSO client secrets, provider and integration credentials, webhook signing secrets, bounce mailbox passwords, two-step sign-in secrets), SSO and two-step sign-in state, email tracking and unsubscribe links, and signed download links. A value that isn't 64 hex characters stops the role with `secret key must be 64 hex characters (32 bytes)` | Always in production: `openssl rand -hex 32`, kept in a secret store and backed up. The dev stack's published key (in `deploy/docker-compose.yml`) is refused unless `PULSE_PUBLIC_URL` is a local address. Without it the log warns `PULSE_SECRET_KEY not set`, SSO, provider credentials and outbound webhooks don't work, and emails go out without open/click tracking |
| `PULSE_ALLOW_PRIVATE_WEBHOOKS` | `false` | `api`, `journeys`, `dispatch`, `webhooks`, `integrations` | Lets every user-entered URL reach private and loopback addresses (and use plain `http` where `https` is otherwise required): journey webhook steps, outbound webhooks and their tests, the custom HTTP gateway and other provider endpoints, custom authentication login and token URLs, Gupshup URLs, the web push install check, integrations (CRM, Shopify, ad platform base URLs), bounce mailboxes (IMAP) and SMTP servers a workspace enters (which otherwise must be public addresses on port 25, 465, 587 or 2525). The operator's own relay (`PULSE_SMTP_ADDR`) is never restricted | Local development and tests only (the dev stack turns it on so the e2e receivers on the Compose network can be called). Leave it off in production: any workspace admin can type these URLs |

### Workers

| Variable | Default | Used by | Meaning | Change it when |
|---|---|---|---|---|
| `PULSE_SMTP_ADDR` | `localhost:11025` | `dispatch` | SMTP relay (`host:port`) for workspaces that haven't added an email provider of their own. The dev default is Mailpit | Always in production (`smtp.example.com:587`) |
| `PULSE_SMTP_FROM` | `Pulse <noreply@pulse.local>` | `dispatch`, `api` | Default sender for that relay. Its domain is also accepted as a sender of AMP email post-backs (`/v1/amp/…`), like the From domains of the workspaces' email providers | Always in production |
| `PULSE_EMAIL_RATE` | `200` | `dispatch`, `api` | Emails per second **per workspace** (a Redis token bucket). Fractions are allowed (`0.5` is one every 2 seconds). It caps every workspace's email, whatever its providers' own **Messages per second** say; Settings → Channels shows it under each provider's sending limits, and warns when a provider's rate is higher | Your relay or providers allow more (or less) than 200 a second per workspace |
| `PULSE_SMS_RATE` | `100` | `dispatch`, `api` | The same for SMS: messages per second per workspace | As for `PULSE_EMAIL_RATE` |
| `PULSE_WHATSAPP_RATE` | `80` | `dispatch`, `api` | The same for WhatsApp | As for `PULSE_EMAIL_RATE` |
| `PULSE_RCS_RATE` | `50` | `dispatch`, `api` | The same for RCS | As for `PULSE_EMAIL_RATE` |
| `PULSE_PUSH_RATE` | `500` | `dispatch`, `api` | The same for push (mobile and web push) | As for `PULSE_EMAIL_RATE` |
| `PULSE_INBOX_RATE` | `2000` | `dispatch`, `api` | The same for app inbox messages | Rarely: the inbox has no outside provider |
| `PULSE_SWEEP_INTERVAL` | `5m` | `segments`, `api` | How often segments with event windows ("within the last N days") or relative dates are re-checked in ClickHouse, so membership decays without new events. The default for workspaces that leave **Re-check time-window segments every** (Settings → Automation & limits) at 0 | Shorter for fresher "within N days" segments, longer to take load off ClickHouse |
| `PULSE_MAINTENANCE_INTERVAL` | `1h` | `journeys` | How often the maintenance pass runs. Each pass runs the daily retention cleanup of the workspaces whose hour it is (each workspace once a day, in UTC hour `workspace id % 24`, by its Settings → Automation & limits) | Rarely; keep it at an hour or less so no workspace's hour is skipped |
| `PULSE_AUDIENCE_RECONCILE_INTERVAL` | `24h` | `integrations` | How often each ad audience sync (Facebook Custom Audiences, Google Ads Customer Match) re-checks its whole segment. Entries and exits stream in between | You want drift corrected sooner, or fewer API calls to the ad platforms |
| `PULSE_RFM_INTERVAL` | `24h` | `journeys` | How often each workspace's buyers' customer value (RFM group, score and churn risk) is scored and its recommendation lists worked out; each workspace runs at its own UTC hour (id % 24) when the interval is a day or more. A contact whose group, score or risk changed gets an `rfm_changed` event | You want churn risk to react faster (each pass reads two years of purchases) |
| `PULSE_RFM_EVENTS_PER_SEC` | `500` | `journeys` | The most `rfm_changed` events a scoring pass sends a second (a first pass changes every buyer) | Your pipeline has headroom and you want first passes over big workspaces to finish sooner |
| `PULSE_AI_API_KEY` | empty (off) | `api` | An Anthropic API key that turns on AI help (subject lines, rewrites, translations, segments from a sentence). Only the text being worked on is sent, never contacts' data | You want AI help; leave it empty to keep every request on your servers |

### Variables that aren't server settings

These appear in the repository but are read by tools, not by the `pulse` binary:

| Variable | Read by | Meaning |
|---|---|---|
| `PULSE_API` (default `http://localhost:7080`), `PULSE_MAILPIT` (`http://localhost:18025`), `PULSE_ADMIN_TOKEN` (`dev-admin-token`), `PULSE_HOOK_HOST` (unset) | end-to-end tests (`test/e2e`) | Where the tests find Pulse and Mailpit, the admin token they use, and the host name their webhook receivers listen on (tests that receive calls skip themselves when it is unset). Set by `make e2e` |
| `PULSE_KEYCLOAK` (`http://localhost:8180`), `PULSE_KEYCLOAK_INTERNAL` (`http://keycloak:8180`) | end-to-end SSO test (`test/e2e/sso_test.go`) | The Keycloak address a browser uses, and the one Pulse uses on the Compose network |
| `PULSE_ROBUST_ONLY` | end-to-end tests (`test/e2e/robust_test.go`) | Debugging aid: a regular expression over `METHOD /path` that limits which routes the robustness test calls |
| `PULSE_CH_ADDR` | integration tests (`make it`) | Same meaning as for the server; the tests skip themselves when it is unset. `make it` sets it to `clickhouse:9000` |
| `PULSE_URL`, `PULSE_DOCS_WORKSPACE`, `PULSE_DOCS_EMAIL`, `PULSE_DOCS_PASSWORD`, `PULSE_MAILPIT` | screenshot scripts (`docs/screenshots`) and `make ui-crawl` | Which Pulse and docs workspace `make docs-shots` captures (defaults `http://localhost:7080`, `acme-retail`, `docs.admin@pulse.test`, `pulse-docs-pass-1`) |
| `PULSE_SITE_KEY`, `PULSE_HOST` | Flutter SDK's optional integration test (`tool/integration_test.dart`) and example app (`--dart-define`) | See [5. Mobile SDKs](05-mobile-sdks.md) |
| `PULSE_AI_API_KEY` in the shell | `deploy/docker-compose.yml` | Passed through to the dev stack's `pulse` service (`${PULSE_AI_API_KEY:-}`), so AI help is on locally only when you export a key before `make up` |

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
| `chsink` | `events.enriched` and `segment.membership` → ClickHouse; also feeds the live event feed in Redis | `pulse-chsink` | ClickHouse, Redis, Kafka |
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
[the architecture document](../architecture.md#kafka-topics); [7. Architecture and
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
| Two-step sign-in codes | 5 tries per person per minute (each also counts as a sign-in attempt); the sign-in's code step is valid 10 minutes | `internal/api/twofactor.go` |
| Session lifetime | 14 days at most, and ended after 7 days without use (`__Host-pulse_session` cookie over https, `pulse_session` over plain http) | `SessionIdle` in `internal/store/pg/auth.go` |
| Password changes | 10 attempts per person an hour (`POST /v1/account/password`) | `internal/api/security.go` |
| Sign-ups (when on) | `PULSE_SIGNUP_RATE` per client IP a minute, ten times that in all | `internal/api/security.go` |
| Expensive requests, per workspace a minute | `POST /v1/reports/run`, `/v1/funnels/run`, `/v1/cohorts/run`, `/v1/revenue/report` 30; `/v1/analytics/paths` 20; `/v1/segments/preview` 60; `/v1/revenue/customer-value/refresh` 2; `GET /v1/contacts/export` 5. Over it: `429` with `Retry-After: 60` (API keys included) | `heavyRoutes` in `internal/api/security.go` |
| One event's `props` and `attrs` | Each at most 500 top-level keys; 10,000 values in all (nested included), 20 levels deep, text values and keys up to 64 KB | `internal/api/robust.go` |
| Background imports | 2 at a time per workspace (uploading, queued or running) and 20 GB uploaded per workspace in 24 hours; over it `429` | `internal/api/imports.go` |
| AI help | 20 requests a minute and 200 a UTC day per workspace; a rewrite's text at most 60 KB, a translation's fields at most 120 KB (as JSON), refused with `413`, never cut | `internal/api/ai.go`, `internal/ai` |
| Personalised templates | A template's functions may build at most 32 MB of text per render | `internal/personalize/budget.go` |
| Public campaign archive | A rendered send is kept 5 minutes (Redis) | `internal/api/archive.go` |
| API key cache | Each `api` instance caches a key's workspace for up to 1 minute, so a replaced key keeps working for up to a minute | `authCacheTTL` |
| Outbound webhook retries | 30 s, 2 min, 10 min, 1 h, 6 h after the first attempt (about 7 hours), then given up | `Backoff` in `internal/webhooks/webhooks.go` |
| Message retries (provider network error, timeout, 429, 5xx) | By default 30 s, 1 min, 2 min, 5 min, 10 min, 30 min, 1 h (at least the provider's `Retry-After`, capped at 1 h), then `<channel>_failed`. Per workspace in Settings → Automation & limits (1-20 tries, longest wait 1 minute to a day) | `BaseBackoff` in `internal/automation/plan.go` |
| Contacts list page | `limit` 1-200 (default 50) | `internal/api/contactview.go` |
| Segment rules | 8 levels deep, 200 nodes, event windows 1-365 days, `in` lists up to 1,000 values | `internal/segment/ast.go` |
| Journeys | 500 steps, waits up to 365 days, 25 jumps per enrollment | `internal/journey` |
| Audience estimates | Cached 30 s; 60 uncached a minute per workspace; frequency-cap check exact up to 50,000 contacts | `internal/api` (`segments/estimate`) |
| Contact lists behind numbers (`in:`, `campaign:`, `outcome:`…) | Bitmaps cached a minute; 120 new views a minute per workspace; counts exact up to 10,000 candidates, sampled above; "Create segment from this list" up to 500,000 contacts, 10 a minute, 2 at a time | `internal/api/contactsets.go` |
| Overview summary (`/v1/stats/summary`) | Built once a minute per workspace (15 s when a part was missing); 20 uncached builds a minute per workspace | `internal/api/overview.go` |
| Dashboards (Users, Campaign performance, Products) | Answers cached a minute; 60 uncached a minute per workspace; ranges up to 366 days | `internal/api/dashboards.go` |
| Live event feed | Newest 1,000 events per workspace (Settings → Automation & limits: 100-10,000), at most 50 a second, properties over 4 KB dropped; 120 polls a minute | `internal/live` |
| Provider tools | Test connection 20 a minute, test sends 30 a minute per workspace | `internal/api` |
| AMP emails | AMP part at most 200 KB (CSS 75,000 bytes); post-back links valid 60 days; 60 answers a minute per client IP and workspace, 10 per link | `internal/amp`, `internal/api/amp.go` |
| On-site overlap counts | Cached a minute; 30 uncached a minute per workspace | `internal/api/onsite_m6.go` |
| Web push install check | 10 a minute per workspace; 1 MB, 10 s per fetch; browser check links valid 30 minutes | `internal/webcheck` |
| Custom authentication tokens | Renewed a tenth of their lifetime (at most a minute) before expiry; one instance logs in at a time (15 s lock, others wait up to 8 s); unknown lifetime = 1 hour | `internal/httpauth` |

Workspace-level settings are not environment variables: admins change them in the app under
**Settings**, or through the API (`/v1/settings/*`, see the [API guide](03-api.md)). Delivery rules
(frequency caps, quiet hours, keywords) are in Settings → Channels; anonymous tracking and visitor
location in Settings → Data & privacy; identity verification and the two-step sign-in policy in Settings → Sign-in & keys; the web
push prompt in Settings → Web push; and in **Settings → Automation & limits**: how long each kind of
data is kept (events, message events, page views, per event name, anonymous visitors, audit log,
import history, webhook and integration logs, the test inbox, the live feed), send retries, the
segment re-check interval, when reachability is recounted, the default time of daily segments,
app inbox expiry and the sending limits new providers start with
([API guide](03-api.md#workspace-settings-automation--limits)).

Next: [3. API guide](03-api.md).
