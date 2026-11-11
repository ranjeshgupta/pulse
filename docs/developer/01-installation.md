# 1. Installation

This guide gets Pulse running on your machine in a few minutes, explains what each container is
for, and then covers running it in production: one server with Docker Compose, or Pulse's roles
spread over several machines with managed databases.

All commands run from the `pulse/` directory of the repository.

---

## Requirements

| | Local development | Production (one server, up to a few million contacts) |
|---|---|---|
| Software | Docker with the Compose plugin (`docker compose`), `make` | Docker and Compose, or any container platform |
| CPU | 4 cores | 8 cores or more |
| Memory | 8 GB free for Docker (the stack idles at about 3–4 GB) | 16 GB or more; ClickHouse, Kafka and Redis want the most |
| Disk | 10 GB (about 2.2 GB of images, plus build caches and data) | SSD; size it for your event volume (ClickHouse keeps every event) |

Go and Node.js are **not** needed on the host: the Makefile runs Go in the `golang:1.26-alpine`
image, and the Pulse image builds the web app in `node:24-alpine`. (You need Node.js only to run
the web app's dev server, see [Working on the web app](#working-on-the-web-app).)

### Ports used on your machine

The dev stack's host ports are offset so it can run next to other stacks:

| Port | Service |
|---|---|
| **7080** | Pulse web app and API (the container listens on 7070) |
| 18025 | Mailpit web UI (every email Pulse sends locally) |
| 11025 | Mailpit SMTP |
| 8180 | Keycloak (local single sign-on) |
| 18123, 19000 | ClickHouse HTTP and native protocol |
| 55432 | Postgres |
| 16379 | Redis |
| 19092 | Kafka (for clients on the host) |

If one is taken, change the left-hand number of that `ports:` entry in
`deploy/docker-compose.yml`. For Pulse itself, also change `PULSE_PUBLIC_URL` (see
[Settings that matter](#settings-that-matter)).

---

## Local quick start

```bash
make up      # build the Pulse image and start everything (first time: a few minutes)
make ps      # every service should be Up; the data services say (healthy)
make seed    # optional: a "Demo Retail" workspace full of sample data
```

`make up` is `docker compose -f deploy/docker-compose.yml up -d --build`. Pulse waits until Kafka,
ClickHouse, Postgres and Redis report healthy, applies its database migrations, creates its Kafka
topics and starts. When it is ready, `make ps` shows something like:

```text
NAME                 IMAGE                               SERVICE      STATUS                  PORTS
pulse-clickhouse-1   clickhouse/clickhouse-server:25.8   clickhouse   Up 2 minutes (healthy)  0.0.0.0:18123->8123/tcp, 0.0.0.0:19000->9000/tcp
pulse-kafka-1        apache/kafka:4.1.0                  kafka        Up 2 minutes (healthy)  9092/tcp, 0.0.0.0:19092->19092/tcp
pulse-keycloak-1     quay.io/keycloak/keycloak:26.4      keycloak     Up 2 minutes            0.0.0.0:8180->8180/tcp
pulse-mailpit-1      axllent/mailpit:latest              mailpit      Up 2 minutes (healthy)  0.0.0.0:11025->1025/tcp, 0.0.0.0:18025->8025/tcp
pulse-postgres-1     postgres:17-alpine                  postgres     Up 2 minutes (healthy)  0.0.0.0:55432->5432/tcp
pulse-pulse-1        pulse-pulse                         pulse        Up 1 minute             0.0.0.0:7080->7070/tcp
pulse-redis-1        redis:8-alpine                      redis        Up 2 minutes (healthy)  0.0.0.0:16379->6379/tcp
```

and `make logs` (Pulse's log, JSON lines) shows `"msg":"migrations applied"` followed by
`"msg":"listening","role":"api","addr":":7070"`. Press Ctrl+C to stop following the log.

Open **http://localhost:7080**.

### First sign-in

You have two choices:

- **Create a workspace:** on the sign-in page click **New to Pulse? Create a workspace** (or open
  `http://localhost:7080/signup`). This is [tutorial 1](../tutorials/01-getting-started.md).
- **Use the demo workspace** made by `make seed`: workspace ID `demo-retail`, email
  `demo.admin@pulse.test`, password `pulse-demo-pass-1`. If `demo-retail` already existed, the seed
  prints the new ID (`demo-retail-xxxxxx`). All local test accounts are listed in
  [deploy/dev-seed.md](../../deploy/dev-seed.md).

![The Pulse sign-in page: Workspace ID, Email, Password, Sign in, and Sign in with SSO](img/dev-01-login.png)

### Mail: Mailpit

Locally, every email goes to **Mailpit** instead of real inboxes: open **http://localhost:18025**.
Pulse's default SMTP relay (`PULSE_SMTP_ADDR`) points there, so journeys, broadcasts and test sends
arrive within seconds. Here, the seeded workspace's "Weekend sale" broadcast:

![Mailpit listing the Weekend sale emails sent by the seeded workspace](img/dev-01-mailpit.png)

### Single sign-on: Keycloak

The stack includes **Keycloak** with a realm `pulse`, imported from `deploy/keycloak/pulse-realm.json`,
so you can try workspace SSO without an identity provider of your own. The admin console is
**http://localhost:8180/admin** (`admin` / `admin`); the test user is `sso.tester` /
`Pulse-SSO-test-1`. The values to enter in **Settings → Sign-in & keys → Single sign-on (OIDC)** are in
[deploy/dev-seed.md](../../deploy/dev-seed.md). Keycloak is for development only; leave it out in
production and use your own provider.

### Other commands

| Command | Does |
|---|---|
| `make down` | Stop and remove the containers. Data stays in Docker volumes (`pulse_postgres-data`, `pulse_clickhouse-data`, `pulse_kafka-data`, `pulse_redis-data`) |
| `make logs` | Follow Pulse's log |
| `make seed` | Another demo workspace (`ARGS="-contacts 1000"` for more contacts) |
| `make docs-seed` | The "Acme Retail" workspace used for the documentation's screenshots |
| `make test` | Go unit tests (no services needed) |
| `make it` / `make e2e` | Integration and end-to-end tests against the running stack |
| `make load ARGS="-rate 10000 -duration 45s"` | Load test ([results](../03-load-test.md)) |

To start again from empty databases: `docker compose -f deploy/docker-compose.yml down -v`. The `-v`
**deletes all the volumes** (every workspace, contact and event); use it only on a local stack.

### Working on the web app

The web app (React + TypeScript, in `web/`) is built into the Pulse binary. To change it with
live reload, run Vite's dev server; it proxies `/v1` to the Pulse on port 7080:

```bash
cd web && npm install && npm run dev    # http://localhost:5173
npm test                                # its unit tests
```

Rebuild the image (`make up`) to see your changes on port 7080.

---

## What each container does

| Container | Image | Role in Pulse |
|---|---|---|
| `pulse` | built from `Dockerfile` | The Pulse binary, here running `pulse all`: the HTTP API and web app, plus every background worker (below) |
| `kafka` | `apache/kafka:4.1.0` | The event bus. Every event, segment change and message to send passes through a topic, so each part of Pulse works at its own pace and nothing is lost when one restarts |
| `postgres` | `postgres:17-alpine` | Workspaces, users, contacts and their attributes, segments, journeys and their timers, templates, campaigns, settings, uploaded assets and imports in progress |
| `clickhouse` | `clickhouse/clickhouse-server:25.8` | The event log and analytics: every event, message outcome and segment change; contact search, reports, funnels and cohorts |
| `redis` | `redis:8-alpine` | Live segment membership (bitmaps), event counters for segment rules, send idempotency and rate limits, sign-in throttling, on-site state |
| `mailpit` | `axllent/mailpit:latest` | Development only: catches outgoing email |
| `keycloak` | `quay.io/keycloak/keycloak:26.4` | Development only: a local OpenID Connect provider for SSO |

### Pulse's roles

The binary runs one **role** per process, or all of them (`cmd/pulse/main.go`):

| Role | Does | Reads | Writes |
|---|---|---|---|
| `migrate` | Applies Postgres and ClickHouse migrations, creates Kafka topics, then exits | | |
| `api` | HTTP: the web app, REST API, event ingestion, web SDK, tracking links, hosted pages | Postgres, ClickHouse, Redis | `events.raw`, `messages.*` |
| `profile` | Identity and profile: finds or creates the contact, merges attributes and anonymous visitors | `events.raw` | `events.enriched` |
| `chsink` | Writes events and segment changes to ClickHouse | `events.enriched`, `segment.membership` | ClickHouse |
| `segments` | Real-time segment membership, backfills of new segments, periodic sweeps | `events.enriched`, `segment.membership` | `segment.membership`, Redis |
| `journeys` | Journey steps and timers; also runs scheduled broadcasts, background imports, retention cleanup and A/B winner picks | `segment.membership`, `events.enriched` | `messages.*`, `events.raw` |
| `dispatch` | Sends messages on every channel: consent, frequency caps, quiet hours, rate limits, providers, SMS fallback | `messages.*` | `events.raw` (sent / failed) |
| `webhooks` | Your workspaces' outbound webhooks and Zapier / Make subscriptions | events, segment changes | HTTP |
| `integrations` | CRM pulls on a schedule, ad audience sync, bounce mailboxes (IMAP) | `segment.membership`, `events.enriched` | `events.raw`, HTTP |
| `all` | `migrate`, then every role above in one process | | |

`pulse all` is what the dev stack and a single-server install run. In a larger installation each
role is its own deployment and scales on its own Kafka consumer group.

---

## Production deployment

### Settings that matter

Every setting is an environment variable; the full table is in the [README](../../README.md#settings)
and `internal/config/config.go`. For production, set at least:

| Variable | Why |
|---|---|
| `PULSE_SECRET_KEY` | 64 hex characters (`openssl rand -hex 32`). Encrypts stored credentials (SSO client secrets, provider and integration credentials, bounce mailbox passwords), SSO state, tracking links and download links. Without it SSO and stored provider credentials don't work, and emails go out without open and click tracking. **Keep it in a secret store and back it up**: if it's lost, everything encrypted with it has to be entered again. Don't reuse the dev key from `deploy/docker-compose.yml` |
| `PULSE_PUBLIC_URL` | The `https://` address people use, e.g. `https://pulse.example.com`. Used for SSO redirects, tracking, unsubscribe and webhook links; `https` also makes the session cookie `Secure` |
| `PULSE_PAGES_URL` | A **separate origin** for landing pages (`/p/`), asset downloads (`/a/`) and hosted forms (`/f/`), e.g. `https://pages.example.com`. Content your users write then never shares an origin or cookies with the app. See [Two origins](#two-origins-public_url-and-pages_url) |
| `PULSE_PG_DSN`, `PULSE_CH_*`, `PULSE_REDIS_ADDR`, `PULSE_KAFKA_BROKERS` | Where the data services are. Use `sslmode=require` (or `verify-full`) in the Postgres DSN when it crosses a network |
| `PULSE_SMTP_ADDR`, `PULSE_SMTP_FROM` | The SMTP relay for workspaces without an email provider of their own, and its default sender |
| `PULSE_ALLOW_SIGNUP=false` | On a private installation, so strangers can't create workspaces. Create them with `PULSE_ADMIN_TOKEN` instead (below) |
| `PULSE_ADMIN_TOKEN` | A long random value (`openssl rand -hex 32`) for `POST /v1/tenants`; leave it unset to turn that route off |
| `PULSE_TRUST_PROXY=true` | Only when Pulse is reachable **only** through your reverse proxy: then client IPs (rate limits, audit log) come from `X-Forwarded-For` |

Keep `PULSE_ALLOW_PRIVATE_SSO` and `PULSE_ALLOW_PRIVATE_WEBHOOKS` off (the default) unless you know
you need them: they let workspace admins make Pulse call addresses on your internal network. The
dev compose file turns them on for the local Keycloak and the end-to-end tests.

### Build the image

```bash
docker build -t pulse:latest .     # from pulse/; tag it with your version and push it to your registry
```

The image (`alpine:3.22`, about 50 MB) runs as user `pulse`, listens on port 7070 (`PULSE_HTTP_ADDR`)
and takes the role as its argument: `docker run … pulse:latest all`. Health check: `GET /healthz`
returns `{"status":"ok"}`. Logs are JSON lines on stdout.

### Option A: one server with Docker Compose

Start from `deploy/docker-compose.yml` (keep a copy outside the repository, e.g.
`/srv/pulse/compose.yml`) and change it:

1. **Remove** the `mailpit` and `keycloak` services, and `mailpit` from Pulse's `depends_on`.
2. **Remove the `ports:`** of `kafka`, `clickhouse`, `postgres` and `redis`: Pulse reaches them on
   the Compose network, nothing else should. For Kafka, also drop the `EXTERNAL` listener:
   `KAFKA_LISTENERS: PLAINTEXT://:9092,CONTROLLER://:9093`,
   `KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:9092` and
   `KAFKA_LISTENER_SECURITY_PROTOCOL_MAP: CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT`.
3. **Change the passwords** of Postgres (`POSTGRES_PASSWORD`) and ClickHouse (`CLICKHOUSE_PASSWORD`)
   and use them in `PULSE_PG_DSN` and `PULSE_CH_PASSWORD`. Put secrets in an `.env` file next to the
   compose file (readable only by root) rather than in the file itself.
4. **Replace the `pulse` service** with your image and production settings:

```yaml
  pulse:
    image: registry.example.com/pulse:1.0.0      # the image you built
    restart: unless-stopped
    command: ["all"]
    depends_on:
      kafka: { condition: service_healthy }
      clickhouse: { condition: service_healthy }
      postgres: { condition: service_healthy }
      redis: { condition: service_healthy }
    environment:
      PULSE_HTTP_ADDR: ":7070"
      PULSE_PG_DSN: postgres://pulse:${POSTGRES_PASSWORD}@postgres:5432/pulse?sslmode=disable
      PULSE_CH_ADDR: clickhouse:9000
      PULSE_CH_DB: pulse
      PULSE_CH_USER: pulse
      PULSE_CH_PASSWORD: ${CLICKHOUSE_PASSWORD}
      PULSE_REDIS_ADDR: redis:6379
      PULSE_KAFKA_BROKERS: kafka:9092
      PULSE_SMTP_ADDR: smtp.example.com:587
      PULSE_SMTP_FROM: "Acme <news@example.com>"
      PULSE_PUBLIC_URL: https://pulse.example.com
      PULSE_PAGES_URL: https://pages.example.com
      PULSE_SECRET_KEY: ${PULSE_SECRET_KEY}
      PULSE_ALLOW_SIGNUP: "false"
      PULSE_ADMIN_TOKEN: ${PULSE_ADMIN_TOKEN}
      PULSE_TRUST_PROXY: "true"
      PULSE_GEOIP_DB: /geoip/GeoLite2-City.mmdb   # optional, see GeoIP below
    volumes:
      - /srv/pulse/geoip:/geoip:ro
    ports:
      - "127.0.0.1:7070:7070"    # only the reverse proxy on this server reaches Pulse
```

Then `docker compose -f /srv/pulse/compose.yml up -d`, and put a reverse proxy with TLS in front
(below).

### Option B: separate roles and managed services

For more throughput or availability, run each role as its own service (any number of `api`
instances behind a load balancer, consumer roles scaled separately) against Postgres, ClickHouse,
Kafka and Redis that you run elsewhere or buy as managed services.

Run `pulse migrate` **once** before starting (and before every upgrade), as a one-off job, then the
roles. In Compose that looks like this (environment as in option A, shared through a YAML anchor):

```yaml
x-pulse: &pulse
  image: registry.example.com/pulse:1.0.0
  restart: unless-stopped
  environment: &env
    PULSE_PG_DSN: postgres://pulse:${POSTGRES_PASSWORD}@db.internal:5432/pulse?sslmode=require
    PULSE_CH_ADDR: clickhouse.internal:9000
    PULSE_CH_PASSWORD: ${CLICKHOUSE_PASSWORD}
    PULSE_REDIS_ADDR: redis.internal:6379
    PULSE_KAFKA_BROKERS: kafka-1.internal:9092,kafka-2.internal:9092,kafka-3.internal:9092
    PULSE_PUBLIC_URL: https://pulse.example.com
    PULSE_PAGES_URL: https://pages.example.com
    PULSE_SECRET_KEY: ${PULSE_SECRET_KEY}
    # … the rest of option A's settings

services:
  migrate:
    <<: *pulse
    restart: "no"
    command: ["migrate"]
  api:
    <<: *pulse
    command: ["api"]
    ports: ["127.0.0.1:7070:7070"]
    depends_on: { migrate: { condition: service_completed_successfully } }
  profile:      { <<: *pulse, command: ["profile"],      depends_on: { migrate: { condition: service_completed_successfully } } }
  chsink:       { <<: *pulse, command: ["chsink"],       depends_on: { migrate: { condition: service_completed_successfully } } }
  segments:     { <<: *pulse, command: ["segments"],     depends_on: { migrate: { condition: service_completed_successfully } } }
  journeys:     { <<: *pulse, command: ["journeys"],     depends_on: { migrate: { condition: service_completed_successfully } } }
  dispatch:     { <<: *pulse, command: ["dispatch"],     depends_on: { migrate: { condition: service_completed_successfully } } }
  webhooks:     { <<: *pulse, command: ["webhooks"],     depends_on: { migrate: { condition: service_completed_successfully } } }
  integrations: { <<: *pulse, command: ["integrations"], depends_on: { migrate: { condition: service_completed_successfully } } }
```

Every role needs the same settings: roles only connect to the services they use, but sharing one
environment keeps them consistent. `PULSE_GEOIP_DB` matters only to `api`; `PULSE_SECRET_KEY` to
`api`, `journeys`, `dispatch`, `webhooks` and `integrations`.

What the managed services must offer today:

| Service | Versions in the dev stack | Notes |
|---|---|---|
| Postgres | 17 | Any managed Postgres; TLS through the DSN (`sslmode=require`) |
| ClickHouse | 25.8 | Native protocol (port 9000) with user and password. Pulse connects to one address without TLS, so keep it on a private network |
| Kafka | 4.1 | Plain connection to the brokers: Pulse has no TLS or SASL settings yet, so a managed Kafka must allow an unauthenticated listener on a private network. Topics: see below |
| Redis | 8 | One address (`PULSE_REDIS_ADDR`), no password or TLS settings yet: keep it on a private network. Turn on persistence (AOF), as the dev stack does |

**Kafka topics.** `pulse migrate` creates `events.raw`, `events.enriched`, `segment.membership`,
`delivery.receipts` and `messages.email`, `messages.sms`, `messages.whatsapp`, `messages.rcs`,
`messages.push`, `messages.inbox`, each with a `.dlq` twin (`events.raw.dlq`, …,
`delivery.receipts.dlq`, `messages.email.dlq`, …), with `PULSE_KAFKA_PARTITIONS` partitions (12 by
default) and **replication factor 1**, and leaves topics that already exist alone. On a cluster of
three or more brokers, create these topics yourself first with replication factor 3 and the
partition count you want; `migrate` then keeps them.

### Scaling notes

- **Partitions cap parallelism.** Each consumer role (`profile`, `chsink`, `segments`, `journeys`,
  `dispatch`, `webhooks`, `integrations`) can run as many useful instances as its topics have
  partitions. Choose `PULSE_KAFKA_PARTITIONS` before the first `migrate`; adding partitions later
  moves contacts to other partitions, so do it only while the pipeline is idle.
- **`api` is stateless:** run several behind a load balancer. Sessions, sign-in throttling and
  rate limits live in Postgres and Redis; uploads and imports go to Postgres. Each instance caches
  API keys for up to a minute, so a replaced key stops working everywhere within a minute.
- **`journeys`** may run several instances: timers, scheduled broadcasts, background imports and
  A/B winners are claimed with row locks (`FOR UPDATE SKIP LOCKED`), so no step fires twice.
- Measured on one 12-core machine running everything: 10,000 events/s at p95 313 ms, and 20,000
  events/s sustained. Redis (single-threaded) is the next limit; see the
  [load test](../03-load-test.md).

### TLS and the reverse proxy

Pulse speaks plain HTTP; terminate TLS in front of it. Point **both** hostnames, the app
(`PULSE_PUBLIC_URL`) and the pages (`PULSE_PAGES_URL`), at the same Pulse (`api`) and keep the
original `Host` header: Pulse decides by host what to serve. With [Caddy](https://caddyserver.com),
which gets certificates by itself:

```caddyfile
pulse.example.com, pages.example.com {
    reverse_proxy 127.0.0.1:7070
}
```

With nginx (certificates from your own tooling):

```nginx
server {
    listen 443 ssl;
    server_name pulse.example.com pages.example.com;
    ssl_certificate     /etc/ssl/pulse/fullchain.pem;
    ssl_certificate_key /etc/ssl/pulse/privkey.pem;
    client_max_body_size 10g;          # background contact imports accept files up to 10 GB

    location / {
        proxy_pass http://127.0.0.1:7070;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 300s;
        proxy_request_buffering off;   # stream large imports instead of buffering them on disk
    }
}
```

Set `PULSE_TRUST_PROXY=true` only when Pulse can't be reached except through this proxy (the
`127.0.0.1:7070` binding above does that): Pulse then takes the client IP from the right-most
`X-Forwarded-For` entry, the one your proxy appended.

### Two origins: PUBLIC_URL and PAGES_URL

With `PULSE_PAGES_URL` set, the pages host serves only landing pages (`/p/`), asset downloads
(`/a/`), hosted forms (`/f/`), the web SDK (`/sdk/`), its API (`/v1/web/`) and `/healthz`; everything
else there is a 404. On the app host, those page paths redirect to the pages host. A landing page
someone writes in a workspace can then never read the app's cookies or pass for its sign-in page.
Use a separate domain or at least a separate subdomain that has no cookies of the app's.

### Workspaces when sign-up is off

With `PULSE_ALLOW_SIGNUP=false`, the operator creates each workspace with the admin token, then its
first admin with the workspace's API key:

```bash
curl -s -X POST https://pulse.example.com/v1/tenants \
  -H "Authorization: Bearer $PULSE_ADMIN_TOKEN" -d '{"name":"Acme Retail"}'
# → {"id":…,"slug":"acme-retail","api_key":"pk_…","site_key":"sk_…"}   (slug: the workspace ID)

KEY=pk_…   # shown once
curl -s -X POST https://pulse.example.com/v1/users -H "Authorization: Bearer $KEY" \
  -d '{"email":"owner@acme.example","name":"Asha Rao","role":"admin","password":"a temporary password"}'
```

Send the workspace ID, email and temporary password to the new admin privately; they can then add
their team (tutorial 1) and set up SSO. Keep the API key on the customer's servers or discard it;
it can be replaced in **Settings → Sign-in & keys**.

### GeoIP (optional)

To fill `geo_*` attributes (country, region, city) from visitors' IP addresses, download a MaxMind
**GeoLite2-City** (free licence key) or **GeoIP2-City** database, e.g. with `geoipupdate`, mount it
into the container and set `PULSE_GEOIP_DB` to its path (option A shows `/geoip/GeoLite2-City.mmdb`).
If the file can't be opened, the `api` role stops with `geoip database …`, so a typo is noticed at
once. MaxMind updates the database weekly: run `geoipupdate` on a schedule and restart `api` to
load the new file.

### Backups

| What | How | Why |
|---|---|---|
| Postgres | `pg_dump` nightly (or your provider's snapshots with point-in-time recovery) | Everything you configured, and every contact |
| ClickHouse | `BACKUP DATABASE pulse TO …` (to a backup disk or S3), or your provider's backups | The event history: reports, contact activity, behaviour segments |
| Redis | Keep AOF on (the dev stack runs `--appendonly yes`) and copy its data directory, or use your provider's snapshots | Live segment membership and event counters |
| `PULSE_SECRET_KEY` | In your secret store, separately from the database backups | Without it, encrypted credentials in a restored database can't be read |

Kafka holds events in flight; it doesn't need a backup of its own, but give its volume or cluster
enough retention (the broker's default is 7 days) to ride out an outage of a consumer role.

Test a restore now and then into a separate stack.

### Upgrades and migrations

Migrations are SQL files built into the binary (`migrations/`), applied in file-name order. Postgres
records each in `schema_migrations`, so it runs once; the ClickHouse statements are written to be safe
to repeat and run on every `migrate`.

1. Back up Postgres and ClickHouse.
2. Build or pull the new image.
3. **One server (`pulse all`):** `docker compose up -d` with the new image. Pulse runs the
   migrations on start, then serves.
4. **Separate roles:** run `pulse migrate` once (the `migrate` service above), wait for it to
   succeed, then restart the roles, `api` last. If several `migrate` (or `all`) processes start at
   once, a Postgres advisory lock makes them migrate one at a time; the later ones find nothing to do.

Events that arrive during the restart wait in Kafka and are processed when the roles are back.

### Dead-letter topics and replaying them

A provider outage doesn't need you: `dispatch` retries a message that failed temporarily (network
error, timeout, 429, 5xx) for about two hours (30 s, 1 min, 2 min, 5 min, 10 min, 30 min and 1 h
apart, longer if the provider sends `Retry-After`), then records `<channel>_failed` with the reason.

A record that a role can't handle at all after 4 quick tries (it can't be decoded, or Postgres or
Redis was down at that moment) goes to `<topic>.dlq`, with the error in its `error` header, so it
doesn't block the partition. Nothing replays these topics by itself: watch them (their size, or
alert on new records), fix the cause, then move the records back:

```bash
pulse replay-dlq --dry-run messages.email   # list what is in messages.email.dlq: offset, key, error
pulse replay-dlq messages.email             # re-publish everything to messages.email
pulse replay-dlq --limit 100 sms            # a channel name works too; replay at most 100 records
```

With Compose: `docker compose run --rm pulse replay-dlq …` (the same image and environment). The
command reads `<topic>.dlq` with the consumer group `pulse-replay-dlq`, re-publishes each batch
(same key, value and headers, without `error`) and commits it, and stops when the dead-letter topic
has had nothing new for 10 seconds. Running it again picks up only records that arrived since. A
record that fails again goes back to the `.dlq`. Replaying is safe for messages: one already sent
is skipped by its idempotency key.

---

## Troubleshooting

| Symptom | Check |
|---|---|
| `make up` fails with "port is already allocated" | Another program uses one of the [ports](#ports-used-on-your-machine). Change the host side of that `ports:` entry |
| Pulse keeps restarting | `make logs`. A data service isn't healthy (`make ps`), or a setting is wrong: `geoip database …` (bad `PULSE_GEOIP_DB` path), a Postgres/ClickHouse error (address or password) |
| `http://localhost:7080` doesn't answer right after `make up` | Pulse starts only once Kafka, ClickHouse, Postgres and Redis are healthy; that takes up to a minute on the first start |
| Signing in seems to work but you're sent back to the sign-in page | `PULSE_PUBLIC_URL` is `https://…` (so the cookie is `Secure`) but you opened Pulse over `http://`, or the reverse proxy changes the host. Use the public address |
| "too many attempts, try again later" when signing in | 10 attempts per workspace and email within 15 minutes; wait, or check the workspace ID |
| Emails don't arrive | Locally, look in Mailpit (`http://localhost:18025`). In production, check `PULSE_SMTP_ADDR` or the workspace's email provider (**Settings → Channels**, **Send test** on the provider), and the contact's consent |
| Log warns `PULSE_SECRET_KEY not set` | SSO and stored provider credentials are off, emails aren't tracked. Set the key and restart |
| SSO fails with an address error | The provider's discovery URL resolves to a private address. Use a public one, or set `PULSE_ALLOW_PRIVATE_SSO=true` for a provider on your internal network |
| Events are accepted (202) but contacts or segments don't change | A consumer role is down or behind: check every role's log, and the consumer groups' lag (`pulse-profile`, `pulse-segments`, `pulse-journeys`, …) with your Kafka tools |
| Landing pages or forms redirect to a host that doesn't answer | `PULSE_PAGES_URL` is set but its DNS or proxy isn't. Point the pages host at Pulse too |

Next: the configuration reference (coming), or start using Pulse with
[tutorial 1](../tutorials/01-getting-started.md).
