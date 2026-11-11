# Pulse architecture

Pulse is a multi-tenant, real-time marketing automation platform written in Go on Kafka, ClickHouse, Postgres and Redis.
The design targets 10–100M contacts and 10k–100k events/sec. This document covers the overall design and the
contracts the code in this repository implements, from Phase 1 through batch 9.

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
                                          [messages.<channel>]  (broadcasts send here too)
                                                       │
                                                   dispatch  rate limit, idempotency, render, SMTP / providers
                                                       └──► <channel>_sent / _failed events back into [events.raw]
```

All roles ship in one binary, `pulse <role>`. Each role is a separate deployment that scales on its own Kafka consumer group,
and `pulse all` runs every role in one process for local development.

## Kafka topics

| Topic | Key | Producer → consumer | Payload |
|---|---|---|---|
| `events.raw` | `tenant:external_id` | api (ingest, forms, tracking), journeys (incl. imports), dispatch, integrations → profile | `event.Event` without `contact_id` |
| `events.enriched` | `tenant:contact_id` | profile → chsink, segments, journeys, webhooks, integrations | `event.Event` with `contact_id` and the merged `attrs` snapshot |
| `segment.membership` | `tenant:contact_id` | segments → journeys, chsink, webhooks, integrations, segments (segments that depend on other segments) | `segment.Change{tenant, segment, contact, entered, source}` |
| `messages.<channel>` (`email`, `sms`, `whatsapp`, `rcs`, `push`, `inbox`) | `tenant:contact_id` | journeys (and broadcasts), api test sends, dispatch (SMS fallback, held messages) → dispatch | `model.Message{id, tenant, contact, channel, template, ...}` |
| `delivery.receipts` | `tenant:provider message id` | api (provider webhooks) → dispatch | delivery receipts (delivered, read, failed) |

Each key keeps one contact's events in order on a single partition. That ordering is what lets the streaming segment path work without locks.

## Identity and tenancy

- `tenants(id, name, slug, api_key_hash, site_key, …)`. Every API call resolves a tenant from `Authorization: Bearer <api key>`,
  a signed-in user's session cookie, or (browser and app SDK routes under `/v1/web/`) the public site key.
- `contacts(tenant_id, id, external_id, email, attrs jsonb)`. `id` is **dense per tenant** (1, 2, 3…), allocated from
  `tenant_counters` with `UPDATE … RETURNING`. Dense ids let a segment be a Redis bitmap: 100M contacts = 12.5 MB per segment.
- `tenant_id` is the first column of every Postgres primary key, every ClickHouse `ORDER BY` and every Redis key.
  Repository functions always take `tenantID`. Postgres Row-Level Security is not enabled (see Known gaps in the parity doc).

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
3. Segments that contain event windows are swept every `PULSE_SWEEP_INTERVAL` (default 5 minutes), so "within N days" membership decays
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
- Nodes (`internal/journey/graph.go`, the builder's newer steps in `nodes.go` / `steps.go`). Outputs: actions and waits
  have `next`; decisions and conditions `yes` / `no`; splits one path per variant; `exit`, `jump` and `delete_contact` end
  the path.
  - **Actions:** `send_email`, `send_sms`, `send_whatsapp` and `send_rcs` (optional SMS fallback), `send_push`,
    `send_inbox`; `send_message` (marketing message: `channels` [{channel, template_id}] in priority order, sent on the
    first the contact can receive: an address or push device, consent, no bounce; none reachable records
    `message_unreachable`); `notify_user` (email to users, the contact's owner or fixed addresses, the template rendered
    with the contact's data; the message carries `To`, so the dispatcher skips consent, caps, quiet hours and tracking and
    records `notification_sent` / `notification_failed`); `webhook`; `update_attr` (values may use `{contactfield=x}`,
    `{{.Attrs.x}}`, and `now`, `now+3hours`, `now-2days`… resolved at run time); `update_tags`, `adjust_points`,
    `update_list` (`list_id`+`list_action`, or `add_lists` / `remove_lists`: static lists only), `change_stage`,
    `update_owner` (`owner_changed`), `add_company` (`company_linked`; one company per contact, so also "primary
    company"), `update_company` (merges `company_attrs` into the contact's company, then `company_updated` to every
    member), `company_points` (adds to the company's `score` attribute once per step key, recorded in `journey_effects`),
    `change_journey` (records `journey_change {add, remove}`; the journey engine removes the contact from those journeys —
    status `removed` — and enrolls them in the active ones), `dnc_add` / `dnc_remove` (`unsubscribed` / `resubscribed`
    with method manual, source journey), `show_focus`, `push_dwc` (see below), `push_integration` (`integration_id`: a HubSpot or Salesforce connector; see
    "Integrations"), `jump`, `delete_contact`.
  - **Decisions** (park on `journey_event_waits` with a timeout; Yes when the event comes, No at the timeout):
    `wait_for_event` (any event); `message_decision` (an earlier send step's own message, matched on the message id
    `{enrollment}:{step}`: opened, clicked — optionally a link containing `url` —, delivered, read, replied (email,
    RCS), clicked (push, no link filter);
    the send step must be upstream); `on_form` (`form_submitted`, optional `form_id`), `on_asset` (`asset_downloaded`),
    `on_page` (`page_view` whose url contains `url`, or `landing_page_viewed` of `page_id`), `on_device` (`page_view`
    with `device_type` / `os`), `on_dwc` (`dwc_requested` for `slot`). The wait stores only the event name; the engine
    checks the step's filters (`Node.Matches`) against the event's props before resuming.
  - **Conditions** (now): `condition` (segment or list), `check` (any segment rule; `preset` tells the builder which
    form made it: field, tags, stage, points, owner, device), `if_journey` (active / completed / entered other journeys),
    `if_form_field` (a field of the submission recorded on the enrollment — the form_submitted that started or resumed
    it — else the latest in ClickHouse; compared like a segment rule), `if_notification` (has a push device and isn't
    opted out of push, or has an unread, unexpired app inbox message), `if_valid_email` (syntax, then MX — or A/AAAA,
    RFC 5321's implicit MX; a null MX fails — with a 3 s timeout, cached 6 h, DNS errors pass and are cached 1 minute;
    optional disposable-domain list), `if_dnc` (unsubscribed from the channel or everything, or bounced), `if_before`
    (Yes until a date: Mautic's "prevent action after date"), `if_visited` (ClickHouse page views, url contains, last N
    days or ever).
  - **Waits and splits:** `wait` (a duration), `wait_until` (a date, or a time of day in the contact's zone), `split`
    (A/B/n by weight, optional automatic winner).
  - Journey settings: trigger (`segment_entered`, `event`), re-entry (`once`, `always`, `after_days`), goal event, schedule window.
- **Jumps.** A graph must be acyclic except through `jump` steps, whose target isn't an edge. `journey_enrollments.jumps`
  counts an enrollment's jumps; at 25 the next jump ends the enrollment (completed, `journey_jump_limit` recorded). Step
  keys are `{enrollment}:{step}` plus `:j{n}` after n jumps, so a step repeated after a jump sends (and records) again,
  and a message decision waits for that pass's message. A jump also moves the pending safety timer to the target.
- **Delete contact** uses the cleanup deletion path (`maintenance.Cleaner.DeleteContacts`: ClickHouse rows first, then
  Postgres: contact, devices, inbox, enrollments). The step needs `confirm: "DELETE"`; saving or activating a journey
  with it needs edit access to contacts, as does one with Remove Do Not Contact, Update contact owner, Update company,
  Add to company or Add to company's score (`contactSteps` in `internal/api/journeyrefs.go`; Add DNC only stops messages,
  so it needs nothing more). Events still in flight for that contact can create a new, empty profile.
- **Per-contact on-site content** (`internal/onsite/pushes.go`, Redis): `show_focus` adds the focus item to
  `ofocus:{tenant}:{contact}` (30 days); the decide API (`POST /v1/web/decide`) for a known — verified, when identity
  verification is on — contact takes and clears it and appends the item (any status: a draft item is journey-only).
  `push_dwc` sets `odwc:{tenant}:{contact}` slot → HTML for N days (default 30); the decide API returns it for that slot
  instead of the slot's own content. The decide API also records `dwc_requested {slot, url}` for each requested slot of a
  known contact, which `on_dwc` decisions wait for.
- **Inbound replies** (`internal/replies`). Set Settings → Channels → Delivery rules → "Reply tracking address" (`reply_to`, e.g.
  `replies@inbound.example.com`; point that domain's MX at the email provider's inbound parse). Every tracked email then
  gets `Reply-To: replies+r<code>@inbound.example.com`: a random 16-character lower-case code stored in Redis
  (`rtok:<code>`, 30 days) with the message's tracking token. The provider posts replies to the workspace's
  `POST /v1/replies/{token}` (the same secret as the bounce URL; shown as `reply_url` under bounces). SendGrid Inbound
  Parse (multipart), Mailgun routes (form) and Postmark inbound (JSON, `MailboxHash`) are read, plus generic JSON
  `{to, from, subject, text}`. Each code found among the recipients records `email_replied` (message, template, journey
  or broadcast ids, from, subject, text) on the message's contact. Replies go to that address, not to the sender's inbox:
  forward them on at the provider if people should read them.
- **Per-step counts** for the canvas: the executor and engine count `entered`, `yes`, `no` and `v{i}` per step in a
  Redis hash `jsteps:{tenant}:{journey}` (best effort; a retried step can count twice); `GET /v1/journeys/{id}/report`
  returns them as `steps`.
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
  and membership of another segment, dynamic or static. A missing attribute reads as "" in both compilers, so "is empty" is `eq ""`.
- The web builder (`web/src/segmentRules.ts` editing model and `treeReducer`, `web/src/segmentCatalog.ts` filter
  catalog, `web/src/pages/SegmentBuilder.tsx` and `FilterPicker.tsx`) is described in the parity doc's "Segment builder".
  Value suggestions come from `GET /v1/attributes/{key}/values` and `GET /v1/events/{name}/values?prop=` (top 25
  values of a 200k-row ClickHouse sample, cached 10 minutes in Redis).
- Every profile keeps `first_seen` (first event time) and `last_seen` (UTC date of the latest event) for recency rules.
- Rules that depend on today (event windows, relative dates) are swept periodically; the Go evaluator and the ClickHouse compiler
  must agree on every rule (`make it`).

## Contact views and imports (batch 9)
- **Views.** `GET /v1/contacts?q=&after=&limit=` (contacts view) parses `q` (`internal/api/contactview.go`): `key:value`
  filters (`is:`, `segment:` / `list:`, `tag:`, `stage:`, `company:`, `owner:`, `points:`, `email:`, `name:`) become one
  `all` segment rule, resolved against the workspace's segments and users; other words are free text. `owner:` with a
  teammate's name, email or id needs view access to settings (the team list); without it only `owner:me` and
  `owner:none` work, and other values answer 400. Without either,
  contacts page through Postgres by primary key, leaving out visitors merged into another contact (`merged_into`), as
  filtered views and the Overview count do. Otherwise ClickHouse (`store/ch/contactview.go`) selects ids from
  `contacts FINAL` with `segment.WhereSQL` (the condition `ToSQL` uses, so a view matches a segment with the same rule)
  and a case-insensitive `positionCaseInsensitiveUTF8` over external ID, email, name, first/last name and phone,
  `ORDER BY id DESC LIMIT n` below the `after` id; the page's contacts then load from Postgres by id. The first page adds
  `count` (exact up to 10,000 via a `LIMIT 10001` subquery, else `uniq(id)` without `FINAL`, flagged `approximate`),
  `filters` (`{token, label}` for the chips) and `rule` (for "Save as segment", which posts it to `/v1/segments`). Bad
  filters answer 400 with the reason. `GET /v1/contacts/export?q=&columns=` (contacts edit) streams the same view as
  CSV, 1,000 ids per ClickHouse page, cells through `csvSafe`. Details and approximations: the parity doc's "Contact views".
- **Imports.** The app's one import flow (`/contacts/import`) reads the header and a few rows in the browser
  (`web/src/importMapping.ts`), lets the user map each column (External ID, Email, Phone, Tags, a contact field, a new
  field, or Don't import) and uploads a new header row followed by `file.slice(end of the old header)` (a Blob, so a
  10 GB file is never read into memory) to `POST /v1/imports` with `list_id` for a static segment. The importer skips
  columns with an empty heading. `POST /v1/contacts/import` (synchronous, 200 MB) stays for API clients.

## Dispatch
- Per-tenant, per-channel token bucket in Redis (messages/second: email 200, set by `PULSE_EMAIL_RATE`; SMS 100, WhatsApp 80,
  RCS 50, push 500, inbox 2,000).
- **SMS bounces.** A provider error for the number itself (`providers.BadNumber`: Twilio 21211/21214/21612/21614, or
  an unreachable recipient, e.g. the sandbox's unreachable suffix) or a failed receipt marked unreachable (Twilio
  30003/30005/30006) records `sms_failed {bounce: true}` with `attrs.sms_bounced = true`; later SMS are
  `sms_suppressed` until the number changes. A contact without a valid number is a plain failure, not a bounce.
- **SMS / RCS click tracking.** With `content.track_links` on the template (Content → Message templates, an SMS or RCS
  template, "Track link clicks"), the dispatcher replaces each http(s) link in the rendered text (and RCS `url` suggestions) with
  `{PULSE_PUBLIC_URL}/t/s/{code}`: 10 random base62 characters naming the message plus one naming the link (about 40
  characters in all; trailing punctuation stays outside the link). The message's tracking token and destinations are one
  Redis key per message (`slink:{code}`, 30 days). `GET /t/s/{code}` records `<channel>_clicked {url, message_id,
  template_id, journey_id, broadcast_id…}` once per message and link, then redirects (302); unknown or expired codes get
  404. WhatsApp is not covered: business-started WhatsApp messages are pre-approved templates whose button URLs live at
  Meta, so Pulse can't rewrite them.
- Templates use Go `html/template` and `text/template` over `{contact, attrs}`.
- Providers (`internal/providers`): SMTP (any relay, SES included), Twilio and MSG91 (SMS), Meta WhatsApp Cloud API, Google RBM
  (RCS), FCM, standard web push (VAPID), the sandbox, and the custom HTTP gateway for any channel. iOS push goes through FCM
  (no direct APNs).
- Every send emits a `<channel>_sent` or `<channel>_failed` event (plus `_suppressed`, `_capped`, `_deferred`, `_fallback`)
  back into `events.raw`, so journeys and segments can react to delivery.

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
| `/t/*`, `/u/*` | email tracking and unsubscribe (sealed tokens); `POST /t/p/{token}` push clicks / dismissals | |

## ClickHouse tables
- `events`: `ReplacingMergeTree PARTITION BY toYYYYMM(ts) ORDER BY (tenant_id, name, contact_id, ts, id)` (redelivered rows
  collapse). Event-window segment queries hit only the key range for (tenant, event name).
- `contacts`: `ReplacingMergeTree(version) ORDER BY (tenant_id, id)` with `attrs Map(String, String)`. Queried with `FINAL`.
- `segment_members`: current membership mirrored from `segment.membership` ("in segment" rules and static segments in SQL).
- `message_events`: message outcomes of broadcasts and journeys, filled by a materialized view (`message_events_mv2`)
  from `events`, for reports.

Migrations: `migrations/postgres/001`–`018` and `migrations/clickhouse/001`–`004`, applied by `pulse migrate` (and `pulse all`).

## Public pages and origins
- Landing pages (`/p/`), asset downloads (`/a/`) and hosted forms (`/f/`) are workspace-authored. With `PULSE_PAGES_URL` set they are
  served only from that origin (app-host requests redirect with 308), and that origin serves nothing but those pages, the SDK
  (`/sdk/`) and the browser API (`/v1/web/`). Session cookies are host-only, so pages never see them.
- Landing page HTML is sanitized by parsing it with the HTML5 algorithm (`golang.org/x/net/html`) and serializing the tree:
  scripts (HTML and SVG), event handlers, `srcdoc`, `<base>`, `<meta http-equiv>`, `<object>`, `<embed>` and javascript:/vbscript:/
  non-image data: URLs are removed. A nonce CSP allows only the Pulse SDK scripts.
- On Pulse-hosted pages the SDK namespaces its stored ids by site key (`storagePrefix`), so workspaces sharing the pages origin
  never share a visitor's identity.

## Integrations (batch 8a)

`internal/integrations` holds the connectors; `internal/api/integrations.go` and `hooks.go` the routes; the
`integrations` role (in `pulse all`) runs pulls and audience sync. The parity doc's
[Integrations](05-feature-parity.md#integrations-batch-8a) lists what each connector does and doesn't.

- **Storage** (`migrations/postgres/016_integrations.sql`): `integrations` (kind, name, `config` jsonb shown as is,
  `secrets` a JSON object sealed with the server key, `mapping` [{remote, attr, dir}], pull schedule and cursor, status,
  the REST hooks key's SHA-256 and last characters), `integration_log` (pruned to a week), `audience_syncs`,
  `audience_members` (hashes uploaded per contact) and `audience_queue`. REST hook subscriptions are rows of `webhooks`
  with `integration_id` (and optional `segment_id` / `form_id` filters); the workspace's own webhook routes never see them.
- **Secrets.** Read through `Service.OpenSecrets`; the API returns `secrets` masked (`Mask`: the last 4 characters) and
  `MergeSecrets` keeps a stored value when an edit leaves it empty or sends the masked text back. The audit log redacts
  the request's `secrets` object like any key containing "secret" or "token".
- **Outbound calls** use `safehttp.Client` (`PULSE_ALLOW_PRIVATE_WEBHOOKS` lets them reach private addresses, for local
  mocks). Every remote base URL (HubSpot API, Salesforce My Domain, Shopify admin, Graph API, Google Ads API and token
  URL) is a connector setting defaulting to the real one 
  (`Field.Hosts`, `CheckHosts`): outside local development (`PULSE_ALLOW_PRIVATE_WEBHOOKS`) it must be https on the vendor's own hosts (api.hubapi.com, *.salesforce.com, *.myshopify.com, graph.facebook.com, googleads / oauth2.googleapis.com), and changing any URL or the shop domain discards the stored credentials, so they must be entered again (`EndpointsChanged`).
- **CRM pulls.** `ClaimDuePulls` claims due connectors with `FOR UPDATE SKIP LOCKED` and moves `next_sync_at` forward,
  so any number of workers share them. A pull asks for records modified at or after the cursor (`<time> <ids at that
  time>`, so the last records aren't recorded twice), oldest first, 1,000 at most (more are due at once), and publishes
  one `crm_synced` event per record into `events.raw` with the mapped attributes plus `hubspot_id` / `salesforce_id`. The
  event id is a hash of connector, record and modification time, so a retried pull dedupes in the profile role. The
  mapping refuses attributes Pulse keeps (`consent.IsReserved`, `event.EventOnlyAttr`, `optout_*`) unless they're push
  only; the profile role would drop them anyway.
- **Pushes.** `Service.PushContact` (API "Push a contact" and the journey executor's `push_integration`) maps the
  contact, updates the known record or the one with its email or creates it, retries network errors, 429 and 5xx
  twice, then records `crm_pushed` (with the record id as an attribute when it's new; the journey's step key is the
  event id). The executor records `integration_push_failed` on failure and moves on.
- **Shopify.** `POST /v1/webhooks/shopify/{id}` reads the connector by id, checks the HMAC (base64 HMAC-SHA256 of the
  raw body) and `X-Shopify-Shop-Domain`, parses the topic (`ParseShopify`) and publishes one event with the customer's
  attributes on the contact found by email (`ExternalIDByEmail`), else phone, else a new one. The event id is
  `shopify:<X-Shopify-Webhook-Id>`.
- **REST hooks.** `/v1/hooks/*` authenticate with the connector's key (`IntegrationByKeyHash`); changes are audited as
  "Integration: <name>". The webhooks worker delivers subscriptions like any webhook: `Webhook.wants` applies the segment
  and form filters, and `decode` adds a `contact_created` payload when an enriched event's `changed_attrs` include
  `date_identified` (once per contact). A 410 answer deletes an integration's subscription (`Unsubscriber`).
- **Audience sync** (`Syncer`): the `pulse-integrations` consumer group reads `segment.membership` and
  `events.enriched` and queues (link, contact) rows; `Flush` claims up to 5,000 per link every two seconds, reads
  membership with pipelined `GETBIT`s, the contacts from Postgres and the hashes already uploaded, then removes and adds
  the difference (`Platform.Remove` / `Add`, 5,000 per call) and saves `audience_members`. `Wanted` decides: in the
  segment, not `optout_all`, not merged away, and an email or phone that normalises (`HashFor`: lower-case trimmed email,
  gmail dots dropped for Google; E.164 phone, with the connector's default country code for national numbers, without
  "+" for Facebook). `ClaimDueReconciles` starts a full reconcile per link on creation and then every
  `PULSE_AUDIENCE_RECONCILE_INTERVAL`, walking the segment bitmap and then `audience_members` for contacts no longer in
  it. Errors put the queue back, mark the link `error` and back off exponentially (2 s to 10 minutes).
- **Tests.** `internal/integrations` unit tests run every connector against `httptest` mocks; the e2e tests
  `TestHubSpotIntegration`, `TestRESTHooksE2E` and `TestAudienceSync` run mocks in the compose-network container
  (`PULSE_HOOK_HOST`), and `TestShopifyWebhooks` signs deliveries itself.

## Reach (batch 8b)

- **Native SDKs** (`sdks/android`, `sdks/ios`). Each splits a platform-neutral core (queue, batching and backoff,
  HTTP behind an interface, JSON, validation, sessions, inbox and in-app logic, push click reports, opt-out) from the
  platform module (storage, lifecycle, device properties, notification helpers, in-app UI). The cores are plain
  Kotlin/JVM and a Foundation-only Swift target, so they are unit-tested in Docker; stored keys are
  `pulse.<siteKey>.*`. They use the same `/v1/web/*` API as the Flutter SDK. See each README for what was and wasn't
  built here.
- **RCS replies.** `providers.GoogleRBM` implements `ReceiptParser` and `BodyChallenger`: the webhook URL check
  (`{clientToken, secret}` → `{secret}`) and messages `{message: {data: base64}}` verified by `X-Goog-Signature` =
  base64 HMAC-SHA512 of the decoded data keyed by the agent's client token (a provider secret). DELIVERED / READ
  events become receipts on Pulse's message id (the RBM message id); text and suggestion taps (`suggestion` reply or
  action, `postback`) become inbound messages handled like SMS / WhatsApp (`<channel>_received`, keywords). RBM
  doesn't say which agent message a user message answers, so the dispatcher keeps the latest successful RCS message per
  contact in Redis (`lastmsg:{tenant}:rcs:{contact}`, 7 days, `replies.SaveLast`) and the inbound handler records
  `rcs_replied` with that message's ids (journey, step key, broadcast), which `message_decision` replied waits for.
- **Bounce mailbox** (`internal/bounces`: `report.go`, `mailbox.go`, `imap.go`; `bounce_mailboxes`, migration 017).
  One IMAP mailbox per workspace, edited at `/v1/settings/bounce-mailbox` (settings edit access; `test` connects with
  unsaved settings, `check` makes it due now). The password is sealed with the server key and returned masked; a
  changed host, port, encryption or user needs it typed again. Plain connections are refused unless
  `PULSE_ALLOW_PRIVATE_WEBHOOKS` (development); connections go through `safehttp.Dialer` (public addresses only). The
  integrations role claims due mailboxes (`FOR UPDATE SKIP LOCKED`, every 5 minutes), reads up to 200 unseen messages
  (`BODY.PEEK[]`), parses RFC 3464 reports (failed + 5.x.x = hard, 4.x.x or delayed = soft; nested reports and base64
  parts are read) and RFC 5965 feedback reports (complaint for `Original-Rcpt-To`, else the original message's `To`;
  `not-spam` skipped), records them through `bounces.Events` (the webhook path's code) with event ids derived from the
  message hash, then flags every read message `\Seen`. A failed publish leaves them unseen for the next check.
- **Push click tracking.** For push the dispatcher seals a tracking token and adds `data.pulse_track` =
  `{PULSE_PUBLIC_URL}/t/p/{token}`. `POST /t/p/{token}?a=clicked|dismissed&platform=` (CORS `*`, no body, so the service
  worker's `no-cors` fetch needs no preflight) records `push_clicked` / `push_dismissed` once per message with the
  message's ids; `push_clicked` lands in `message_events` like other clicks.
- **Form uploads and pages.** Field types `file` (`accept` extensions, `max_mb` 1-10) and `page_break` (its label
  titles the next page). Uploads are read from multipart posts (hosted page, gated asset page, and the web SDK, which
  sends `payload` JSON plus files when a form has files), checked by `assets.DetectUpload` (extension allowed, format
  signature, and Go's WHATWG content sniffing must not see HTML, XML, script or SVG; no SVG or ZIP uploads) and stored
  as assets with `upload_form_id` (migration 018): every other asset query excludes them, `GET
  /v1/forms/{id}/uploads/{upload}` serves them (contacts view access, attachment, sandbox CSP), and deleting the form
  or the contact deletes them. `form_submitted.fields.<key>` holds `{upload_id, filename, size, content_type}`; file
  fields never become attributes. The hosted and gated pages render every page in one form with a nonce'd script that
  shows one page at a time, checks it (`checkValidity`, size and type) before Next, and opens the page with the first
  server error; without scripts all pages show. The landing page preview draws the first page and the progress.
- **Audit export.** `GET /v1/audit/export?format=csv|json&user_id=&object_type=&from=&to=` (settings edit, like the
  log) pages through `ListAudit` 1,000 entries at a time and streams CSV (`csvSafe` on every text cell) or one JSON
  array.

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
