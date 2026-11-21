# Pulse architecture

Pulse is a multi-tenant, real-time marketing automation platform written in Go on Kafka, ClickHouse, Postgres and Redis.
The design targets 10–100M contacts and 10k–100k events/sec. This document covers the overall design and the
contracts the code in this repository implements, from Phase 1 through batch 9 and the marketing
team's milestones M1 (events and segments), M2 (personalisation), M3 (campaigns), M4 (delivery truth and dashboards),
M5 (channels and settings), M6 (AMP and on-site) and M7 (contacts behind every number, one campaign model).

Naming: the app calls broadcasts **one-time campaigns** and journeys **triggered** or **automation campaigns**
(since M7); the code, the API and this document keep `broadcast` and `journey`.

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
| `messages.<channel>` (`email`, `sms`, `whatsapp`, `rcs`, `push`, `inbox`) | `tenant:contact_id` | journeys (and broadcasts), api test sends, dispatch (SMS fallback, held messages, retries) → dispatch | `model.Message{id, tenant, contact, channel, template, ...}`; header `pulse-tries` on a retry |
| `delivery.receipts` | `tenant:provider message id` | api (provider webhooks) → dispatch | delivery receipts (delivered, read, failed, undeliverable, and from the gateway and sandbox also bounced / complained), with a reason code when the provider gives one |
| `<topic>.dlq` for each topic above | as the topic | a consumer that gave up on a record → `pulse replay-dlq` | the record as it was, plus an `error` header |

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
| `tags`, `points` | `tag_added`, `tag_removed`, `points_changed`, scoring rules | server identify events may also set them (imports add tags through `tag_added`) |
| `first_seen`, `last_seen` | every event | RFC 3339 time / UTC date |
| `stage`, `stage_changed_at` | `stage_changed {stage}` | from `PUT /v1/contacts/{id}/stage`, `POST /v1/events` (stage checked against the workspace's stages), the `change_stage` journey step, or a points threshold |
| `company_id`, `company_name`, `company_<key>` | `company_linked`, `company_updated`, `company_unlinked` | copies of the linked company's attributes, so segment rules on them work in both compilers |
| `merged_into` | `contact_merged_into` | the contact was merged into another; it leaves every segment |
| `date_added`, `date_identified`, `date_modified` | every event | RFC 3339 time the contact was created (contacts created before this get it at their next event, from `created_at`); RFC 3339 time of the first event with a non-`anon-` id or an email; UTC date of the latest change to any other attribute (a day, so at most one extra write per contact per day) |
| `email_domain` | any event that sets `email` | lower-case part after `@`; removed when the email is cleared |
| `device_type`, `device_os`, `device_os_version`, `device_brand`, `device_model`, `browser`, `app_version` | `identify`, `page_view`, `screen_view`, `session_start` | last-seen device from the event's flat props (`device_type`, `os`, `os_version`, `device_brand`, `device_model`, `browser`, `app_version`, each at most 100 characters). An event naming a device (`device_type` or `os`) replaces the whole device, so a phone's model never sticks to a desktop visit |
| `locale` | the same events (`props.locale`) | not reserved: a server or import may set it, and an event's own `attrs.locale` wins over its props |
| `owner_id`, `owner_name` | `owner_changed {owner_id, owner_name}` | only from `PUT /v1/contacts/{external_id}/owner {"user_id": N}` (0 unassigns); the user must belong to the workspace |
| `phone_country` | any event that sets `phone` (or the next event of a contact whose phone predates it) | ISO 3166 code of the number's country code (`+91…` → IN, `0044…` → GB), from `github.com/nyaruka/phonenumbers`; only numbers written with `+` or `00` are placed, a national number removes it |
| `optout_topic_<key>` | `unsubscribed` / `resubscribed` with `props.topic` | left a subscription topic: messages of that topic skip the contact; it stays in every segment |
| `<channel>_hard_bounces`, `<channel>_soft_bounces`, `<channel>_complaints` (email, sms, whatsapp, rcs) | bounce reports: `email_bounced {kind}`, `email_complained`, `<channel>_failed` / `_bounced` (by `reasons.BounceType` of `props.reason_code`; `props.bounce` = hard) | bounce rule counters (`profile/bounce.go`); a new `email` / `phone` resets that channel's |
| `email_bounced`, `sms_bounced`, `whatsapp_bounced`, `rcs_bounced`, `<channel>_bounced_address` | `<channel>_bounce_action {action: stop\|leave_segments}` (added by the profile when a rule fires) | the channel is suppressed for that address; sticky on merge; a server may clear it; a new address lifts it, changing back to the stopped address restores it |
| `segments_excluded`, `segments_excluded_by` | `<channel>_bounce_action {action: leave_segments}` | the contact leaves every dynamic segment (engine and backfill SQL) and every static one until a new address on that channel (or a server clears it) |

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
  If an identify arrives before the visitor's own first events (they can sit on another partition), the `anon-`
  visitor is created right then, already merged, so those events follow it to the known contact too.
- **Identity keys (batch O1).** Settings → Identity lists the fields that identify a person in priority order (external
  id, email, phone, any contact field), each "merge" or "suggest". `contact_keys` (pg migration 031) holds every
  contact's normalised value per key (emails and fields trimmed, lower case; phones as digits with the `+` kept;
  `identity.Value` and `pg.keyExpr` must agree), written by the profile role only for contacts whose values changed.
  Before the fold, `profile.resolveIdentity` looks up the merging values an event brings (all of them for a contact
  made for this event, otherwise only values it doesn't hold yet: one query per workspace per batch) and ranks the
  matches by key order, the event's own contact at the external id's place unless it is new. If the own contact wins,
  a `merge_requested {loser, source: identity, key}` per other match goes into the batch before the event. If another
  contact wins, the event and a `merge_requested` for the winner go back to `events.raw` under the winner's key,
  marked `rerouted` (never sent on twice), so a profile is only ever written by its own partition; the contact's other
  events in the batch follow. A key added later is filled in for existing contacts by `identity_backfills` (the
  maintenance role, 5,000 contacts a statement, starting 40 s after the change so the pipeline's 30-second settings
  cache already writes the key); contacts sharing a value then show under Duplicates instead of merging in bulk. An
  external id that is an email address or E.164 phone also fills an empty `email` / `phone`. Only server events take
  part: browser / app SDK events (`from_browser`, set by the API) and anonymous visitors (`anon-…`) never trigger a key
  merge and are never matched, since anyone can type any email into a page; the browser API also clears `rerouted`.

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

- Groups: `all`, `any` and `none` (matches when no child does; SQL `NOT (a OR b)`, exact because
  every leaf compiles to a non-NULL condition), nestable (8 levels, 200 nodes). Attribute ops: see the second "Segment rules" section below. Event ops: `eq ne gt gte lt lte` on `count`.
- **Window semantics:** `days: N` means *today plus the previous N−1 calendar days, in UTC*; `from`/`to`
  (instead of `days`) is an absolute range of UTC days, both included, cut at today (`segment.Window.Bounds`).
  Both compilers use these definitions. Counters keep 365 days (`segment.CounterDays`); a range starting
  earlier makes `Counter.Count` return `segment.ErrNeedsSweep`, and `Rule.SweepOnly(now)` keeps such a
  segment out of the streaming index (checked at every 2 s index refresh), so the sweep alone
  evaluates it. Journey `check` conditions refuse such ranges when saved.
- Compiler 1, **`segment.Eval`**: a Go predicate over (attrs, counter reader). It's used per event by the streaming path.
- Compiler 2, **`segment.ToSQL`**: ClickHouse SQL with bound parameters, which returns matching contact ids. It's used by backfill and sweeps.
- `segment.Refs` lists the event names and attribute keys a rule reads. The segment engine keeps an inverted index from refs to segments, so an
  event only triggers evaluation of the segments that could change.

- **Property paths.** A property filter's `prop` is a name, a dotted path into nested objects
  (`product.code`, up to 4 parts) or a path through one list (`items[].code`): true when any item
  passes; a missing, empty or non-list value is one missing value (`segment.ParsePath`, `MatchProps`).
  In SQL each path becomes variadic `JSONType`/`JSONExtract*(props, k1, k2…)` lookups; a list path is
  `if(empty(JSONExtractArrayRaw(props, …)), <Go's result for a missing value>, arrayExists(x -> …))`.
- **List fields** (`has` / `not_has`) read a JSON array (or a string holding one) like ClickHouse's
  `JSONExtract(x, 'Array(String)')`, scalars as their canonical strings (`segment.ListValues`).
  Date `between` takes two dates.
- **Daily refresh.** `segments.refresh` `{"mode":"daily","at":"HH:MM","tz":…}` (NULL = live). Daily
  segments stay out of the streaming index; `ClaimSegmentWork` claims one whose `next_refresh_at` has
  come (leasing it 10 minutes) after backfills and before sweeps, the engine rebuilds it with source
  `daily` (treated as live by journeys) and stores the next time (`Refresh.Next`); sweeps and the
  post-backfill catch-up skip daily segments.
- **Explain.** `segment.Explain` evaluates every node (no short cuts) for one contact with the live
  counters, for `GET /v1/contacts/{id}/segments/{id}/explain`.

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

- Triggers: `segment_entered` (optionally ignoring backfill), `event` (by name, with optional `where` property
  filters checked by `Trigger.MatchesEvent` with `segment.MatchProps`) and `form_submitted` (any form, or
  `form_id`; indexed under the `form_submitted` event and filtered on `props.form_id`; the submission is recorded on
  the enrollment for `if_form_field`).
- **Loading journeys.** Each journey worker keeps an index of active journeys (by trigger segment and event), rebuilt
  every 2 s. Every change through the API (activate, pause, edit, archive, delete) also `INCR`s `journeys:version` and
  publishes it on the Redis channel `journeys:changed`; workers reload at once and record the version they loaded in the
  hash `journeys:loaded` (instance → version and time). The API waits up to 3 s until every worker seen in the last 15 s
  has the new version before it answers (`journey.Announce`), so an activated journey takes the next matching event.
- **Archive and delete.** A journey nobody has entered can be deleted. One with enrollments is archived instead
  (`journeys.archived_at`): hidden from the app's list, refused by `activate` and left out of the workers' index, with
  its enrollments and reports kept; `unarchive` clears it. Archiving needs the journey paused.
- A step with no outgoing edge (`exit`, `jump`, `delete_contact`) ends its path; API-made graphs need no explicit
  `exit` after them.
- Nodes (`internal/journey/graph.go`, the builder's newer steps in `nodes.go` / `steps.go`). Outputs: actions and waits
  have `next`; decisions and conditions `yes` / `no`; splits one path per variant; `exit`, `jump` and `delete_contact` end
  the path.
  - **Actions:** `send_email`, `send_sms`, `send_whatsapp` and `send_rcs` (optional SMS fallback), `send_push`,
    `send_inbox`; `send_message` (marketing message: `channels` rules [{channel, template_id, fallback_template_id?,
    when?}] in order; `when` is segment property filters over `{contact: {email, external_id, ...attrs}, event: trigger
    props}`; the first rule whose conditions match and whose channel the contact can receive (an address or push device,
    consent, no bounce) sends; the choice counts as step output `ch:<channel>`; no rule matched records `message_skipped`,
    matched but none reachable `message_unreachable`, both `ch:none`); `notify_user` (email to users, the contact's owner or fixed addresses, the template rendered
    with the contact's data; the message carries `To`, so the dispatcher skips consent, caps, quiet hours and tracking and
    records `notification_sent` / `notification_failed`); `webhook` (`body` a text template with the variables, `| json`
    and `{{json .Attrs}}`); `update_attr` (values may use `{contactfield=x}`, the variables (trigger event included),
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
  - Journey settings: trigger (`segment_entered`, `event`, `form_submitted`), re-entry (`once`, `always`, `after_days`), goal event, schedule window.
- **Jumps.** A graph must be acyclic except through `jump` steps, whose target isn't an edge. `journey_enrollments.jumps`
  counts an enrollment's jumps; at 25 the next jump ends the enrollment (completed, `journey_jump_limit` recorded). Step
  keys are `{enrollment}:{step}` plus `:j{n}` after n jumps, so a step repeated after a jump sends (and records) again,
  and a message decision waits for that pass's message. A jump also moves the pending safety timer to the target.
- **Delete contact** uses the cleanup deletion path (`maintenance.Cleaner.DeleteContacts`: ClickHouse rows first, then
  Postgres: contact, devices, inbox, enrollments, form uploads), as does `DELETE /v1/contacts/{external_id}` (the
  profile's Delete contact button). The step needs `confirm: "DELETE"`; saving or activating a journey
  with it needs edit access to contacts, as does one with Remove Do Not Contact, Update contact owner, Update company,
  Add to company or Add to company's score (`contactSteps` in `internal/api/journeyrefs.go`; Add DNC only stops messages,
  so it needs nothing more). Events still in flight for that contact can create a new, empty profile.
- **Per-contact on-site content** (`internal/onsite/pushes.go`, Redis): `show_focus` adds the focus item to
  `ofocus:{tenant}:{contact}` (30 days); the decide API (`POST /v1/web/decide`) for a known — verified, when identity
  verification is on — contact takes and clears it and appends the item (any status: a draft item is journey-only).
  `push_dwc` sets `odwc:{tenant}:{contact}` slot → HTML for N days (default 30); the decide API returns it for that slot
  instead of the slot's own content. The decide API also records `dwc_requested {slot, url}` for each requested slot of a
  known contact, which `on_dwc` decisions wait for.
- **App content slots** (batch O3). A content slot with `platform: app` is served only by `POST /v1/web/content`
  (`{id, user_hash, platform: android|ios, slots}` → `{content: {slot: {card | html, variant}}}`), from the same
  few-second cache as the decide API. Segments count for `anon-` ids and ids with a valid user hash; contact-field rules
  and `{{…}}` in card texts use the contact only for a verified identity. Page-address and device rules don't apply
  (refused on save). Journey pushes for the slot win, and `dwc_requested {slot, platform: app}` is recorded as on the web.
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
  columns with an empty heading and turns a `tags` column into a `tag_added` event, so imported tags add to a contact's
  tags instead of replacing them. `POST /v1/contacts/import` (synchronous, 200 MB) stays for API clients.

## Dispatch
- Per-tenant, per-channel token bucket in Redis (messages/second, defaults in `dispatch.DefaultRates`: email 200, SMS 100,
  WhatsApp 80, RCS 50, push 500, inbox 2,000; set per server by `PULSE_EMAIL_RATE`, `PULSE_SMS_RATE`,
  `PULSE_WHATSAPP_RATE`, `PULSE_RCS_RATE`, `PULSE_PUSH_RATE`, `PULSE_INBOX_RATE`, merged by `dispatch.EffectiveRates`),
  plus per-provider limits and send deadlines (see "Campaigns (M3)"). The API returns the effective rates as
  `send_rates` in `GET /v1/channels/catalog`, so the provider form can show the cap and warn when a provider's own
  rate is higher (the slower of the two applies).
- **SMS bounces.** A provider error for the number itself (`providers.BadNumber`: Twilio 21211/21214/21612/21614, or
  an unreachable recipient, e.g. the sandbox's unreachable suffix) or a failed receipt marked unreachable (Twilio
  30003/30005/30006) records `sms_failed {bounce: true}`: a hard bounce for the bounce rules, which decide what follows
  (by default `sms_bounced`, so later SMS are `sms_suppressed` until the number changes). The dispatcher itself sets no
  attribute. A contact without a valid number is a plain failure, not a bounce.
- **Consent check** (`consent.SuppressReason`, the dispatcher and the journey send steps). A message is suppressed when
  the contact left its topic (`model.Message.Topic`), or opted out of the channel or of everything; with the
  workspace's email policy `segment`, email opt-outs don't suppress email (an email spam complaint,
  `email_complaints` > 0, still does). Then `<channel>_bounced` suppresses the channel (`bounced_before`).
- **SMS / RCS click tracking.** With `content.track_links` on the template (Content → Message templates, an SMS or RCS
  template, "Track link clicks"), the dispatcher replaces each http(s) link in the rendered text (and RCS `url` suggestions) with
  `{PULSE_PUBLIC_URL}/t/s/{code}`: 10 random base62 characters naming the message plus one naming the link (about 40
  characters in all; trailing punctuation stays outside the link). The message's tracking token and destinations are one
  Redis key per message (`slink:{code}`, 30 days). `GET /t/s/{code}` records `<channel>_clicked {url, message_id,
  template_id, journey_id, broadcast_id…}` once per message and link, then redirects (302); unknown or expired codes get
  404. WhatsApp is not covered: business-started WhatsApp messages are pre-approved templates whose button URLs live at
  Meta, so Pulse can't rewrite them.
- Templates are rendered by `internal/personalize` (see "Personalisation (M2)"): standard variables translated into Go
  `html/template` (email HTML, escaped per context) and `text/template` (every other field) actions, next to the older
  `{{.Attrs.x}}` syntax over `{Contact, Attrs, UnsubscribeURL, PreferencesURL}`. Click tracking runs on the rendered HTML,
  so links built from variables are tracked too.
- Providers (`internal/providers`): SMTP (any relay, SES included), the Gmail and Google Workspace SMTP presets and the
  Workspace SMTP relay (M5), Twilio, MSG91 and Gupshup (SMS), Meta WhatsApp Cloud API and Gupshup WhatsApp, Google RBM and
  Gupshup RCS, FCM, standard web push (VAPID), the sandbox, and the custom HTTP gateway for any channel (standard signed
  JSON or a custom request, with custom authentication; see "Channels and settings (M5)"). iOS push goes through FCM
  (no direct APNs).
- **Broadcast audiences** (`internal/broadcast`): a run (and an A/B winner step) loads the segment's Redis bitmap and
  clears the bits of every excluded segment (`exclude_segment_ids`, `broadcast.Exclude`), so exclusions cost one bitmap
  read each. A local-time broadcast (one-time only) runs when its clock time comes in UTC+14 and stamps each message
  with `local_at` (the clock time) and `local_zone` (the zone it was chosen in).
- **Local-time broadcasts.** A message with `local_at` (a clock time, set by a local-time broadcast) is held in the
  same Redis set as quiet hours (`deferred:messages`) until that time in the contact's zone (`timezone`, else
  `geo_timezone`, else the broadcast's `local_zone`), recording `<channel>_deferred` with the reason; then quiet hours and
  caps apply as usual. The binary embeds the time zone database (`time/tzdata`), so zones work in the slim image.
- Every send emits a `<channel>_sent` or `<channel>_failed` event (plus `_suppressed`, `_capped`, `_deferred`, `_fallback`)
  back into `events.raw`, so journeys and segments can react to delivery.
- **Retries (`internal/dispatch/retry.go`).** A provider failure is classified by the adapter: `providers.Permanent`
  (other 4xx, rejected template, bad credentials) and `Unreachable` fail at once; anything else (network error,
  timeout, 429, 5xx) is temporary. A temporarily failed message is not retried in place, which would hold up the
  partition and so other workspaces' messages: it is parked in the Redis sorted set `retry:messages`, scored by when
  to try again, and the dispatcher's 5-second loop (the same one that releases quiet-hours messages) re-publishes due
  ones to `messages.<channel>` with the record header `pulse-tries`. By default waits are 30 s, 1 min, 2 min, 5 min,
  10 min, 30 min and 1 h (`automation.BaseBackoff`, about 1 h 50 min in all), or the provider's `Retry-After` when
  longer (capped at the longest wait; `providers.RetryAfterError`). Since M5 each workspace sets the tries and the
  longest wait (Settings → Automation & limits: the schedule is cut, capped or extended by doubling,
  `automation.Retries.Schedule`). After the last try the message is recorded as `<channel>_failed` with
  `props.reason` "gave up after N tries: …", which reports count as failed. A retried message keeps its id and is
  marked sent (`sent:{tenant}:{id}`) only when it goes out, so it is sent once. For a push message with several
  devices, a retry happens only when no device got it.
- **Dead letters.** A record the dispatcher can't handle at all after 4 quick tries (undecodable, or Postgres or
  Redis down at that moment) goes to `<topic>.dlq` with an `error` header. `pulse replay-dlq <topic>` moves such
  records back once the cause is fixed (`--dry-run` lists them, `--limit N`), committing as it goes in its own
  consumer group, `pulse-replay-dlq`.

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
- `message_events`: message outcomes of broadcasts and journeys, filled by a materialized view (`message_events_mv4`
  since O4; mv3 since M4 added `complained`) from `events`, for reports; `reason_code` (M4) holds the outcome's reason
  code; `template_id` and `provider_id` (O4, ClickHouse migration 007, a `set` skip index on the template, the template
  backfilled once) feed template reports (`GET /v1/templates/{id}/report`) and the `template:` contact filter. The
  dispatcher records the provider a message went through as `props.provider_id` (`Message.SentVia`, kept in the receipt
  lookup so delivery reports carry it too). ClickHouse migrations run on every start, so 006 no longer creates mv3.
- `segment_changes` (O5, ClickHouse 008): every segment join and leave, from a view over `segment_members` inserts
  (the pipeline publishes changes only), kept 400 days, for the trigger segment's joined / left on campaign timelines.
  Journey step counts are also kept per UTC hour in Redis (`jsteps:<workspace>:<journey>:h:<yyyymmddhh>`, 400 days)
  beside the running totals; `GET /v1/journeys/{id}/timeline` folds them into the period's days or hours in the
  viewer's zone (half-hour zones split an hour by its start), with `journey_entered` / `journey_completed` from `events`
  and outcomes from `message_events`; `GET /v1/broadcasts/{id}/timeline` has the outcomes.
- `active_users_daily` (M4): `AggregatingMergeTree` of `uniqState(contact_id)` per workspace, UTC day, platform and UTM
  source, and `first_active` (the first active day per contact), both fed by materialized views and backfilled once
  (400 days), for the Users dashboard.

- `events.product_code` and `events.product_category`: MATERIALIZED columns from the props (migration
  005), for product reports.

Migrations: `migrations/postgres/001`–`028` and `migrations/clickhouse/001`–`006`, applied by `pulse migrate` (and `pulse all`) under a Postgres advisory lock, so processes that start together
migrate one at a time.

## Public pages and origins
- Landing pages (`/p/`), asset downloads (`/a/`) and hosted forms (`/f/`) are workspace-authored. With `PULSE_PAGES_URL` set they are
  served only from that origin (app-host requests redirect with 308), and that origin serves nothing but those pages, the SDK
  (`/sdk/`) and the browser API (`/v1/web/`). Session cookies are host-only, so pages never see them.
- Landing page HTML is sanitized by parsing it with the HTML5 algorithm (`golang.org/x/net/html`) and serializing the tree:
  scripts (HTML and SVG), event handlers, `srcdoc`, `<base>`, `<meta http-equiv>`, `<object>`, `<embed>` and javascript:/vbscript:/
  non-image data: URLs are removed. A nonce CSP allows only the Pulse SDK scripts.
- On Pulse-hosted pages the SDK namespaces its stored ids by site key (`storagePrefix`), so workspaces sharing the pages origin
  never share a visitor's identity.
- **Campaign archive** (O6, `internal/api/archive.go`): `/p/{workspace}/archive` (index), `/archive/{run}` (one send) and
  `/archive.rss` list sends of email campaigns with `archive_public`, rendered now from their template for an empty
  contact (no contact data; workspace blocks and the run's feed content), under the same nonce CSP and page rate limit.
  The slug `archive` is reserved for landing pages.

## RSS-to-email (O6)

A campaign with `feed_url` (email, not A/B) reads the feed (`internal/feed`, RSS 2.0 or Atom, through `safehttp`, 2 MB
cap) when a run starts. Items published after `broadcasts.feed_seen_at` (first run: one repeat interval back; a one-time
campaign takes the 10 newest) are rendered to HTML and text and kept on the run (`broadcast_runs.feed_html` /
`feed_text`), and `feed_seen_at` moves to the newest item, in one transaction. The dispatcher adds them to the run's
messages as `{{block.feed}}` (looked up by broadcast and scheduled time, cached an hour). A run with nothing new, or
whose feed can't be read, finishes with no recipients and the campaign moves to its next run, so a broken feed never
blocks the schedule; a retried run reuses the items it kept rather than reading the feed again. Changing the feed
address resets the mark.

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
- **Bounce mailboxes** (`internal/bounces`: `report.go`, `mailbox.go`, `imap.go`; `bounce_mailboxes`, migrations 017
  and 029). Up to 20 IMAP mailboxes per workspace (`id` primary key, `name`, `folders` jsonb: up to 10 `{name, kind}`,
  kind `auto` = read each message to tell, `bounces`, or `complaints` = every report in it counts as a complaint),
  edited at `/v1/settings/bounce-mailboxes[/{id}]` (settings edit access; `test` connects with unsaved settings and
  opens every folder, `{id}/check` makes one due now). The password is sealed with the server key and returned masked; a
  changed host, port, encryption or user needs it typed again. Plain connections are refused unless
  `PULSE_ALLOW_PRIVATE_WEBHOOKS` (development); connections go through `safehttp.Dialer` (public addresses only). The
  integrations role claims due mailboxes (`FOR UPDATE SKIP LOCKED`, every 5 minutes), reads its folders one after
  another (a failing folder doesn't stop the others), up to 200 unseen messages per folder
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
  read every event name in the range. **Done for DAU/WAU/MAU (M4):** `active_users_daily` and `first_active` pre-aggregate
  daily activity per workspace; cohorts don't use them yet.
- Attribution groups purchases by event id to drop pipeline duplicates and streams one row per attributed purchase to the
  API; a `purchases` table (ReplacingMergeTree by id, with the amount extracted) and a per-contact touch rollup would cut
  both.
- Breakdowns and attribute groups join the contacts snapshot; a narrow `(tenant, id, key, value)` table for frequently used
  attributes (or a dictionary) would avoid reading the attrs map.
- Report builder sums use events as stored, so a redelivered event can count twice until ClickHouse merges its copies.

## Event schemas (M1)

`internal/eventschema` describes the properties each event carries and checks events against them;
`internal/api/events.go` serves Audience → Events.

- **Storage.** `event_schemas(tenant_id, name, label, description, mode, props jsonb, updated_at)`
  (migration 020). A property: `key`, `path` (a property path, see segment rules), `type` (string,
  number, bool, date, enum, list, object), `required`, enum `values` with labels, `depends_on {prop,
  values}` (applies, and is required, only when another property has one of those values; one level,
  no list paths), `personal`, `add_to_list` (a custom list contact field; created by the API when missing, which needs segments
  edit; `eventschema.ListTargetAllowed` refuses Pulse's own, consent and identity fields, and the
  profile role checks it again before adding).
  `Schema.Normalize` validates and is run again by every cache load.
- **Cache.** `eventschema.Cache` holds every workspace's schemas in the roles that check events
  (profile, api). Like `scoring:v`, it reloads when the Redis key `eventschemas:v` changes (read at
  most once a second) or every 5 minutes; the API bumps it on every change and invalidates its own
  copy. An event without a schema costs one map lookup (the tenant's map is fetched once per batch).
- **Checks.** Modes `off` (documentation), `warn` (default) and `enforce`. The profile role checks
  every event that has a schema in warn or enforce mode (`Schema.Check`: missing, type, enum,
  not_applicable) and counts, per batch and pipeline, into `evv:{tenant}:{event}:{day}` (fields
  `checked`, `bad`, `refused`, `<code>:<prop>`; 35 days; days are when the check happened) and the
  latest 20 bad events into `evvs:{tenant}:{event}` (14 days; props without personal properties,
  cut at 2 KB). Counting is best effort (a retried batch can count twice) and never fails a batch.
  `POST /v1/events` refuses the whole request with 400 when an event doesn't fit a schema in enforce
  mode, counting it as `refused`; browser and SDK routes never refuse. Switching to enforce needs
  `confirm: "ENFORCE"`; any change to, or deletion of, a schema that is or becomes enforced needs
  settings edit.
- **List adds.** The profile role adds `add_to_list` values (canonical strings, deduplicated, the
  newest 200 kept) to the event's `attrs` before merging, so the change is ordered with the contact's
  other events like any attribute.
- **Catalog.** `GET /v1/events/catalog` (ClickHouse: count, `uniq(contact_id)`, `max(ts)` per name for
  30 days, cached 2 minutes in Redis, merged with schemas and 7-day problem totals) and
  `GET /v1/events/{name}/summary` (daily volume, properties discovered from the first 500 rows of 30
  days by `eventschema.Discover`, with example values hidden for personal properties, and 14 days of checks).
- **Starter schemas** (`eventschema.Suggested`): purchase (adds `product_code` and
  `items[].product_code` to `products`), signup and login (`method` → `social_provider`), add_to_cart,
  cart_abandoned, checkout_abandoned.

## Personalisation (M2)

`internal/personalize` fills templates; `internal/channel.RenderWith` applies it to every field of a template.

- **Syntax.** `{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`, `{{links.x}}`, with pipes (`default`, `upper`, `lower`,
  `title`, `date`, `money`, `number`, `truncate`, `urlencode`, `json`). `Translate` rewrites each such action into a Go
  template pipeline (`{{pulseVar "contact.x" | pulseDefault "there" | pulseOut}}`) and leaves every other action alone,
  so one parser handles both syntaxes and `html/template` escapes values in email HTML as before. In HTML, entities in
  a standard action are decoded first. Unknown pipes, `campaign`/`links` keys and bad paths are parse errors, so
  `channel.Validate` refuses them on save.
- **Values.** `contact.*`: `email`/`external_id` from the contact, else attributes (dotted paths, list indexes,
  `items[].x` joined). `event.*`: the message's trigger properties. `campaign.*`: from the message (`Campaign` name,
  `Step`, journey or broadcast id). `links.*`: the dispatcher's unsubscribe and preference URLs (email). Values print
  without exponents; lists join with ", ". A variable with no value and no default is recorded in `Rendered.Missing`.
- **Trigger context.** When an event or form submission enrolls a contact, the engine stores
  `eventschema.MessageProps(schema, props)` on `journey_enrollments.trigger` (migration 021): the props minus the schema's
  personal paths, at most 16 KB (largest top-level properties dropped first). Nothing is stored for an event marked
  `Unverified` (`messageTrigger`): `POST /v1/web/events` (browser and app SDKs) sets it on every event without a
  verified identity (`verifiedIdentity`), `POST /v1/events` clears it (the API key proves the sender), and it is never
  taken from a request body; otherwise anyone could put their text and links into a contact's messages by sending an
  event for that ID. The journey role keeps its own
  `eventschema.Cache` for this. Send steps (`send_*`, `send_message`, `notify_user`) of event and form journeys read it
  (`EnrollmentTrigger`) and put it on the Kafka message with the journey's name and the step (`model.Message.Event`,
  `Campaign`, `Step`); the SMS fallback copies them. Broadcasts set `Campaign` only.
- **Missing values.** `templates.missing_values`: `empty` (default) or `skip`. With `skip` and anything missing, the
  dispatcher records `<channel>_failed {reason: "skipped: no value for …", skipped: "missing_value"}` and sends nothing.
- **Webhook bodies** render with `Renderer.JSON`: every action, old syntax or new, ends in `pulseJSONEsc` (the
  template tree is rewritten, as html/template does), so values land JSON-escaped and must sit inside quotes; the
  `json` pipe and `{{json .x}}` return `JSONRaw`, inserted as it is. A body that isn't valid JSON once filled is not
  sent (the step fails with `webhook_failed`).
- **Elsewhere.** Webhook bodies, `push_dwc` HTML (`html/template`, escaped) and `update_attr` values render with the
  trigger event when the step runs. `POST /v1/web/inapp` fills in-app campaigns' title, body and buttons per app user
  (`inapp.Personalize`; contact variables only). Public routes use the contact's data only for a verified identity
  (`verifiedIdentity`: identity secret set and a valid user hash for a non-anonymous ID); otherwise defaults. A
  personalised `push_dwc` stores a filled and an anonymous version (`onsite.PersonalDWC`), and `POST /v1/web/decide`
  serves the anonymous one unless the visitor is verified (`onsite.ResolveDWC`).
- **Previews.** `POST /v1/templates/preview` and `POST /v1/channels/test` take sample `event` props (≤ 16 KB);
  `GET /v1/templates/sample-event` reads the contact's latest event of a name from ClickHouse (`LatestEvent`, personal
  properties removed; contacts view) or builds `eventschema.Example(schema)`.
- **Web.** `web/src/personalize.ts` mirrors the syntax (catalog, tag building, lint, sample fill for live previews);
  `VarPicker.tsx` is the picker and the `TemplatedInput` / `TemplatedText` fields used by every templated field.

## Campaigns (M3)

- **Hub.** `GET /v1/campaigns` lists broadcasts and journeys as one list (`api.CampaignRow`). The category is derived,
  never stored: a broadcast is `one_time`, a journey with an event or form trigger `trigger`, a segment-entered journey
  `automation`. Channels come from the journey's send steps and `send_message` rules (a broadcast's channel, plus SMS
  with a fallback); audience is the trigger or audience segment's `BITCOUNT`; sent and last activity come from one
  grouped ClickHouse query over `message_events` (`ch.CampaignTotals`). Filters (category, status, channel, tag, name or
  tag search) run in the API over the workspace's rows, which are few. The route needs no area; the handler lists
  broadcasts only to roles that view broadcasts and journeys only to roles that view journeys (403 with neither).
- **Tags.** `broadcasts.tags` and `journeys.tags` (`text[]`, migration 022), normalised by `model.NormalizeTags` (trimmed,
  case-insensitive duplicates dropped, 20 tags of 40 characters, no commas). `PUT /v1/{broadcasts|journeys}/{id}/tags`
  changes them in any status; `GET /v1/campaigns/tags` suggests the used ones, most used first.
- **One-time wizard.** The app saves the content as a template with `hidden = true` (`templates.hidden`: left out of
  `GET /v1/templates`, so of every picker; "save as reusable" clears it), then an ordinary broadcast; nothing on the
  server is wizard-specific. A broadcast may name extra audience segments (`include_segment_ids`, union with
  `segment_id`, at most 10 in all). Duplicating a broadcast copies a hidden template too.
- **Reachability bitmaps** (`internal/reach`). Per workspace, Redis bitmaps like segment bitmaps (bit N = contact N):
  `rch:{t}:known`, `email` (an address, not bounced), `phone` (valid E.164), `sms`, `whatsapp`, `rcs` (and not bounced on that channel), `push` (a
  device), and `out:{channel}` (opted out of the channel or of everything). The segments role sets a contact's bits from
  the profile snapshot on every enriched event that creates the contact or changes `email`, `phone`, `optout_*`,
  `*_bounced` or `merged_into` (`reach.Bits`, the dispatcher's own checks), in the same pass as segment bits. Device
  registration (API) sets `push`; a dead token removes it when it was the contact's last device. A rebuild from
  Postgres (`reach.Rebuilder`, keyset pages of 10,000 contacts plus `contact_devices`, bitmaps built in memory, swapped
  with `SET`+`RENAME`) runs for a workspace without `rch:{t}:built` (new workspaces, after a Redis loss) and then daily
  (the key's TTL), under a per-workspace `SETNX` lock; contacts changed while it ran (`updated_at`) are applied again
  after the swap. At 100M contacts the eleven bitmaps take about 140 MB of Redis per workspace.
- **Exact estimates.** `POST /v1/segments/estimate {include, exclude, channel, transactional}` runs one Redis pipeline of
  `BITOP`s over the workspace's keys (`reach.Count`): `OR` of the included segments, then the excluded ones removed,
  then the channel's opt-outs removed, then `AND` with the channel's address bitmap, counting after each step.
  A AND NOT B is computed as A XOR (A AND B), which pads shorter bitmaps correctly. Every key is built from the
  caller's workspace, and every segment id is checked to belong to it. With a frequency cap on the channel (and not
  transactional), the reachable bitmap is read once and each member's cap set counted (`ZCOUNT`, pipelined), exactly up
  to 50,000 members and from an evenly spread sample of 5,000 above (`approximate: true`). Results are cached for 30 s
  (`rchest:{t}:{hash}`); cache misses are limited to 60 a minute per workspace (429). `GET /v1/segments/{id}/reach`
  gives each channel's numbers for one segment (segment builder, journey Start panel), with the same cache and limit.
- **Per-provider limits** (`channel_providers.limits`: `rate_per_second`, `daily_quota`, `timezone`;
  `PUT /v1/channels/providers/{id}/limits`, channels edit). The dispatcher caches each workspace's default provider
  (and each provider a message chose, see [Provider per send](#after-m7-overview-summary-two-step-sign-in-provider-per-send))
  with its limits for 30 s. Before sending it takes one message from the provider's quota with a Lua script on
  `quota:{provider}:{yyyymmdd}` (the day in the provider's zone; expires after 3 days; shared by every dispatcher), then
  waits for the workspace's channel bucket and the provider's own bucket (`rlp:{provider}`). Over the quota, the
  message is held in `deferred:messages` until midnight in that zone (`<channel>_deferred`, kind `quota`); a message
  that went out to no target gives its unit back. `GET /v1/channels/providers` adds `usage_today`. Held messages are
  released up to 20,000 per 5-second round (they then meet the rate limits).
- **Send deadlines.** `model.Deadline{at "HH:MM", hours, timezone}` on broadcasts (`broadcasts.deadline`) and on send
  steps and `send_message` (`deadline` in the graph). A broadcast run stamps `Message.Deadline` = the earlier of the
  first `at` after the run's scheduled time and that time plus `hours`; a local-time run stamps `DeadlineLocal`, a clock
  time read in the contact's zone like `LocalAt`; a journey step stamps it from when the step runs. The dispatcher drops
  a message at or after its deadline, and any hold (quiet hours, local time, quota) that would release it later, as
  `<channel>_failed` with `reason: "deadline"` and `props.deadline`. Reports count them (`late`, from the event log).
- **Pooled SMTP** (`internal/providers/smtppool.go`). Connections are pooled per relay, login (password hashed) and
  limits in a process-wide registry, so adapters rebuilt every 30 s share them: at most `max_connections` (default 4)
  open per process, each used for `messages_per_connection` (default 100) messages, idle ones closed after 60 s.
  Encryption follows the provider's `tls` setting: automatic (STARTTLS whenever offered, implicit TLS on port 465),
  `starttls` (refused if the server doesn't offer it), `ssl` (implicit TLS on any port) or `none`; `skip_tls_verify`
  accepts any certificate. The login type (`auth_protocol`) is PLAIN, LOGIN or CRAM-MD5 (or none); PLAIN and LOGIN send
  the password only over TLS or to localhost. All of these are part of the pool's key, with the HELO name. A reply error is
  `RSET` and the connection kept; a network error or 421 closes it; a dropped idle connection is retried once on a new
  one before anything was sent. 5xx replies stay permanent failures. The benchmark against a fake relay with a 3 ms
  greeting: about 7,800 messages/s pooled against 260 with a connection per message.
- **Duplicates and paste.** `POST /v1/{broadcasts|journeys|templates|segments}/{id}/duplicate` create drafts named
  "… (copy)" (a journey copy is checked like a save, so a step needing contacts edit still needs it; static segments
  can't be duplicated). Journey steps are copied by the app as clipboard JSON (`journey.Clipboard`: `pulse`,
  `version`, `entry`, `nodes`); `POST /v1/journey-steps/check` decodes it strictly, drops edges and jumps to steps
  outside it and decisions watching a send step outside it, validates the steps as a journey save would, then clears
  references (templates, segments, lists, forms, assets, companies, journeys, users) that don't exist in the caller's
  workspace, with a note each. The app renames and inserts the steps (`web/src/journeyClipboard.ts`). Segment groups are
  copied as rule JSON and checked in the app against the rule limits (`web/src/segmentClipboard.ts`).
- **Test contacts** (`test_contacts`, at most 50 per workspace; `/v1/test-contacts`, content area). `POST
  /v1/channels/test` takes `test_contact_id`: one with an external ID sends to that contact as before; one with bare
  addresses sends a `Message` with `TestTo` and `TestName` and no contact, which the dispatcher renders with only the
  name, sends through the channel's provider without consent, caps, tracking or events.

## Delivery truth and dashboards (M4)

- **Message references** (`internal/tracking/ref.go`). Every email with tracking on carries `Codec.Ref`: `p1` +
  base64url of varint ids (workspace, contact, template, journey, broadcast, run, variant, message) + `.` + 10 bytes of
  HMAC-SHA256, usually 45–70 characters. Signed, not sealed: it holds ids only. It is the Message-ID's local part
  (`<ref@sender-domain>`), `X-Pulse-Ref`, and the sending service's own metadata header, chosen by the SMTP provider's
  **Sending service** setting (`config.service`, auto-detected from the host): SendGrid `X-SMTPAPI` unique_args, Mailgun
  `X-Mailgun-Variables`, Postmark `X-PM-Metadata-pulse_ref` (split into `pulse_ref2` past 80 characters), SES
  `X-SES-MESSAGE-TAGS`, SparkPost `X-MSYS-API`. The custom gateway gets `ref` in its JSON. Chosen over VERP: no
  return-path domain to set up, and every service echoes one of these.
- **Linking** (`bounces.Link`). A reference signed by this server *for the workspace whose secret URL received the
  report* names the message and its contact (tenant ids in bodies are never trusted); otherwise the contact with that
  address, unlinked. A bounce report records `email_bounced` with `kind` and `same_address` (whether the contact still
  has the bounced address); the bounce rules act only on the current address (`profile/bounce.go`). Linked
  events take ids from the message (`fb:{message}:{kind}`), so repeated webhooks record once; deliveries without a
  reference are dropped. Bounce emails (DSN) and feedback loops (ARF) are linked from the quoted original's
  `X-Pulse-Ref` or Message-ID. Bounces from before M4 stay unlinked.
- **Delivered.** SendGrid `delivered`, Postmark `Delivery`, Mailgun `delivered`, SES `Delivery`, SparkPost `delivery`
  and generic `{"type":"delivered"}` record `email_delivered`; SparkPost bounces and complaints are read too. Plain SMTP
  relays, Gmail / Workspace, FCM and web push report no delivery: `providers.ReportsDelivery` says so per provider and
  reports show "—".
- **Webhook verification.** The per-workspace secret URL picks the workspace. On top: SES through SNS is always verified
  (SNS signature v1/v2, certificate only from `sns.<region>.amazonaws.com`, cached); SendGrid's signed event webhook
  (ECDSA over timestamp + body, ±24 h) once its verification key is saved; Mailgun's HMAC (timestamp + token, ±24 h) once
  its signing key is saved, each token accepted once (Redis, 25 h); Postmark, SparkPost, generic posts and SES without
  SNS need HTTP basic auth once a user/password is saved. Keys live sealed in `tenants.email_webhook_keys` (migration pg
  023), set with `PUT /v1/settings/bounces/signing` and never returned. A workspace with no key saved is still accepted
  on the secret URL alone ("URL secret only"); **once any key is saved, the URL fails closed**: a format with no check of
  its own needs the basic auth password, else it is refused (the settings page shows "Refused").
- **Reason codes** (`internal/reasons`). Every `<channel>_failed`, `_suppressed`, `_capped`, `email_bounced` and
  `email_complained` carries `props.reason_code` next to the raw `props.reason`: invalid_number, invalid_email,
  no_address, not_on_channel, blocked, invalid_token, hard_bounce, soft_bounce, complaint, consent, bounced_before,
  capped, deadline, missing_value, template, no_provider, provider_auth, provider_rejected, spam_policy, rate_limited,
  timeout, provider_error, unknown. They are derived from the dispatcher's and adapters' error texts and Meta / Twilio
  codes; receipts may carry one (`Receipt.Code`). Skips group as consent, reachability, cap, missing_value and deadline.
  Rows from before M4 read "Not recorded".
- **ClickHouse** (migration ch 006): `message_events.reason_code`, `message_events_mv3` (adds `complained`; the old
  views' CREATE statements were removed from 003/004), `active_users_daily` and `first_active` (see "ClickHouse
  tables"). Active means `analytics.ActiveCondition`: not identify, message outcomes or bookkeeping (a test checks the
  migration uses the same text). WAU/MAU merge 7/30 days of states in one `ARRAY JOIN range(30)` query.
  `first_active` rows go with deleted contacts; the uniq states can't drop one contact, so DAU history keeps counting
  deleted contacts.
- **Funnels.** Broadcast and journey reports return `funnel` (`ch.BuildFunnel`) and `reasons` (and `reasons_by_node`
  for journeys): audience at send time (broadcasts: `reach.Count` for the channel stored on `broadcast_runs.audience`
  when a run starts, migration pg 023; journeys: enrolled), targeted, skipped by group, sent, delivered, opened / read,
  clicked, downloads (clicks on `/a/{key}` assets or links ending in .pdf, .zip, .docx…, counted in clicked too),
  bounced (hard / soft), complaints, failed and unsubscribed.
- **Dashboards** (`GET /v1/analytics/users|campaigns|products`, reports view). Cached a minute in Redis
  (`dash:{tenant}:…`); cache misses limited to 60 a minute per workspace (`rldash`). Campaign performance credits
  revenue within 7 days of a recipient's first message, and shows a campaign's name only to roles that may view that
  kind of campaign (the contact page's message list does the same). Products read `product_code` / `product_category`
  with view event `product_viewed`, cart `add_to_cart` (changeable on the page) and the revenue event; abandoned =
  added to cart and not bought in the range. CSV exports are built in the browser.
- **Live feed** (`internal/live`). The ClickHouse sink appends stored events to a capped Redis stream per workspace
  (`live:{tenant}`, about 1,000 entries or the workspace's own size in `livek:{tenant}`, at most 50 events/s per
  workspace, props over 4 KB dropped); `GET /v1/live/events?cursor=` reads it (contacts view, 120 polls a minute per
  workspace) and removes schema-personal properties. Polling every 2 s rather than SSE: no long-lived connections
  through proxies, and the cursor makes it lossless within the cap.
- **Contact page** (`/contacts/:id`, contacts view): `GET /v1/contacts/{id}/profile` (reach per channel by the
  dispatcher's rules, devices, journeys with their step, 1/7/30/365-day activity, purchases, products), `/messages` (one
  row per message from the event log with status, reason, campaign, opens / clicks) and
  `/events?name=&kind=activity|pulse&before=`. Index `journey_enrollments (tenant_id, contact_id, updated_at)`
  (migration pg 023).

## Channels and settings (M5)

- **Custom HTTP authentication** (`internal/httpauth`): `none`, `basic`, `bearer`, `api_key` (header or query, name
  configurable), `oauth2` (client credentials, and the refresh-token grant used by Gmail XOAUTH2; client secret in the
  body or as basic auth; scope, audience) and `login` (method, URL, headers and a body template with `{{secrets.x}}`,
  JSON-escaped in a JSON body and percent-encoded in a form or URL; the token read by JSON path, a response header (a
  leading `Bearer ` dropped) or a regular expression; expiry from a JSON path (seconds left, Unix s / ms, or RFC 3339),
  a fixed lifetime, or 1 hour). The token goes into a header (default `Authorization: Bearer {{token}}`), a query
  parameter or a top-level body field. Settings are JSON in the provider's `config.auth`; secrets (`auth_username`,
  `auth_password`, `auth_token`, `auth_api_key`, `auth_client_secret`, `auth_refresh_token` and every
  `{{secrets.x}}`) live with the provider's sealed secrets.
- **Token cache.** Redis `httpauth:{tenant}:{provider}:{fingerprint}` (the fingerprint hashes the auth settings and
  secrets, so a changed password never reuses a token), sealed with the server key, plus a per-process copy. A missing
  or nearly expired token (margin: a tenth of its lifetime, at most a minute) is fetched under a `SET NX` lock (15 s);
  other instances poll the cache for up to 8 s, then log in themselves. A 401 on the main request drops the token
  (compare-and-delete) and retries once. A login answering 4xx (not 429), or without the token where the settings say,
  is permanent (`provider_auth`); 5xx and network errors are retried like any send. Only adapters built by the
  dispatcher (and test sends) share the cache; Test connection and previews never touch it.
- **Host pinning and secrets** (`providers.Endpoints`, `CredentialSettingsChanged`). Secrets are merged key by key on
  update (an empty field keeps its saved value). When a provider is saved with any setting changed that could steer a
  secret — a URL setting including path and query, an SMTP host, the mode, the auth or request templates, receipt
  mappings, user names — its stored secrets are dropped and every secret must be entered again (the save is refused
  until they are; the response says `secrets_cleared`); harmless settings (sender name or number, pool sizes, DLT ids)
  don't. Login and token URLs go through `safehttp` (private addresses refused unless `PULSE_ALLOW_PRIVATE_WEBHOOKS`,
  which also allows plain http; otherwise https only), and every request that carries credentials (logins, token
  requests, the gateway, Gupshup) is wrapped in `safehttp.SameOrigin`: no redirect to another site. A template is
  refused in the scheme, user info, host or port of the login, token and custom request URLs, both on validation and
  when the request is built, so a secret or contact value can never choose, or leak through, the host that is called.
  Test connection with saved secrets works only for the saved settings, and its error text never contains a secret.
- **Custom-request mode** (`http_*` providers, `config.mode = custom`, template in `config.request`): method
  (GET/POST/PUT/PATCH), URL, headers, content type, body; a success rule (status range, default 200–299, plus an
  optional JSON path that must equal a value, or be present and not false); the provider message id by JSON path; a
  receipt mapping (list path, id path, status path, a status map to delivered / read / failed / undeliverable (SMS
  fallback) / bounced, reason and code paths). Templates take `{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}` and
  `{{message.x}}` (to, text, subject, html, amp, title, body, sender_id, dlt_template_id, template_name, params,
  suggestions, card, ref…; `personalize.MessageKeys`), from the message's personalisation context, which the dispatcher
  passes to providers (`Outbound.Vars`). JSON bodies use `Renderer.JSON`; form bodies and query values are percent-encoded
  value by value; a header value with a line break refuses the send. Answers: 404/410 unreachable, 429/5xx retried, a
  2xx outside the success rule permanent. **Receipts fail closed:** custom-mode receipts need the provider's
  `receipt_token` (a header, a query parameter, or the key of an HMAC-SHA256 of the body); without one every receipt
  is refused.
- **Preview and Test connection.** `POST /v1/channels/providers/preview` returns the exact request (method, URL, headers,
  body) for Pulse's test message or a template rendered for a contact, secrets masked and the token as `{{token}}`.
  `POST /v1/channels/providers/check-auth` runs the login or token request and returns the token masked and its expiry
  (20 a minute per workspace). Both need channels edit and settings edit and are audited with secrets redacted.
- **Email presets** (`internal/providers/gmail.go`): `gmail_smtp` (500/day, 1/s) and `gworkspace_smtp` (2,000/day, 2/s)
  — smtp.gmail.com 587 STARTTLS or 465 TLS with an app password or XOAUTH2 (OAuth client + refresh token → access token
  through `httpauth`, cached per provider; AUTH only over TLS) — and `gworkspace_relay` (10,000/day,
  smtp-relay.gmail.com, by IP with an EHLO domain, `helo_domain` → `SMTP.Helo`, or SMTP auth). A new provider starts
  with its kind's limits (`Kind.DefaultLimits`) unless limits are sent, else the workspace's defaults for the channel.
  Provider fields may carry `option_labels` (M7) so forms show options in words.
- **Gupshup** (`gupshup_sms`, `gupshup_whatsapp`, `gupshup_rcs`), from Gupshup's public docs and tested against mocks
  only (every base URL editable): SMS through the Enterprise gateway (`method=SendMessage`, TEXT / UNICODE_TEXT, DLT
  `principalEntityId` / `dltTemplateId` from the SMS template's `dlt_entity_id` / `dlt_template_id` or the provider's
  defaults), delivery reports by POST or GET (`providers.GetReceiver`); WhatsApp template and session messages with v2
  message events matched on `gsId`, Gupshup and Meta codes mapped to reason codes, inbound messages feeding reply
  keywords; RCS with an RBM-style `contentMessage` (inferred, the least certain part), falling back to SMS. Gupshup
  webhooks need the provider's `webhook_token` (`?token=` or `X-Pulse-Token`).
- **Automation settings** (`internal/automation`, `tenant_settings.automation` jsonb, migration pg 024; `GET|PUT
  /v1/settings/automation`, settings view / edit, audited). One document: retention per log type (events, message
  events, page / screen views, per event-name overrides, anonymous visitors, audit log, import history, webhook
  deliveries, integration log, sandbox test inbox, live feed size), retries (attempts, longest wait), schedules (sweep
  minutes — 0 = `PULSE_SWEEP_INTERVAL`, honoured in the claim SQL —, the UTC hour reachability is rebuilt, the default
  time and zone of new daily segments), app inbox expiry and default provider limits per channel. Missing fields take
  `automation.Defaults`, which equal the behaviour before M5. `POST /v1/settings/automation/preview` counts what a
  shorter retention would delete (cached 60 s; one uncached preview per workspace every 5 s);
  `POST /v1/settings/automation/run-retention` runs the cleanup now. `PUT /v1/settings/data` still edits the three old
  retention values, merged into the document.
- **Daily retention** (`internal/maintenance`). Each workspace is cleaned once a day in UTC hour `tenant % 24`
  (`automation.RetentionDue`; a workspace that missed its hour runs after 47 h), claimed in `retention_runs` so several
  workers never repeat it: one ClickHouse lightweight `DELETE … WHERE tenant_id = ? AND ts < cutoff AND name IN / NOT IN
  (…)` per group of event names (`Retention.CHPlan`; a per-name setting always wins over a type-wide delete), skipped
  when `SELECT 1 … LIMIT 1` finds nothing older; Postgres logs in `LIMIT 5000` batches. Workspaces that existed without a
  settings row keep their audit log forever (migration 024 writes `audit_days: 0`); new ones get 365 days.
- **Segment rebuild check** (`segengine.Rebuild`). A new segment's catch-up rebuild could read ClickHouse before a
  contact's latest identify had reached it and put back a contact the stream had just removed. Diffs of up to 20,000
  contacts of a streamed segment are now checked against the contacts' current profiles (Postgres) and counters
  (Redis), evaluated as the stream does, and only the changes that still hold are applied, bit by bit; larger diffs
  (a first backfill) replace the bitmap as before.

## AMP and on-site (M6)

- **Storage.** The AMP code is `templates.content.amp` and the builder's document `content.amp_blocks`; with blocks the
  API rebuilds the code on every save (`channel.PrepareAMP` → `amp.Doc.Build`), so the blocks are the source. No new
  columns.
- **Validator** (`internal/amp/validate.go`): a rules table over the HTML tokenizer with line numbers — doctype,
  `<html ⚡4email>` (or `amp4email`), `<meta charset="utf-8">` first in head, the runtime script, the boilerplate, only AMP
  component scripts from cdn.ampproject.org for the allowed components (accordion, anim, autocomplete, bind, carousel
  0.2, date-picker, fit-text, form, image-lightbox, inputmask, lightbox, list, selector, sidebar, timeago, mustache 0.2),
  each used component's script present, no other script, no event-handler attributes or `javascript:` URLs, no
  disallowed tags (img, iframe, video, link, svg…), one `<style amp-custom>`, no `!important` / `@import`, CSS ≤ 75,000
  bytes, sized https `amp-img`, forms `method="post"` with `action-xhr`, document ≤ 200 KB. Errors stop a save (400 "AMP
  part: line N: …"); warnings don't. Full validation stays Google's (AMP validator, Gmail sender registration).
- **Placeholders and tokens.** Forms post to `action-xhr="pulse:submit/<block>"` (amp-list may read
  `pulse:results/<block>`); any other target is refused. The dispatcher fills them per recipient with
  `https://<public URL>/v1/amp/<token>` (`amp.Fill`), the token a sealed `tracking.AMPToken` (workspace, contact, message,
  template, campaign ids, block, 60-day expiry, test flag; its own prefix, so it is never a tracking token). Without a
  server key an AMP part with forms is left out, as is one over 200 KB once personalised.
- **Rendering.** The AMP part renders with the HTML part's html/template renderer; amp-mustache `<template>`s are cut out
  before and put back after (`amp.Protect`), so Pulse variables don't work inside them. `Codec.InstrumentAMP` adds click
  tracking, the unsubscribe footer and an `<amp-img>` open pixel. `amp.PreviewHTML` makes the script-free preview.
- **MIME and providers.** `multipart/alternative` is text, AMP (`text/x-amp-html`), HTML (inside `multipart/mixed` with
  attachments). Every SMTP relay passes the part except Postmark (`providers.SupportsAMP`, shown in the editor per
  provider); the custom gateway gets `content.amp` and `{{message.amp}}`; the sandbox stores it.
- **Post-back** `POST /v1/amp/{token}` (public): token opened and expiry checked; CORS per the AMP for Email spec — v2
  `AMP-Email-Sender` → `AMP-Email-Allow-Sender`, or v1 `Origin` of a known email client plus `__amp_source_origin` →
  `Access-Control-Allow-Origin`, `AMP-Access-Control-Allow-Source-Origin`, `Access-Control-Expose-Headers`; the
  sender's domain must be a From domain of the workspace's email providers or `PULSE_SMTP_FROM`'s (cached a minute).
  Limits: 60 a minute per client IP and workspace (`rlamp`), 10 a minute per token. Polls: one answer per contact per
  poll (`amp:ans:{tenant}:{template}:{block}:{contact}`, `SETNX`, 400 days), counts in the hash
  `amp:poll:{tenant}:{template}:{block}`, event `amp_poll_answered`. Forms: `amp_form_submitted` with one property per
  field; fields with `save_to` update custom contact fields (`eventschema.ListTargetAllowed` rules: never identity,
  consent or Pulse's own); the event id hashes token and values, so a replay records once. Test tokens answer without
  recording. `GET /v1/amp/{token}/results` serves `items` for `amp-list`. Both events are platform events (refused on
  `POST /v1/events`) with starter schemas.
- **Editor routes.** `POST /v1/templates/amp/check` (content view) validates code or builds blocks and returns problems
  with lines, size, the preview and the providers' AMP support; the template preview returns `amp` / `amp_preview`;
  `GET /v1/templates/{id}/amp/results` gives poll counts.
- **Content variants** (`model.DWCVariant` in `dwc_slots.variants`): id, name, audience (`model.OnsiteAudience`: segments
  in, segments out, page URL contains / doesn't contain, devices, contact-field conditions) and HTML; array order is the
  priority, the first match wins, else the default (`onsite.DecideFor`). Contact-field conditions match only verified
  identities. The SDK sends `device` to `/v1/web/decide`, which also returns `variants` (slot → variant id).
- **Never shown and overlap.** `onsite.Shadowed` finds variants an earlier one covers, from the rules alone (returned by
  GET and save, mirrored live in the editor). `POST /v1/dwc/overlap` and `POST /v1/focus-items/overlap` run
  `onsite.CountOverlap`: one Redis pipeline of BITOPs into temporary `ovltmp:*` keys (per variant audience, pair ANDs,
  who would see each in order, contacts in more than one, the default's share; page, device and field rules aren't
  counted, so "approximate"), cached a minute under `ovl:{tenant}:{hash}`, 30 uncached counts a minute per workspace.
- **Focus items** take the same audiences and a priority (`targeting.priority`; `PUT /v1/focus-items/order`). The SDK
  shows one pop-up, one bar and one slide-in per page, the first whose frequency allows it.
- **Web push prompt** (`internal/webcheck`, `tenant_settings.webpush`, migration pg 025; `GET|PUT
  /v1/settings/web-push`, channels view / edit): bell, bar or pop-up, texts and colours, triggers, ask-again days,
  pages, the site address and service-worker path; `/v1/web/push-config` returns the prompt and service worker.
- **Install check** `POST /v1/settings/web-push/check` (channels edit, 10 a minute): `webcheck.Fetcher` fetches the page
  and the service worker through `safehttp` wrapped in `safehttp.SameOrigin` (public addresses checked per connection,
  https only, redirects only within the site, 1 MB, 10 s; private and http only with `PULSE_ALLOW_PRIVATE_WEBHOOKS`).
  **Browser self-check:** `POST /v1/settings/web-push/check-token` stores a 30-minute token (`pchk:{token}` → workspace)
  for a `?pulse_check=` link; the SDK posts `Pulse.diagnose()` to `POST /v1/web/diagnose` (stored in `pchkr:{token}`),
  and the app polls `GET /v1/settings/web-push/diagnosis`.

## Contacts behind every number and one campaign model (M7)

- **Set and outcome filters** (`internal/api/contactsets.go`). `GET /v1/contacts?q=` takes, besides the batch 9
  filters: `in:` (any of the listed segments; several `in:` intersect), `not_in:`, `subscribed:<channel>`,
  `reachable:<channel>`, `under_cap:<channel>`, `journey:` with `at:<step>` and
  `journey_status:in_progress|completed|converted|exited|active|waiting`, `campaign:broadcast/<id>` or
  `campaign:journey/<id>` with `run:`, `step:`, `outcome:` and `reason:<code>`, `event:` / `no_event:` with `product:` /
  `category:`, `active:any|web|android|ios|other|new|returning` with `utm:`, `amp_poll:<template>/<block>` with
  `answer:`, `form:<id>`, and `period:<from>..<to>` (UTC days, both included). Each comes back as a chip label in words;
  wrong combinations are refused with the reason. The export takes the same filters.
- **Two engines.** Set filters run in Redis on the bitmaps the numbers come from (segment bitmaps, the reach bitmaps; a
  journey's enrollments read from Postgres by `JourneyContacts` and streamed into a bitmap): one `BITOP` pipeline per
  view, cached a minute under `cvset:{tenant}:{hash}`, paged newest first by `GETRANGE` chunks (8 KB growing to 1 MB),
  counted by `BITCOUNT`. Outcome, event, activity, poll and form filters are ClickHouse subqueries over
  `message_events` / `events` / `first_active`, each with `tenant_id = ?`. A view with both checks the bitmap's
  candidates in ClickHouse 5,000 at a time (`ContactFilter`); its count is exact up to 10,000 candidates (50,000 for the
  frequency cap alone) and sampled above ("about"). Bitmap builds and first pages are limited to 120 a minute per
  workspace (`rlview`). Where a list can differ from its number the response carries a `note`.
- **Permissions** (`viewFilterNeeds` in `perms.go`): contacts view, plus broadcasts or reports view for
  `campaign:broadcast`, triggered & automation campaigns (journeys area) or reports view for `campaign:journey` and `journey:`, reports view for outcomes across
  every campaign, content or reports view for `amp_poll:` and `form:`. The app mirrors the rules (`web/src/contactLinks.ts`)
  and leaves a number unlinked with a tooltip.
- **Create segment from this list.** `POST /v1/contacts/view-segment {q, name}` (segments edit plus the view's needs;
  audited; 10 a minute and 2 fills at a time per workspace) makes a static segment and adds the list's contacts (up to 500,000) in the
  background as `list_joined` events, from a 15-minute copy of the view's bitmap (`cvset:*:snap`).
- **Campaign navigation.** One sidebar entry, Campaigns; the types One-time (broadcasts), Triggered (event or form
  journeys) and Automation (segment journeys) as in the M3 hub. `/journeys` and `/broadcasts` answer 301 from the server
  (`api.OldCampaignLists`) and redirect in the app to `/campaigns` and `/campaigns?tab=one_time`; `/campaigns/new` opens
  the New campaign chooser (`/journeys/new?start=event|form|segment` for the builder); `/journeys/:id` and
  `/broadcasts/:id` are unchanged. Role areas keep their keys (`broadcasts`, `journeys`) and are labelled "One-time
  campaigns" and "Triggered & automation campaigns".
- **Events catalog.** "Last 30 days" is 30 whole UTC days, today included, so its contact count equals the list it opens.

## After M7: Overview summary, two-step sign-in, provider per send

- **Overview summary** (`internal/api/overview.go`, `store/pg/overview.go`, `store/ch/overview.go`; migration pg 028).
  `GET /v1/stats/summary` (anyone signed in) builds the Overview's slower part: contacts added (today, last 7 days, the
  7 before) and unsubscribes, revenue from the workspace's revenue event, one-time campaigns sending, coming up and
  recently sent with their counts, and "Needs attention" warnings (a provider near its daily quota, a channel failing in
  the last hour, an email bounce spike, a campaign run with many failures). It is built once a minute per workspace
  (`dash:{tenant}:summary` in Redis, 15 s when a part was missing), concurrent misses in a process wait for one build
  (10 s timeout), and uncached builds are limited to 20 a minute per workspace (`rlsum`, 429). Each caller gets only
  the sections its role may view. Queries read one workspace's index ranges: `contacts_created`
  (`(tenant_id, created_at)`, partial on not-merged contacts) and `broadcasts_tenant` (`(tenant_id, status)`) in
  Postgres, a few event names in ClickHouse `events`, a few broadcast runs in `message_events`. The page shows the live
  numbers first and polls the summary every minute while its tab is visible.
- **Two-step sign-in** (`internal/auth/totp.go`, `internal/api/twofactor.go`, `store/pg/twofactor.go`; migration pg
  026). RFC 6238 codes (HMAC-SHA1, 30 s, 6 digits, ±1 step, each step accepted once via `user_two_factor.last_step`).
  The secret (and a pending one shown as a QR code until confirmed) is sealed with `PULSE_SECRET_KEY`; ten recovery
  codes are kept as SHA-256 hashes and removed as used. Password sign-in of a user who has it on, or must set it up
  under the workspace policy (`tenant_settings.two_factor`: `optional`, `everyone`, or `some` roles / people), answers
  `two_factor: code|setup` with a sealed 10-minute `pulse_2fa` cookie bound to the user's two-step state and good for
  one sign-in; the session is created only by `/v1/auth/two-factor/verify` or `/enable`. Wrong codes are limited per
  person and count toward the sign-in limit. SSO sign-ins skip it (the identity provider decides). Admins with settings
  edit reset a teammate's setup (`DELETE /v1/users/{id}/two-factor`); the policy needs full admin. Web:
  `web/src/pages/Account.tsx` (Your account, `/account`), the code step in `Login.tsx`, the policy in Settings →
  Sign-in & keys, the Team column and **Reset two-step**.
- **Bounce rules** (`internal/automation/bounces.go`, `internal/profile/bounce.go`; the `bounces` section of
  `tenant_settings.automation`). Webhooks, mailboxes, provider receipts and the dispatcher only report what happened;
  the profile role classifies each report (`bounceOf`: `email_bounced` by `kind`, `email_complained`,
  `<channel>_failed|bounced` by `reasons.BounceType`), increments `<channel>_<type>` counters, and when a count equals
  the rule's `count` (and the report is about the current address) appends one follow-up event right after it:
  `unsubscribed {channel or all, source: bounce|complaint}` or `<channel>_bounce_action {channel, type, count, action,
  address}`, which sets `<channel>_bounced` (+ `_bounced_address`) and, for `leave_segments`, `segments_excluded`
  (+ `_by`). `delete` is done by the journeys role with the delete-contact step's deleter. A change of `email` / `phone`
  (`resetBounces`) clears the channel's counters, stop and exclusion, or restores the stop when it is back to the
  stopped address. Rules are read through a 30-second cache per workspace (defaults without a store). `whatsapp_bounced`
  and `rcs_bounced` also feed the reach bitmaps (rebuilt once through a new built marker), the contact page and
  `if_dnc`. `<channel>_bounce_action` is `event.PlatformOnly`; browsers and Zapier / Make hooks can't send
  `event.BounceReport` events.
- **Delivery by channel** (`store/ch/delivery.go`, `web/src/campaignDash.ts`). Funnels count hard / soft bounces and
  complaints on every channel by bounce type (failed rows included, skip groups not), so `bounced` is at least
  hard + soft; Campaign performance rows add complaints from failures (`rowCounts`) and the CSV has hard and soft
  bounces. Contacts: `outcome:hard_bounced|soft_bounced|bounced|complained` match reason codes by bounce type on any
  channel, and `via:<channel>` narrows `campaign:` / `outcome:` / `reason:` to one channel (`api/contactsets.go`).
- **Unsubscribes** (`internal/automation/unsubscribe.go`, the `unsubscribe` section; `api/tracking.go`,
  `api/preferences.go`; migration pg 030 adds `broadcasts.topic`). `link_mode` `confirm` or `one_click`: in one-click
  mode `GET /u/{token}` renders a form that a nonce'd inline script (CSP `script-src 'nonce-…'`) submits, so fetching
  the link alone changes nothing. RFC 8058 `POST /u/{token}` (`List-Unsubscribe=One-Click`) leaves the message's topic,
  else email. `email_policy` `enforce` or `segment` (see the consent check under Dispatch; `reach.Count` with
  `KeepOptedOut` keeps email opt-outs in the estimate and returns `opted_out`). Topics (at most 30, `key` not a channel
  name): `topic` on broadcasts, journey send nodes and `send_message`, and test sends; the tracking token carries it,
  so the unsubscribe page and one-click header leave the topic only; `consent.Updates` maps
  `unsubscribed|resubscribed {topic}` to `optout_topic_<key>`. `GET /v1/unsubscribe-topics` (any principal) feeds the
  web's `TopicPick`, the Contacts filter `unsubscribed:<channel|all|topic>` and the segment builder's "Unsubscribed from
  topic" fields. Web: `web/src/pages/BounceSettings.tsx` (Settings → Bounces & unsubscribes: bounce rules,
  unsubscribes, the bounce webhook panel, `BounceMailbox.tsx`).
- **Provider per send** (migration pg 027). `model.Message.ProviderID` sends through one of the channel's providers
  instead of the default: set from `broadcasts.provider_id` (`ON DELETE SET NULL`), a send step's or `send_message`
  rule's `provider_id`, `notify_user`'s (email) and `POST /v1/channels/test`'s. The API checks on save that the provider
  is the workspace's and of the right channel; the dispatcher (`buildVia`) loads it with `ProviderToSend`, caches a
  sender per `tenant:channel:provider`, and falls back to the default with a logged warning if it is gone or of another
  channel. An SMS fallback always uses the SMS default. Web: the **Send through** picker (`web/src/ProviderPick.tsx`,
  the default preselected, hidden when the channel has one provider) in the one-time campaign editor and New campaign
  wizard, the journey step inspector and the test-send dialog.
- **Guided provider forms.** Catalog fields carry `type` (`bool`, `number`, `textarea`), `advanced` (folded under "More
  settings") and `show_if` (shown only for some values of another setting; a hidden field is never required, see
  `Field.Shown`); kinds carry `guide` (numbered steps, e.g. Gmail's app password) and `presets` (the SMTP form's "Fill in
  for:" Amazon SES, SendGrid, Mailgun, Postmark, Mailjet, Brevo, SparkPost, Microsoft 365, Zoho Mail and Gmail, each
  with a note). SMTP's custom headers (`headers`, `Name: value` per line, at most 20) can't replace Pulse's own. The
  custom HTTP gateway's "Connect to a gateway's existing API" mode can be filled from pasted curl or `postman request`
  examples (`web/src/curlImport.ts`, in the browser only): the send request, optionally a login request and an example
  login answer, from which the token path is found.
