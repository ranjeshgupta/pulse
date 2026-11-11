# 7. Architecture and extending

A developer's map of the code: how one event moves through Pulse, which package does what, and the
recipes for the changes people make most: a new channel provider, a new journey step, a new segment
filter, a new migration, a new API route.

The design reasons, data layouts and contracts (topics, keys, ClickHouse tables, journey semantics,
segment compilers) are in **[the architecture document](../02-architecture.md)**; this guide links
there instead of repeating it.

---

## One event, end to end

Follow a `purchase` sent with the API key. Each arrow is a Kafka topic; each box is a role you can
run and scale separately (`pulse <role>`, see [Configuration](02-configuration.md#process-roles)).

```
POST /v1/events ─► api ─► [events.raw] ─► profile ─► [events.enriched] ─┬─► chsink ─► ClickHouse
                                                                          ├─► segments ─► [segment.membership] ─┬─► journeys
                                                                          ├─► journeys                           ├─► chsink, webhooks
                                                                          ├─► webhooks                           └─► integrations
                                                                          └─► integrations
journeys / broadcasts ─► [messages.<channel>] ─► dispatch ─► providers ─► <channel>_sent | _failed ─► [events.raw]
provider webhooks ─► api ─► [delivery.receipts] ─► dispatch ─► <channel>_delivered | _read | _failed ─► [events.raw]
```

1. **`api`** (`internal/api/marketing.go`, `ingest`): authenticates (`server.go`), checks the
   route's permission (`perms.go`), validates each event (`event.Event.Normalize`), refuses events
   only Pulse may record (`event.PlatformOnly`), links companies, and publishes to `events.raw`
   keyed `tenant:external_id`. It answers 202 only after Kafka acknowledged every record.
2. **`profile`** (`internal/profile`): resolves the external ID to the dense per-workspace contact
   ID (creating the contact), merges anonymous visitors, folds the event into the attributes
   (`MergeScored`: consent, tags, points, stage, company, device, dates), bumps the Redis day
   counters segments read, skips event IDs it has already processed (24 h), and publishes to
   `events.enriched` keyed `tenant:contact_id` with the profile snapshot and the changed attributes.
3. **`chsink`** (`internal/chsink`): writes every enriched event, and contact snapshots, to
   ClickHouse; segment changes too.
4. **`segments`** (`internal/segengine`, rules in `internal/segment`): looks up only the segments
   that reference this event or the changed attributes (an inverted index refreshed every 2 s),
   runs `segment.Eval` for this contact, flips the Redis bitmap bit and publishes
   `segment.membership` (entered / exited). New or edited segments are backfilled from ClickHouse
   with `segment.ToSQL`; windowed segments are swept every `PULSE_SWEEP_INTERVAL`.
5. **`journeys`** (`internal/journey`): enrolls contacts on `segment_entered`, event or form triggers (the index of
   active journeys reloads at once when the API announces a change, see `internal/journey/changes.go`),
   walks the graph (`Executor.Run`), parks waits and decisions on durable Postgres timers, and
   publishes messages to `messages.<channel>`. The same role runs scheduled broadcasts
   (`internal/broadcast`), background imports, retention cleanup and A/B winner picks.
6. **`dispatch`** (`internal/dispatch`): renders the template, checks consent, frequency caps and
   quiet hours, rate-limits per workspace and channel, sends through the workspace's provider
   (`internal/providers`), falls back to SMS, and records the outcome as an event, which goes round
   the loop again (so reports, segments and journeys see `email_opened` like any event).
7. **`webhooks`** and **`integrations`**: read the enriched events and membership changes and call
   out (outbound webhooks, Zapier subscriptions, ad audiences, CRMs).

### Delivery guarantees

Every consumer is **at-least-once** (`internal/kafka`): offsets are committed after the handler
succeeds, so after a crash a record can be handled again. Handlers must be idempotent, which is why
every derived event carries a deterministic ID (`msg:<message>:<channel>_sent`,
`rcpt:<message>:<status>`, journey `StepKey`s) and the profile role skips IDs it has seen.

| Consumer loop | On error |
|---|---|
| `Run` (record by record: dispatch, journeys) | 4 tries, 100-800 ms apart, then the record goes to `<topic>.dlq` with an `error` header |
| `RunPartitionBatches` (profile, segments, webhooks, integrations) | 4 tries of the batch, then record by record, then the DLQ |
| `RunBatch` (chsink) | Retried forever with backoff up to 10 s (ClickHouse being down must not lose events) |

These quick tries are for records a handler can't process at all. Slow failures are handled
inside the role so they don't block the partition: `dispatch` parks a message whose provider failed
temporarily in Redis and retries it for about two hours before recording `<channel>_failed`
(`internal/dispatch/retry.go`); `webhooks` does the same for outbound webhooks. Nothing replays a
`.dlq` topic automatically: watch them, fix the cause, then run `pulse replay-dlq <topic>`
(`kafka.Replay`; `--dry-run` lists the records, `--limit N`), see
[installation](01-installation.md#dead-letter-topics-and-replaying-them).

### Where state lives

| Store | Holds | Package |
|---|---|---|
| Postgres | Workspaces, users, sessions, contacts and attributes, segments, journeys, enrollments and timers, templates, broadcasts, providers (sealed), webhooks, forms, assets, imports, settings, audit log | `internal/store/pg` |
| ClickHouse | Every event, contact snapshots, segment membership history, message outcomes (materialised views), everything reports read | `internal/store/ch` |
| Redis | Segment bitmaps, event day counters, send idempotency and rate limits, message retry and quiet-hours queues, webhook retry queue, sign-in throttling, on-site pushes, reply codes | used directly (`go-redis`) |
| Kafka | Events and messages in flight | `internal/kafka` |

Tenancy: `tenant_id` leads every Postgres key, ClickHouse `ORDER BY` and Redis key, and every
store function takes the tenant ([architecture, identity and tenancy](../02-architecture.md#identity-and-tenancy)).

---

## Package map

| Package | What it is |
|---|---|
| `cmd/pulse` | The binary: one function per role, wiring (`deps`) |
| `cmd/seed`, `cmd/loadgen` | Demo and docs data (`make seed`, `make docs-seed`); the load generator (`make load`) |
| `internal/config` | `PULSE_*` variables |
| `internal/api` | HTTP: routing (`server.go`), permissions (`perms.go`), one file per area. Handlers depend on narrow store interfaces |
| `internal/auth` | Passwords (argon2id), tokens, the AES-GCM `Box` that seals secrets, roles and permissions |
| `internal/event` | The event envelope, reserved names, topic names |
| `internal/kafka` | Producer, consumer loops, DLQ and its replay, topic creation |
| `internal/profile` | Identity resolution and the profile fold |
| `internal/segment` | Segment rule AST, the two compilers (`Eval`, `ToSQL`), bitmaps, counters, funnels |
| `internal/segengine` | Keeps membership current: streaming, backfill, sweeps, dependent segments |
| `internal/journey` | Journey graph, validation, executor, timers, A/B winners |
| `internal/broadcast` | Scheduled broadcasts and A/B tests |
| `internal/dispatch` | The send pipeline and rate limiter |
| `internal/channel` | Channels, template content, rendering, validation |
| `internal/personalize` | Standard variables (`{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`, `{{links.x}}`) and pipes, translated into Go templates |
| `internal/providers` | Provider adapters and their catalog (SMTP, Twilio, MSG91, Meta WhatsApp, Google RBM, FCM, web push, sandbox, custom gateway) |
| `internal/consent`, `internal/delivery` | Opt-outs; frequency caps, quiet hours, keywords |
| `internal/tracking`, `internal/replies`, `internal/bounces` | Open/click/unsubscribe links, reply codes, bounce parsing and IMAP mailboxes |
| `internal/webhooks` | Outbound webhook worker and signing |
| `internal/integrations` | CRM, Shopify, REST hooks, ad audiences |
| `internal/forms`, `internal/landing`, `internal/assets`, `internal/onsite`, `internal/inapp` | Forms, landing pages, files, website content, in-app campaigns |
| `internal/analytics` | Cohorts, revenue attribution, custom reports |
| `internal/importer`, `internal/maintenance` | CSV imports; retention cleanup and contact deletion |
| `internal/safehttp` | The HTTP client for user-entered URLs (refuses private addresses) |
| `internal/geoip`, `internal/schedule`, `internal/attr` | GeoIP lookups; start/end windows and repeats; attribute value helpers |
| `internal/store/pg`, `internal/store/ch` | Postgres and ClickHouse stores |
| `migrations` | SQL files embedded into the binary |
| `web` | The React app (`web/src`), the web SDK (`web/src/sdk`), `embed.go` serving the build |
| `sdks` | Flutter, Android and iOS SDKs |
| `test/e2e` | End-to-end tests against a running stack |

---

## Add a channel provider

A provider adapter connects an existing channel (email, SMS, WhatsApp, RCS, push) to a service.
Everything lives in `internal/providers`; the web app's form is generated from the catalog, so a new
adapter needs no UI code.

1. **Create `internal/providers/<name>.go`** and register a `Kind` in `init()`:

   ```go
   func init() {
       register(Kind{
           ID: "acme_sms", Channel: channel.SMS, Name: "Acme SMS",
           Description: "Sends SMS through Acme's REST API.",
           Config:  []Field{{Key: "sender_id", Label: "Sender ID", Placeholder: "ACMEIN", Required: true}},
           Secrets: []Field{{Key: "api_key", Label: "API key", Required: true}},
           New: func(d Deps, c, s map[string]string) (Provider, error) {
               return &AcmeSMS{HTTP: d.HTTP, BaseURL: "https://api.acme-sms.example", Sender: c["sender_id"], Key: s["api_key"]}, nil
           },
       })
   }
   ```

   `Config` values are stored as-is and shown in the app; `Secrets` are sealed with
   `PULSE_SECRET_KEY` and never returned. `New` validates the settings (return an error for bad
   ones: saving fails with it).

2. **Implement `Send(ctx, Outbound) (Result, error)`**. `Outbound` has the rendered message
   (`Rendered.Subject`, `HTML`, `Content`), the recipient (`To`), Pulse's `MessageID` (pass it as
   the provider's idempotency key or reference if it has one) and, for email, headers and
   attachments. Return `Result{ProviderMessageID: …}` so receipts can be matched. Classify failures
   with the helpers, because the dispatcher acts on them:

   | Return | Dispatcher does |
   |---|---|
   | `Unreachable(reason)` | Recipient not on this channel: WhatsApp/RCS fall back to SMS; push tokens are deleted |
   | `BadNumber(reason)` | Permanent, and the SMS number is marked `sms_bounced` |
   | `Permanent(reason)` | Records `<channel>_failed`; no retry |
   | `StatusError(name, code, body, header)` | 429 and 5xx stay transient (carrying the response's `Retry-After`, if any), other codes become permanent |
   | any other error | Transient: the message is retried later, for about two hours (30 s up to 1 h apart, at least the `Retry-After`), then `<channel>_failed` |

   Use `d.HTTP` for fixed vendor hosts. For a URL an admin types in, use
   `safehttp.Client(timeout, d.AllowPrivate)` as the custom gateway does (`gateway.go`), so it can't
   reach your internal network.

3. **Receipts and inbound messages (optional)**: implement `ReceiptParser`
   (`ParseReceipts(*http.Request) ([]Receipt, error)`). **Verify the provider's signature first** and
   return `ErrUnauthorized` when it fails. Return `Receipt{ProviderMessageID, Status:
   "delivered"|"failed"|"read", Unreachable, Reason}`, or `Status: Inbound` with `From` and `Text`
   for replies (STOP handling then comes for free). If the provider verifies the URL first, implement
   `ChallengeResponder` (a GET, like Meta) or `BodyChallenger` (a POST, like Google RBM). The URL is
   `/v1/webhooks/providers/{id}`, already routed.

4. **Test** with `httptest` servers, like `twilio_test.go` and `msg91_test.go`: the request you
   send, each failure class, and signature checks. Add the kind's required settings to
   `TestNewWithRequiredSettings` in `catalog_test.go`; `TestCatalogKinds` checks every kind has an
   ID, name, description and labelled fields.

A whole new **channel** (not just a provider) touches more: `channel.All`, its `Content` fields and
`Validate`, the journey send node (`sendNodes`), `dispatch.DefaultRates`, consent
(`consent.IsReserved` covers `optout_<channel>`), the ClickHouse message views that build reports,
and the web app's channel lists and template editor. Look at how `inbox` was added.

---

## Add a journey step

Steps are validated in Go (`internal/journey`), run by the executor, and offered by the builder
(`web/src/journeyCatalog.ts`). Newer steps follow one pattern; take an existing one of the same
kind (action, condition, decision) as the template, e.g. `update_owner` for an action.

1. **`internal/journey/nodes.go`**: add the type constant and put it in `kinds` (action,
   decision, condition or terminal). Its kind decides its edges: actions use `next`, decisions and
   conditions `yes` / `no`, terminals none.
2. **`internal/journey/graph.go`**: add the fields it needs to `Node` with `omitempty` JSON tags.
   Graphs are decoded with `DisallowUnknownFields`, so the field must exist before the app can save
   it.
3. **Validation** in `validNew` (`nodes.go`): check every field and return messages people can act
   on; they're shown in the builder.
4. **Execution**: actions and conditions run in `Executor.runStep` (`steps.go`), which returns the
   next step ID. Side effects must be idempotent on retry: derive IDs from
   `StepKey(enrollment, step)`, as the other steps do. A decision waits for an event: add it to
   `DecisionEvent` and `Matches` (`nodes.go`) so the engine parks and wakes it.
5. **References**: if the step points at something in the workspace (a user, a template, another
   journey), check it in `internal/api/journeyrefs.go`, and add it to the steps that need contacts
   edit access if it changes contact data beyond marketing attributes.
6. **Web app**: add a catalog entry in `web/src/journeyCatalog.ts` (category, label, keywords,
   defaults), its form in `web/src/pages/journey/Inspector.tsx`, and its summary in
   `web/src/pages/journey/nodeText.ts`.
7. **Tests**: validation and execution in `internal/journey/steps_test.go` (or `builder_test.go`),
   the catalog in `web/src/journeyCatalog.test.ts`, and an end-to-end case in
   `test/e2e/builder_test.go` if it touches other roles.

Journey semantics (enrollment, re-entry, goals, timers, splits): [architecture,
journeys](../02-architecture.md#journeys).

---

## Add a segment filter

Two different things are called "filter".

**A new choice in the segment builder** that maps onto existing rules (a preset on an attribute,
an event with property filters) is web-only: add it to `web/src/segmentCatalog.ts` (group, label,
how it builds the rule) and `web/src/segmentRules.ts` if it needs a new editor, with tests in
`segmentCatalog.test.ts` and `segmentRules.test.ts`. A Contacts search filter (`tag:`, `is:`…) is
`internal/api/contactview.go` (`viewKeys`), which compiles to an ordinary rule.

**A new rule operator or node** changes the rule language, and must be implemented **twice, with
the same meaning**:

1. `internal/segment/ast.go`: accept it in `validCompare` (or `validate` for a new node kind), and
   make sure `Refs`, `Counters` and `HasWindow` report what it reads, so the streaming index and the
   sweep pick it up.
2. `internal/segment/eval.go`: the Go predicate (`compare`), used per event.
3. `internal/segment/sql.go`: the ClickHouse SQL (`compare` / `rule`), used for backfills, sweeps,
   previews and contact views. Bind values as parameters; never format them into the SQL.
4. Unit tests in `eval_test.go` and `sql_test.go`, then **`make it`**: `TestSQLAgreesWithEval`
   (`agreement_integration_test.go`) generates random contacts and rules against a real ClickHouse
   and fails on any contact the two compilers disagree about. Add the operator to its generator.
5. The builder (`segmentCatalog.ts` / `segmentRules.ts`) to offer it.

The rule format and the window semantics: [architecture, segment rules](../02-architecture.md#segment-rules).

---

## Add a migration

Migrations are SQL files embedded into the binary (`migrations/embed.go`) and applied by
`pulse migrate` (and `pulse all` at start), Postgres first, then ClickHouse, under a Postgres
advisory lock.

**Postgres** (`migrations/postgres/NNN_name.sql`, next number after the highest, today `018`):

- Applied once each, in file-name order, each in a transaction, and recorded in `schema_migrations`.
  **Never edit a file that has been applied** anywhere: add a new one.
- Lead new tables' keys and indexes with `tenant_id`.
- Big tables: prefer additive changes (new nullable columns, `CREATE INDEX` on a new table). A
  migration runs while the old binary may still be serving during an upgrade.

**ClickHouse** (`migrations/clickhouse/NNN_name.sql`):

- **Every file runs on every `migrate`**, statement by statement, so each statement must be safe to
  repeat: `CREATE TABLE IF NOT EXISTS`, `ALTER TABLE … ADD COLUMN IF NOT EXISTS`,
  `DROP VIEW IF EXISTS`.
- Files are split on `;` and lines starting with `--` are dropped, so don't put semicolons inside
  string literals or comments at line ends.
- A materialised view's query can't be changed in place: create a new one (`…_mv2`) and drop the old,
  as `004_message_events_v2.sql` does.

New **Kafka topics** go in `pipelineTopics` (`cmd/pulse/main.go`): `runMigrate` creates them with their `.dlq` twins, and `replay-dlq` accepts them.

Check a migration against a fresh database: `docker compose -f deploy/docker-compose.yml down -v`
(local only, it deletes everything), `make up`, then `make e2e`.

---

## Add an API route

1. Register it in the area's `…Routes(mux)` function with a method and pattern
   (`mux.HandleFunc("POST /v1/things/{id}/archive", s.tenant(s.archiveThing))`).
2. Wrap it: `s.tenant` or `s.authed` (signed in or API key; the permission comes from the table),
   `s.admin` (also settings edit), `s.fullAdmin` (every admin permission), or nothing for public
   routes, which must authenticate some other way (a site key, a signature, a token in the path).
3. **Permissions** (`internal/api/perms.go`): the first path segment maps to an area
   (`areaByPrefix`); reads need view, changes edit. Add an entry to `special` when it differs (a
   `POST` that only reads, a publish action, a `GET` that reveals secrets). An unlisted prefix needs
   settings edit. Add a case to `TestRoutePermissions`.
4. Non-`GET` requests through `authed` are written to the audit log automatically (with secrets
   redacted).
5. Errors: `writeErr(w, status, message)` with a message the user can act on; `s.fail(w, err)` for
   store errors (maps not found to 404, conflicts to 409, the rest to 500 without details).
6. Document it in [the API guide](03-api.md) (the route index is built from the router).

Next: [8. Contributing and testing](08-contributing-and-testing.md).
