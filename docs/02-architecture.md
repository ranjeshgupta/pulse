# Pulse architecture

Pulse is a multi-tenant, real-time marketing automation platform written in Go on Kafka, ClickHouse, Postgres and Redis.
The design targets 10–100M contacts and 10k–100k events/sec. This document covers the overall design and the
Phase 1 contracts that the code in this repository implements.

## Pipeline

```
 client / SDK ──POST /v1/events──► ingest ──► [events.raw]  key = tenant:external_id
                                                 │
                                              profile  resolve external_id → dense contact id,
                                                 │     merge attributes into Postgres, bump Redis day counters
                                                 ▼
                                          [events.enriched]  key = tenant:contact_id
                                         ┌───────┴────────┐
                                      chsink           segments  re-evaluate only segments that reference
                                   (ClickHouse:        │          this event name or attribute; flip Redis bit
                                  events, contacts)    ▼
                                              [segment.membership]  entered / exited
                                                       │
                                                    journeys  enroll, run nodes, durable Postgres timers
                                                       ▼
                                               [messages.email]
                                                       │
                                                   dispatch  rate limit, idempotency, render, SMTP / SES
                                                       └──► email_sent event back into [events.raw]
```

All roles ship in one binary, `pulse <role>`. Each role is a separate deployment that scales on its own Kafka consumer group,
and `pulse all` runs every role in one process for local development.

## Kafka topics

| Topic | Key | Producer → consumer | Payload |
|---|---|---|---|
| `events.raw` | `tenant:external_id` | ingest, dispatch → profile | `event.Event` without `contact_id` |
| `events.enriched` | `tenant:contact_id` | profile → chsink, segments, journeys | `event.Event` with `contact_id` and the merged `attrs` snapshot |
| `segment.membership` | `tenant:contact_id` | segments → journeys | `segment.Change{tenant, segment, contact, entered, source}` |
| `messages.email` | `tenant:contact_id` | journeys → dispatch | `message.Email{id, tenant, contact, template, ...}` |

Each key keeps one contact's events in order on a single partition. That ordering is what lets the streaming segment path work without locks.

## Identity and tenancy

- `tenants(id, name, api_key_hash)`. Every API call resolves a tenant from `Authorization: Bearer <api key>`.
- `contacts(tenant_id, id, external_id, email, attrs jsonb)`. `id` is **dense per tenant** (1, 2, 3…), allocated from
  `tenant_counters` with `UPDATE … RETURNING`. Dense ids let a segment be a Redis bitmap: 100M contacts = 12.5 MB per segment.
- `tenant_id` is the first column of every Postgres primary key, every ClickHouse `ORDER BY` and every Redis key.
  Repository functions always take `tenantID`. Postgres Row-Level Security is planned for Phase 6.

## Profiles and reserved attributes

The profile role (`internal/profile`) folds each contact's events, in order, into its attribute map. Most attributes are
whatever events carry, but some are maintained by Pulse and change through their events, so every change is ordered
with the contact's other events, shows on the timeline and moves segments and journeys at once. Browsers (site key) can't
set any of them or send their events (`consent.IsReserved`, `event.ServerOnly`). Stage, company and `merged_into`
attributes can't be set by attributes on any event, even from a server (`event.EventOnlyAttr`); servers may still set
consent, tags and points directly (imports).

