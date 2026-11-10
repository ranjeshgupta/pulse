# Pulse

Real-time, multi-tenant marketing automation in Go on Kafka, ClickHouse, Postgres and Redis.
It aims for Mautic/WebEngage features at Listmonk speed: events move contacts into segments
and journeys within seconds, not on a 15-minute cron.

- [docs/01-review.md](docs/01-review.md): what Mautic and Listmonk do, and why Mautic is slow
- [docs/02-architecture.md](docs/02-architecture.md): pipeline, topics, segment and journey design
- [docs/03-load-test.md](docs/03-load-test.md): measured throughput and latency, bottlenecks fixed
- [docs/04-phase2-plan.md](docs/04-phase2-plan.md): web app, sign-in, channels, on-site
- [docs/05-feature-parity.md](docs/05-feature-parity.md): Mautic + WebEngage feature check, and what's left

## What's built

- **Core (Phase 1):** event ingestion, identity, real-time segments (streaming, backfill, sweep), journeys with durable
  waits, and the ClickHouse event log.
- **Phase 2:**
  - Web app (React + TypeScript, embedded in the Go binary).
  - Sign-in with passwords and workspace SSO (OIDC; tested against Keycloak).
  - Channels: email, SMS, WhatsApp and RCS with SMS fallback, and FCM push. Each has a preview with a real contact,
    test sends, and provider connection tests.
  - Scheduled journeys, broadcasts and on-site content: start required, end optional (empty means it never ends),
    broadcasts repeating daily, weekly or monthly.
  - Focus items and Dynamic Web Content, through a public decision API.
- **P0 (done):** per-channel consent honored by every send; email open and click tracking; unsubscribe links and RFC 8058
  one-click; CSV import and export; the web SDK (`/sdk/pulse.js`: track, identify, page views, focus items, content slots,
  push tokens). Install snippet: Sign-in & keys → Your website. Test page: `/sdk/demo.html?key=<site key>`.
- **P1 batches 1-3 (done):** journey power steps, frequency caps and quiet hours; lists, tags, scoring, identity merge;
  forms (hosted `/f/{key}` and `data-pulse-form`), preference center, STOP/START replies, broadcast A/B tests,
  broadcast and journey reports, funnels.
- **P1 batch 4 (done):** custom roles, audit log, outbound webhooks (`webhooks` role), background imports, bounce and
  complaint webhooks, standard web push, a custom HTTP gateway for any channel, and the Flutter SDK
  ([sdks/flutter/pulse_sdk](sdks/flutter/pulse_sdk)).
- **App inbox (done):** an `inbox` channel kept by Pulse itself; journeys (`send_inbox`) and broadcasts send to it, the
  SDKs list and update it (`GET/POST /v1/web/inbox`, `Pulse.inbox`), reads and clicks count in reports.
  A signed-in user's inbox always needs their user hash (identity verification); anonymous visitors' doesn't.
- **In-app campaigns (backend done):** `/v1/inapp` to manage them, `POST /v1/web/inapp` for the app SDK (android, ios).
- **Still to build:** in-app rendering in the Flutter SDK and the editor page, then batch 5 and P2 in the feature-parity doc.

## Run it

Only Docker is required. Go runs inside the `golang` image when it isn't installed.

```bash
make up        # Kafka, ClickHouse, Postgres, Redis, Mailpit, Keycloak + pulse (all roles, web app included)
make seed      # demo workspace with data (credentials: deploy/dev-seed.md)
make test      # Go unit tests (no infrastructure)
make it        # SQL-vs-Go segment agreement test against ClickHouse (needs `make up`)
make e2e       # end-to-end (needs `make up`); webhook tests run in a container on the compose network
make load ARGS="-rate 10000 -duration 45s"   # load test; results in docs/03-load-test.md
cd web && npm install && npm run dev && npm test   # web app on :5173 (proxies the API), and its unit tests
```

| Service | URL |
|---|---|
| Pulse web app and API | http://localhost:7080 |
| Mailpit (sent mail) | http://localhost:18025 |
| Keycloak (dev SSO) | http://localhost:8180 |
| ClickHouse HTTP | http://localhost:18123 (user/password `pulse`) |

