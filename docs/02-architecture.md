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

- Groups: `all` and `any`, nestable. Attribute ops: `eq ne gt gte lt lte in contains exists`. Event ops: `eq ne gt gte lt lte` on `count`.
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
- Rule types: attribute comparisons (`eq`, `ne`, `contains`, `in`, `exists`, `gt`/`gte`/`lt`/`lte`, and dates: `before`, `after`,
  `within_days`, `older_than_days`), event counts in a window with property filters, tags, and membership of another segment or list.
- Every profile keeps `first_seen` (first event time) and `last_seen` (UTC date of the latest event) for recency rules.
- Rules that depend on today (event windows, relative dates) are swept periodically; the Go evaluator and the ClickHouse compiler
  must agree on every rule (`make it`).

## Dispatch
- Per-tenant token bucket in Redis (`EMAIL_RATE_PER_SEC`).
- Templates use Go `html/template` and `text/template` over `{contact, attrs}`.
- Providers sit behind the `channel.Sender` interface: SMTP in Phase 1, then SES, Twilio, Gupshup, MSG91, Google RBM, FCM/APNs and VAPID.
- Every send emits an `email_sent` or `email_failed` event back into `events.raw`, so journeys and segments can react to delivery.

## ClickHouse tables
- `events`: `MergeTree PARTITION BY toYYYYMM(ts) ORDER BY (tenant_id, name, contact_id, ts)`. Event-window segment queries hit only the
  key range for (tenant, event name).
- `contacts`: `ReplacingMergeTree(version) ORDER BY (tenant_id, id)` with `attrs Map(String, String)`. Queried with `FINAL`.