| Attributes | Changed by (events) | Notes |
|---|---|---|
| `optout_<channel>`, `optout_all` | `unsubscribed`, `resubscribed` | per-channel consent |
| `tags`, `points` | `tag_added`, `tag_removed`, `points_changed`, scoring rules | server imports may also set them |
| `first_seen`, `last_seen` | every event | RFC 3339 time / UTC date |
| `stage`, `stage_changed_at` | `stage_changed {stage}` | from `PUT /v1/contacts/{id}/stage`, `POST /v1/events` (stage checked against the workspace's stages), the `change_stage` journey step, or a points threshold |
| `company_id`, `company_name`, `company_<key>` | `company_linked`, `company_updated`, `company_unlinked` | copies of the linked company's attributes, so segment rules on them work in both compilers |
| `merged_into` | `contact_merged_into` | the contact was merged into another; it leaves every segment |
| `date_added`, `date_identified`, `date_modified` | every event | RFC 3339 time the contact was created (contacts created before this get it at their next event, from `created_at`); RFC 3339 time of the first event with a non-`anon-` id or an email; UTC date of the latest change to any other attribute (a day, so at most one extra write per contact per day) |
| `email_domain` | any event that sets `email` | lower-case part after `@`; removed when the email is cleared |
| `device_type`, `device_os`, `device_os_version`, `device_brand`, `device_model`, `browser`, `app_version` | `identify`, `page_view`, `screen_view`, `session_start` | last-seen device from the event's flat props (`device_type`, `os`, `os_version`, `device_brand`, `device_model`, `browser`, `app_version`, each at most 100 characters). An event naming a device (`device_type` or `os`) replaces the whole device, so a phone's model never sticks to a desktop visit |
| `locale` | the same events (`props.locale`) | not reserved: a server or import may set it, and an event's own `attrs.locale` wins over its props |
| `owner_id`, `owner_name` | `owner_changed {owner_id, owner_name}` | only from `PUT /v1/contacts/{external_id}/owner {"user_id": N}` (0 unassigns); the user must belong to the workspace |
| `sms_bounced` | `sms_failed` with `props.bounce` (attrs from the dispatcher) | like `email_bounced`: SMS to the contact is suppressed; sticky on merge; a server may clear it, and a new `phone` clears it |

- **Unsubscribe method.** The profile role adds `props.method` to every `unsubscribed` event that lacks a valid one:
  `self` for the contact's own actions (`source` email_link, one_click, preference_center, form, complaint, `*_keyword`),
  `manual` for everything else (the app's consent panel sends `pulse_app`, API calls without a source, imports). Segments
  use it as an event property ("Unsubscribed manually: email" = `optout_email` is yes AND an `unsubscribed` event with
  channel email and method manual in the last 365 days).
- **Device and sessions from the SDKs.** The web SDK parses the User-Agent (refined by User-Agent Client Hints where the
  browser has them; the model and exact OS version are requested once per browser session and used once they arrive)
  and sends `platform: web`, `device_type`, `os`, `os_version`, `browser`, `device_brand`, `device_model` and `locale`
  on `identify`, `page_view` and `session_start`; custom `track()` events don't carry them. A session ends after 30
  minutes without any event from the browser (shared by its tabs, `pulse.sid` / `pulse.sla` in localStorage); each new one
  starts with `session_start {session_id, url, path, referrer, device props, the visit's utm_* / referrer_domain}`, and
  page views carry `session_id`. The Flutter SDK sends `platform` (android / ios), `device_type` (tablet when the
  shortest side is ≥ 600 logical pixels), `os`, `os_version`, `locale`, and `app_version` / `device_model` /
  `device_brand` from `PulseConfig` (no plugin dependency); it starts a session at the first event and after 30 minutes
  idle or in the background. "Has X browsing sessions" is an ordinary event-count rule on `session_start`.

- **Stages.** A workspace orders its stages (`stages` table); a stage with `min_points` is a threshold. When a points
  change crosses a threshold upward, the profile role moves the contact to the furthest stage whose threshold the new
  points reach, only if that stage comes after the current one, and emits a `stage_changed` event (props
  `source: points`) right after the scoring event. Stage definitions are cached with the scoring rules and reloaded when
  `scoring:v` is bumped. Renaming a stage fans out `stage_changed {rename: true}` to its contacts, which changes the name
  but keeps `stage_changed_at`; a deleted stage stays on its contacts until they move.
- **Companies.** `companies(tenant_id, name, domain, attrs)`; names are unique ignoring case. Linking publishes
  `company_linked {company_id, name, attrs}`, which sets `company_id`, `company_name` and `company_<key>` and removes the
  copies of a previous company. A server identify with `attrs.company` (a name) is followed by a `company_linked` event
  (the company is created when new); the browser path never does this. Updating a company fans out `company_updated`
  to its contacts in pages of 2,000 in the background, and deleting it fans out `company_unlinked`; both apply only to
  contacts still linked to that company when the event is processed.
- **Merges.** An identify with `props.anonymous_id` (browsers: `anon-` ids only), or `merge_requested {loser}` from
  `POST /v1/contacts/merge`, folds the other contact into this one: its attributes fill gaps (the winner's values win,
  tags are combined, points added, first/last seen widened, a winner with a company keeps only its own company's copies),
  its Redis counters move, `contact_merged` makes the sink copy its ClickHouse history, and `contact_merged_into` marks it
  `merged_into` the winner. The API adds `list_joined` events for the loser's static lists after the merge request, and a
  short Redis reservation stops two merges of the same contacts from racing on different partitions. Later events of a
  merged contact (and of contacts merged into it before) follow `merged_into` to the winner.

## Segment rules

Segments are a JSON AST:

```json
{ "all": [
    { "attr": "city", "op": "eq", "value": "Delhi" },
    { "event": "purchase", "op": "gte", "count": 3, "days": 7 },
    { "any": [ { "attr": "plan", "op": "in", "value": ["pro", "team"] },
               { "event": "login", "op": "eq", "count": 0, "days": 30 } ] }
] }
```

- Groups: `all` and `any`, nestable (8 levels, 200 nodes). Attribute ops: see the second "Segment rules" section below. Event ops: `eq ne gt gte lt lte` on `count`.
- **Window semantics:** `days: N` means *today plus the previous N−1 calendar days, in UTC*. Both compilers use this definition.
- Compiler 1, **`segment.Eval`**: a Go predicate over (attrs, counter reader). It's used per event by the streaming path.
- Compiler 2, **`segment.ToSQL`**: ClickHouse SQL with bound parameters, which returns matching contact ids. It's used by backfill and sweeps.
- `segment.Refs` lists the event names and attribute keys a rule reads. The segment engine keeps an inverted index from refs to segments, so an
  event only triggers evaluation of the segments that could change.

### Streaming path (per event)
1. Load the candidate segments from the index for (tenant, event name) and for (tenant, the attribute keys this event changed).
2. Run `Eval` for that one contact. Event counts come from the Redis hash `cnt:{t}:{c}:{event}`, keyed by `yyyymmdd`.
3. Compare the result with `GETBIT seg:{t}:{s} c`. If it changed, `SETBIT` and publish `segment.membership`.

### Batch path (create, edit, sweep)
1. Run `ToSQL` in ClickHouse to get a set of ids. Build the bitmap bytes in Go, in Redis bit order (bit 0 = MSB of byte 0).
2. XOR it with the current bitmap to get entered and exited contacts. Write it back atomically (`SET tmp` + `RENAME`), then publish the diffs
   with `source=backfill` or `source=sweep`.
3. Segments that contain event windows are swept every `SWEEP_INTERVAL` (default 5 minutes), so "within N days" membership decays
   without a rescan in the event path.

Known Phase 1 limitation: an event that arrives for a contact while a backfill of the same segment is running can be overwritten by the
`RENAME`. The next sweep corrects it.

## Journeys

A journey is a trigger plus a graph of nodes, stored as JSON:

```json
{ "trigger": { "type": "segment_entered", "segment_id": 4 },
  "entry": "w1",
  "nodes": {
    "w1": { "type": "wait",      "seconds": 3600, "next": "c1" },
    "c1": { "type": "condition", "segment_id": 9,  "yes": "e1", "no": "x" },
    "e1": { "type": "send_email","template_id": 2, "next": "x" },
    "x":  { "type": "exit" } } }
```

- Triggers: `segment_entered` (optionally ignoring backfill) and `event` (by name).
- Nodes (see `internal/journey/graph.go`):
  - Actions: `send_email`, `send_sms`, `send_whatsapp` and `send_rcs` (optional SMS fallback), `send_push`, `send_inbox`,
    `update_attr`, `update_tags`, `adjust_points`, `update_list`, `change_stage` (a stage of the workspace), `webhook`.
  - Conditions: `condition` (segment or list membership), `check` (any segment rule, evaluated now), `split` (A/B/n by weight).
  - Waits and decisions: `wait` (a duration), `wait_until` (a date, or a time of day in the contact's zone),
    `wait_for_event` (yes when the event happens, no at the timeout), `message_decision` (yes when an earlier send step's own
    message is opened, clicked, delivered or read, no at the timeout; matched on the message id `{enrollment}:{step}`); `exit`.
  - Journey settings: trigger (`segment_entered`, `event`), re-entry (`once`, `always`, `after_days`), goal event, schedule window.
- Entering and finishing a journey record `journey_entered` and `journey_completed` events (props `journey_id`), so segments can
  select contacts by journey with ordinary event rules.
- `journey_enrollments(tenant_id, journey_id, contact_id, node, status)` keeps one live enrollment per (journey, contact); re-entry
  rules decide when a contact may enter again.
- Waits insert a row into `journey_timers(due_at, …)`. A poller claims due rows with `FOR UPDATE SKIP LOCKED` and advances them. Any number of
  journey workers can run without double-firing a step.
- Each send carries the idempotency key `{enrollment}:{node}`. The dispatcher drops duplicates with Redis `SET NX`.

## Segment rules
- Rule types: attribute comparisons (`eq`, `ne`, `contains`, `not_contains`, `starts_with`, `ends_with`, `in`, `not_in`,
  `exists`, `not_exists`, `gt`/`gte`/`lt`/`lte`, `between` [low, high], and dates: `on`, `before`, `after`, `within_days`,
  `older_than_days`), event counts in a window with property filters (the same ops except the relative dates), tags,
  and membership of another segment or list. A missing attribute reads as "" in both compilers, so "is empty" is `eq ""`.
- The web builder (`web/src/segmentRules.ts` editing model and `treeReducer`, `web/src/segmentCatalog.ts` filter
  catalog, `web/src/pages/SegmentBuilder.tsx` and `FilterPicker.tsx`) is described in the parity doc's "Segment builder".
  Value suggestions come from `GET /v1/attributes/{key}/values` and `GET /v1/events/{name}/values?prop=` (top 25
  values of a 200k-row ClickHouse sample, cached 10 minutes in Redis).
- Every profile keeps `first_seen` (first event time) and `last_seen` (UTC date of the latest event) for recency rules.
- Rules that depend on today (event windows, relative dates) are swept periodically; the Go evaluator and the ClickHouse compiler
  must agree on every rule (`make it`).

## Dispatch
- Per-tenant token bucket in Redis (`EMAIL_RATE_PER_SEC`).
- **SMS bounces.** A provider error for the number itself (`providers.BadNumber`: Twilio 21211/21214/21612/21614, or
  an unreachable recipient, e.g. the sandbox's unreachable suffix) or a failed receipt marked unreachable (Twilio
  30003/30005/30006) records `sms_failed {bounce: true}` with `attrs.sms_bounced = true`; later SMS are
  `sms_suppressed` until the number changes. A contact without a valid number is a plain failure, not a bounce.
- **SMS / RCS click tracking.** With `content.track_links` on the template (Templates → SMS or RCS, "Track link
  clicks"), the dispatcher replaces each http(s) link in the rendered text (and RCS `url` suggestions) with
  `{PULSE_PUBLIC_URL}/t/s/{code}`: 10 random base62 characters naming the message plus one naming the link (about 40
  characters in all; trailing punctuation stays outside the link). The message's tracking token and destinations are one
  Redis key per message (`slink:{code}`, 30 days). `GET /t/s/{code}` records `<channel>_clicked {url, message_id,
  template_id, journey_id, broadcast_id…}` once per message and link, then redirects (302); unknown or expired codes get
  404. WhatsApp is not covered: business-started WhatsApp messages are pre-approved templates whose button URLs live at
  Meta, so Pulse can't rewrite them.
- Templates use Go `html/template` and `text/template` over `{contact, attrs}`.
- Providers sit behind the `channel.Sender` interface: SMTP in Phase 1, then SES, Twilio, Gupshup, MSG91, Google RBM, FCM/APNs and VAPID.
- Every send emits an `email_sent` or `email_failed` event back into `events.raw`, so journeys and segments can react to delivery.

## Content: emails, assets and landing pages

- **Email parts.** `channel.RenderFor` renders the HTML and the plain-text part (`content.text`, a text template); when the
  template has no text, `channel.TextFromHTML` generates it from the rendered HTML (links become "text (url)", lists get
  dashes, entities decoded, whitespace collapsed). The dispatcher tracks the HTML only: text links stay direct (no click
  redirect, no pixel), and the unsubscribe link is appended to the text unless it already has it. The SMTP adapter
  (`providers.BuildMessage`) writes `multipart/alternative` (text, then HTML, quoted-printable), inside
  `multipart/mixed` with base64 attachments when the template lists any (`content.attachments`: asset ids, at most 10,
  10 MB together, checked on save and in the preview). The dispatcher caches attachment bytes per asset (assets never
  change their file). The custom HTTP gateway gets `text` and `attachments: [{filename, content_type, size, url}]` with
  signed download links valid 7 days rather than inline bytes, so requests stay small.
- **Assets.** Files live in Postgres (`assets` for metadata, `asset_files` for the bytes, stored out of line, 25 MB each,
  2 GB per workspace) rather than a disk directory, so every API instance serves every file with no shared volume, backup
  is the database's, and there is no path to traverse. Uploads are accepted by extension from an allowlist (PDF, PNG,
  JPEG, GIF, WebP, SVG, CSV, TXT, ICS, ZIP, DOCX, XLSX, PPTX); binary types must start with their format's signature and
  text types must be UTF-8. Every download is `Content-Disposition: attachment` with the stored type, `nosniff` and
  `Content-Security-Policy: default-src 'none'; sandbox`, so SVG or anything else is never rendered on Pulse's origin.
- **Gated downloads.** An asset with `form_id` shows that form at `/a/{key}`; a valid submission (the forms pipeline:
  rate limit, honeypot, fill time, `form_submitted` with `asset_id`) gets a link `/a/{key}/download?t=` sealed with
  AES-GCM (purpose, asset, workspace, submitter, 15-minute expiry); following it records `asset_downloaded {asset_id}` for
  the submitter (one event per link). A gated asset whose form is deleted or a draft isn't served (never opened up).
  Open downloads count in `download_count` only: a plain link carries no identity.
- **Landing pages** are served at `/p/{workspace slug}/{page slug}` while published and inside their window. Authored
  HTML is trusted but never runs: `<script>`, inline handlers, `javascript:` URLs, `<base>` and `<meta http-equiv>` are
  removed, and the CSP allows only scripts carrying the response's nonce: `/sdk/pulse.js` and `/sdk/lp.js` (the
  bootstrap, configured by data attributes). Pages may be framed by any site. `lp.js` starts the SDK with the
  workspace's site key and sends `landing_page_viewed {page_id, utm_*}`; the SDK renders embedded forms (`{form:KEY}`),
  so a submission merges the visitor's anonymous views into the contact. Submissions whose page URL is one of the
  workspace's pages carry `landing_page_id`. Stats (views, unique visitors, submissions) come from ClickHouse; anonymous
  visitors count only when the workspace tracks anonymous visitors. The app's preview (`POST /v1/landing-pages/preview`,
  signed-in, content view) renders unsaved HTML without scripts (forms drawn statically) into a sandboxed `srcdoc` frame.

### Public routes

| Route | What | Limits |
|---|---|---|
| `GET /f/{key}`, `POST /f/{key}` | hosted form | 10 submissions/min per IP and workspace |
| `GET /a/{key}`, `POST /a/{key}` | asset download, or its gate form | 60 downloads/min per IP and workspace; form limits |
| `GET /a/{key}/download?t=` | sealed 15-minute (gate) or 7-day (gateway attachment) link | same |
| `GET /p/{workspace}/{page}` | landing page | 300 views/min per IP and workspace |
| `/v1/web/*` | web SDK API (site key) | 600 events/min per IP and workspace |
| `/t/*`, `/u/*` | email tracking and unsubscribe (sealed tokens) | |

## ClickHouse tables
- `events`: `MergeTree PARTITION BY toYYYYMM(ts) ORDER BY (tenant_id, name, contact_id, ts)`. Event-window segment queries hit only the
  key range for (tenant, event name).
- `contacts`: `ReplacingMergeTree(version) ORDER BY (tenant_id, id)` with `attrs Map(String, String)`. Queried with `FINAL`.

## Public pages and origins
- Landing pages (`/p/`), asset downloads (`/a/`) and hosted forms (`/f/`) are workspace-authored. With `PULSE_PAGES_URL` set they are
  served only from that origin (app-host requests redirect with 308), and that origin serves nothing but those pages, the SDK
  (`/sdk/`) and the browser API (`/v1/web/`). Session cookies are host-only, so pages never see them.
- Landing page HTML is sanitized by parsing it with the HTML5 algorithm (`golang.org/x/net/html`) and serializing the tree:
  scripts (HTML and SVG), event handlers, `srcdoc`, `<base>`, `<meta http-equiv>`, `<object>`, `<embed>` and javascript:/vbscript:/
  non-image data: URLs are removed. A nonce CSP allows only the Pulse SDK scripts.
- On Pulse-hosted pages the SDK namespaces its stored ids by site key (`storagePrefix`), so workspaces sharing the pages origin
  never share a visitor's identity.

## Analytics (batch 7)

All report SQL is compiled in `internal/analytics` (cohorts, attribution, custom reports, A/B test stats) and
`internal/segment` (funnels and breakdowns), with every user value bound as an argument and keys checked with
`attr.ValidKey` / `event.ValidName`; only fixed fragments chosen by validated enums and computed integers are spliced in.
Event queries name their events and bound `ts`, so they read the `(tenant_id, name, …)` key range of the partitions in range;
ranges are a year at most. Counts that come from joins of rows already unique per contact are plain `count()`; report-builder
counts use `uniq` (approximate above 65k, exact below); funnels, cohorts' inputs and attributed purchases stay exact.

- **Cohorts.** Members are `(contact, cohort start)`: from the contacts snapshot's `first_seen`, or the first time in the range
  a contact did an event. Return activity is `(contact, period start)` for the return event (or any event except Pulse's own
  sends, outcomes and bookkeeping), joined with the members (the hash side) and bucketed by calendar period (UTC; weeks start
  Monday). At most 60 cohorts and 60 days / 26 weeks / 12 months followed; periods that haven't started are `null`.
- **Revenue attribution.** A workspace names its revenue event and amount property (`tenant_settings.revenue`, default
  `purchase` / `amount`; the currency is a display code). Touches are message opens and clicks of broadcasts and journeys
  (`message_events`), and page views carrying a UTM campaign (the web SDK adds the session's `utm_*` to `page_view`, and
  `landing_page_viewed` has them). For each purchase in the range ClickHouse summarizes the same contact's touches in the
  N days before it (window 1-90 days): the first, the last and the distinct sources. Go applies the model: last touch (all
  credit to the latest), first touch (the earliest in the window) or linear (an equal share per distinct source).
  Messages and UTM campaigns are attributed separately; purchases with no touch are unattributed. Broadcast and journey
  reports add the revenue of their recipients: revenue events within the conversion window (broadcasts) or 7 days of a
  contact's first message.
- **UTM capture.** The web SDK reads `utm_source/medium/campaign/term/content` (and the referring site's domain) from the
  landing URL: first touch is stored once, last touch every time, and both go with `identify` and SDK form submissions as
  `first_utm_*`, `first_referrer`, `last_utm_*`, `last_referrer` (the profile keeps `first_*` once set). Landing pages get it
  through the SDK that `lp.js` starts.
- **Custom reports.** Sources: events, message outcomes (`message_events`, test sends excluded) and contacts (snapshot).
  Metrics: count, unique contacts, sum or average of a numeric property / attribute. Groups: event name, property, profile
  attribute (events aggregate per contact first, then join the snapshot), outcome, channel, broadcast/journey, day/week/
  month. At most 100 groups (`LIMIT 100`), at most a year, and grouping by day only for ranges of 100 days or less.
  Saved cohort and custom reports share `saved_reports (kind, spec)`.
- **Funnel breakdown.** The funnel's per-contact levels join the contacts snapshot (streamed; the funnel's contacts are the
  hash side) on one attribute; the top 10 values by step-1 contacts come back and the rest is "everyone else".
- **Journey A/B winners.** A split step records `journey_split {journey_id, step, path, label, decided}` (platform-only)
  for every contact it routes. A split with `winner: {metric, after_seconds, min_per_variant}` is checked every 5 s by the
  journey workers (`journey.WinnerPicker`): per path, contacts routed before a decision and those who then opened or clicked
  a message of an email/inbox step after the split (`message_events`) or did the goal event. Once `after_seconds` have passed
  since the first contact and every path has `min_per_variant`, the best rate wins (ties: more hits, then the first path)
  and is written once to `journeys.split_winners`; the engine's next refresh routes every later contact down it. A decision
  whose path no longer leads to the same step (the journey was edited) is ignored. Assignment before the decision stays
  the per-enrollment hash.

### At scale (needs pre-aggregation)

These run on raw events today and are fine into the hundreds of millions of rows per tenant; past that they want rollups:

- Cohorts by first seen read the whole contacts snapshot of the tenant (`FINAL`, the attrs map); "any activity" returns
  read every event name in the range. A daily `(tenant, contact, day)` activity table (an `AggregatingMergeTree` fed by a
  materialized view) would make return activity a narrow scan.
- Attribution groups purchases by event id to drop pipeline duplicates and streams one row per attributed purchase to the
  API; a `purchases` table (ReplacingMergeTree by id, with the amount extracted) and a per-contact touch rollup would cut
  both.
- Breakdowns and attribute groups join the contacts snapshot; a narrow `(tenant, id, key, value)` table for frequently used
  attributes (or a dictionary) would avoid reading the attrs map.
- Report builder sums use events as stored, so a redelivered event can count twice until ClickHouse merges its copies.
