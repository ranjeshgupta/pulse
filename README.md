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
- **In-app campaigns (done):** `/v1/inapp` to manage them (with `GET /v1/inapp/check` and `POST /v1/inapp/{id}/test`),
  `POST /v1/web/inapp` for the app SDK (android, ios); the Flutter SDK renders modal, banner and full-screen messages.
  Segment targeting counts for signed-in users only with their user hash. (On-site focus items follow the workspace's
  identity-verification setting instead.)
- **Batch 5 (done):** GeoIP and the anonymous-tracking switch, retention cleanup (Settings → Data & privacy), journeys
  grouped into Actions/Conditions/Decisions/Waits with message-engagement decisions, and segment presets with date rules.
- **Batch 6, contacts (done):** lifecycle stages (Workspace → Stages; by hand, API, the `change_stage` journey step, or
  points thresholds that only move contacts forward), companies (Audience → Companies; attributes copied to contacts as
  `company_<key>`, linked by hand or by a server identify with `attrs.company`), and duplicate merge
  (`POST /v1/contacts/merge`, `GET /v1/contacts/duplicates`, Contacts → Duplicates and "Merge into…").
- **Batch 6, content (done):** every email carries a plain-text part (written in the template's Plain text tab, or
  generated from the HTML) and may attach up to 10 files (10 MB together); Engage → Assets holds uploaded files
  (25 MB each, kept in Postgres) downloadable at `/a/{key}`, openly or behind a form (`asset_downloaded` for the
  submitter); Engage → Landing pages publishes pages at `/p/{workspace}/{page}` with embedded forms, a schedule, a
  script-free preview and views/visitors/submissions from the web SDK's `landing_page_viewed`.
- **Rich text editor (done):** a CKEditor-style editor (TipTap) on every HTML field: email templates and landing pages
  (a Rich text tab beside drag and drop and HTML), focus items and web content, with merge tags and a Source view.