## Security settings for production

| Variable | Default | Meaning |
|---|---|---|
| `PULSE_SECRET_KEY` | none | 64 hex characters; encrypts stored secrets, SSO state, tracking links and webhook secrets |
| `PULSE_TRUST_PROXY` | `false` | Take the client IP (for rate limits and the audit log) from the right-most `X-Forwarded-For` entry. Turn on only behind your own reverse proxy |
| `PULSE_ALLOW_PRIVATE_SSO` | `false` | Let SSO discovery reach private addresses (an IdP on your internal network) |
| `PULSE_ALLOW_PRIVATE_WEBHOOKS` | `false` | Let journey webhooks, outbound webhooks and the custom gateway call private addresses (development only) |

The dev compose file turns the two `ALLOW_PRIVATE` switches on for the local Keycloak and the e2e webhook receivers.

## API walkthrough

```bash
# 1. Create a tenant (admin token from deploy/docker-compose.yml) and keep its API key
curl -s -X POST localhost:7080/v1/tenants -H 'Authorization: Bearer dev-admin-token' -d '{"name":"Acme"}'
KEY=pk_...

# 2. Template, segment, journey
curl -s -X POST localhost:7080/v1/templates -H "Authorization: Bearer $KEY" \
  -d '{"name":"welcome","subject":"Welcome {{.Attrs.first_name}}","html":"<p>Hi {{.Attrs.first_name}}</p>"}'
curl -s -X POST localhost:7080/v1/segments -H "Authorization: Bearer $KEY" \
  -d '{"name":"Delhi buyers","rule":{"all":[{"attr":"city","op":"eq","value":"Delhi"},{"event":"purchase","op":"gte","count":1,"days":7}]}}'
curl -s -X POST localhost:7080/v1/journeys -H "Authorization: Bearer $KEY" \
  -d '{"name":"welcome","graph":{"trigger":{"type":"segment_entered","segment_id":1},"entry":"send",
       "nodes":{"send":{"type":"send_email","template_id":1,"next":"done"},"done":{"type":"exit"}}}}'
curl -s -X POST localhost:7080/v1/journeys/1/activate -H "Authorization: Bearer $KEY"

# 3. Send events. The email shows up in Mailpit within a few seconds.
curl -s -X POST localhost:7080/v1/events -H "Authorization: Bearer $KEY" -d '{"events":[
  {"external_id":"u1","name":"identify","email":"u1@example.com","attrs":{"city":"Delhi","first_name":"Asha"}},
  {"external_id":"u1","name":"purchase","props":{"amount":499}}]}'
curl -s localhost:7080/v1/contacts/u1 -H "Authorization: Bearer $KEY"   # profile + segment ids
```

## Layout

```
cmd/pulse/          one binary, one role per argument (migrate, api, profile, chsink, segments, journeys, dispatch, all)
internal/segment/   rule AST, Go evaluator, ClickHouse SQL compiler, Redis bitmaps and day counters
internal/segengine/ streaming membership, backfill, sweep
internal/journey/   graph validation, executor, triggers and durable timers
internal/dispatch/  email: idempotency, token bucket, templates, SMTP
internal/profile/   identity resolution, attribute merge
internal/chsink/    events.enriched -> ClickHouse
internal/api/       HTTP API
internal/store/     Postgres and ClickHouse stores
migrations/         embedded SQL
test/e2e/           end-to-end tests against the running stack
```

## Known Phase 1 limits

- Journeys are enter-once per contact. Exit-on-segment-exit, A/B splits and wait-for-event come later.
- A backfill that runs while live events arrive for the same segment can overwrite a fresh streaming
  change. The next sweep fixes it.
- Postgres Row-Level Security is not enabled yet. Tenant isolation is enforced in every query (and tested at the API level).
- Measured on one 12-core machine running everything: 10k events/s at p95 313 ms and 20k events/s sustained.
  Redis (single-threaded) and the per-poll barrier are the next limits. See [docs/03-load-test.md](docs/03-load-test.md).