- **Batch 7, analytics (done):** cohorts / retention (`POST /v1/cohorts/run`, saved under `/v1/cohorts`), revenue
  attribution (`/v1/revenue/settings`, `POST /v1/revenue/report`: last touch, first touch or linear over broadcasts,
  journeys and UTM campaigns; recipients' revenue in broadcast and journey reports), UTM capture in the web SDK
  (`first_utm_*` / `last_utm_*` profile attributes, the session's campaign on `page_view`), funnel breakdowns
  (`breakdown: {attr}`), a custom report builder (`POST /v1/reports/run`, saved under `/v1/reports`) and automatic A/B
  winners on journey split steps (`winner: {metric, after_seconds, min_per_variant}`).
- **Batch 7b, segment builder (done):** one searchable, grouped "Add filter" picker (Contact, Company, Location, Device,
  Consent & deliverability, Behaviour, Points & stage, Segments & tags; every custom attribute listed with its type;
  recently used first; keyboard accessible), typed filter rows with suggested values, behaviour filters as sentences, and
  explicit nested "Match ALL / ANY of the following" groups. New data for Mautic's filters: last-seen device and locale
  from both SDKs, browsing sessions (`session_start`), `email_domain`, `date_added` / `date_identified` /
  `date_modified`, contact owners (`PUT /v1/contacts/{id}/owner`), SMS bounces (`sms_bounced`), manual vs self-service
  unsubscribes (`method` on `unsubscribed`), and SMS / RCS link click tracking (`/t/s/{code}`, `sms_clicked`). New rule
  operators: doesn't contain, starts / ends with, is not one of, is not set, between, on a date. Mautic filter mapping:
  [docs/05-feature-parity.md](docs/05-feature-parity.md#segment-builder-batch-7b).
- **Batch 7b, campaign builder (done):** "+" on every journey path asks Action, Decision, Condition, Wait or Split, then
  a searchable list with descriptions (disabled items say why); every Mautic campaign decision, action and condition is
  a step: form / asset / page / device / dynamic content decisions, opens / clicks (any or one link) / replies to email
  (inbound reply tracking: `reply_to` delivery setting, `POST /v1/replies/{token}`), marketing messages with channel
  fallback, email to users, show focus item and push dynamic content per contact, owner / company / company score,
  change campaigns, jump to step (25 jumps per enrollment at most), Do Not Contact, delete contact (typed confirmation),
  where deleting, removing DNC and owner / company steps need contacts edit access, and conditions on campaigns, form fields, notifications, valid email (MX), DNC, a date and page
  history. The canvas has Yes/No colours, step numbers, Tidy up, zoom to fit, minimap, undo/redo and per-step counts.
  Mapping: [docs/05-feature-parity.md](docs/05-feature-parity.md#campaign-builder-batch-7b).
- **Batch 8a, integrations and audience sync (done):** Settings → Integrations, a connector registry per workspace
  (credentials sealed with `PULSE_SECRET_KEY` and only ever shown masked, a status, last sync, a sync log, Test connection
  and previews):
  - **HubSpot** (private app token) and **Salesforce** (connected app, client credentials; Contact or Lead): contacts
    pulled on a schedule (15 minutes to a day) through a field mapping (remote field ↔ attribute, both ways, into Pulse or
    out to the CRM; reserved attributes can only be pushed), as `crm_synced` events keeping `hubspot_id` /
    `salesforce_id`; a contact is pushed on demand (`POST /v1/integrations/{id}/push`) or by the journey step
    **Push contact to integration** (`push_integration`; a failed push records `integration_push_failed` and the journey
    carries on).
  - **Shopify**: `POST /v1/webhooks/shopify/{id}` takes orders/create, customers/create and checkouts/create|update,
    verified with Shopify's HMAC, as `order_completed` (amount, revenue, currency, items: set Analyze → Revenue to
    `order_completed` / `amount`), `customer_created` and `cart_updated` (abandoned carts) on the contact with that email
    or phone.
  - **Zapier / Make** (REST hooks): the connector has its own API key (`pzk_…`); `/v1/hooks/subscriptions` subscribes a
    URL to `contact_created`, `segment_entered` / `segment_exited` (optionally one segment), `form_submitted` (optionally
    one form) or any event, delivered like outbound webhooks (signed, retried, a 410 unsubscribes); actions
    `/v1/hooks/contacts`, `/v1/hooks/events`, `/v1/hooks/tags`.
  - **Audience sync**: link a segment to a Facebook Custom Audience or a Google Ads Customer Match list (existing, or
    created by Pulse). Members go up as SHA-256 hashes of the normalised email and phone (E.164), in batches; entries and
    exits stream from `segment.membership`, consent and address changes from `events.enriched`, with a full reconcile
    every `PULSE_AUDIENCE_RECONCILE_INTERVAL`. Contacts unsubscribed from everything are never uploaded. Linking needs
    settings and segments edit access.
  Every remote base URL is a setting (the real one by default), and every call goes through the SSRF-guarded client.
- **Still to build:** the rest of batch 8 (native SDKs, RCS replies, bounce mailbox, push click tracking, form uploads,
  audit export), then batch 9 in the feature-parity doc.

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

## Reserved attribute names

Pulse maintains some profile attributes itself, so they can't be set directly by browsers, forms, journey "update
profile" steps, server events or CSV imports (they are dropped there): `tags`, `points`, `stage`, `stage_changed_at`,
`company_id`, `company_name` and every `company_*` key, `first_seen`, `last_seen`, `date_added`, `date_identified`,
`date_modified`, `email_domain`, `owner_id`, `owner_name`, the last-seen device (`device_type`, `device_os`,
`device_os_version`, `device_brand`, `device_model`, `browser`, `app_version`: from the SDKs' event properties),
`merged_into`, `email_bounced` and `sms_bounced` (a server may clear them; a new phone number clears `sms_bounced`), and
the `optout_*` consent flags. `locale` is filled from the SDKs but may be set by your server. `first_utm_*` and `first_referrer` (first-touch attribution, set by the
web SDK) can be set but keep their first value. Change them through their own APIs and events: tags, points, stages
(`PUT /v1/contacts/{id}/stage`, journey step `change_stage`), owners (`PUT /v1/contacts/{id}/owner`), companies (`PUT /v1/contacts/{id}/company`, or an identify
with `attrs.company` from your server), consent (`unsubscribed` / `resubscribed`). Name your own fields differently, e.g.
`team_size` rather than `company_size`.

## Security settings for production

| Variable | Default | Meaning |
|---|---|---|
| `PULSE_SECRET_KEY` | none | 64 hex characters; encrypts stored secrets, SSO state, tracking links, webhook secrets and download links (form-gated assets, attachment links for the custom gateway need it) |
| `PULSE_TRUST_PROXY` | `false` | Take the client IP (for rate limits and the audit log) from the right-most `X-Forwarded-For` entry. Turn on only behind your own reverse proxy |
| `PULSE_ALLOW_PRIVATE_SSO` | `false` | Let SSO discovery reach private addresses (an IdP on your internal network) |
| `PULSE_ALLOW_PRIVATE_WEBHOOKS` | `false` | Let journey webhooks, outbound webhooks, the custom gateway and integrations (CRM, Shopify, ad platform APIs) call private addresses (development only) |
| `PULSE_AUDIENCE_RECONCILE_INTERVAL` | `24h` | How often each ad audience sync re-checks its whole segment (entries and exits stream in between) |
| `PULSE_GEOIP_DB` | none | Path to a MaxMind GeoLite2-City or GeoIP2-City `.mmdb` file (download with a free MaxMind licence key, e.g. via `geoipupdate`); unset turns geolocation off |
| `PULSE_MAINTENANCE_INTERVAL` | `1h` | How often retention cleanup runs |
| `PULSE_PAGES_URL` | none | A separate origin for landing pages, asset downloads and hosted forms, e.g. `https://pages.example.com`. **Set it in production**: workspace-authored pages then never share an origin or cookies with the app (no lookalike login pages on the app's domain); those paths redirect there, and that host serves nothing else |

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
cmd/pulse/          one binary, one role per argument (migrate, api, profile, chsink, segments, journeys, dispatch,
                    webhooks, integrations, all)
internal/segment/   rule AST, Go evaluator, ClickHouse SQL compiler, Redis bitmaps and day counters
internal/segengine/ streaming membership, backfill, sweep
internal/journey/   graph validation, executor, triggers and durable timers
internal/dispatch/  email: idempotency, token bucket, templates, SMTP
internal/profile/   identity resolution, attribute merge
internal/chsink/    events.enriched -> ClickHouse
internal/integrations/ CRM, Shopify, REST hook and ad audience connectors; audience sync
internal/api/       HTTP API
internal/store/     Postgres and ClickHouse stores
migrations/         embedded SQL
test/e2e/           end-to-end tests against the running stack
```

## Known Phase 1 limits

- Journeys don't exit contacts when they leave the trigger segment (use a condition, a goal or "Change campaigns").
- A backfill that runs while live events arrive for the same segment can overwrite a fresh streaming
  change. The next sweep fixes it.
- Postgres Row-Level Security is not enabled yet. Tenant isolation is enforced in every query (and tested at the API level).
- Measured on one 12-core machine running everything: 10k events/s at p95 313 ms and 20k events/s sustained.
  Redis (single-threaded) and the per-poll barrier are the next limits. See [docs/03-load-test.md](docs/03-load-test.md).
