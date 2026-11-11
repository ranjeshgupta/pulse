# Marketing team requirements: gap analysis and plan

Source: the marketing team's sheet of problems with Mautic (29 rows) plus the product owner's brief
(2026-11-11): event properties, personalisation from events, custom HTTP authentication, Google SMTP
and Gupshup presets, and Settings for everything automatic.

**Owner's decisions:** "product" is both an event property and a contact field; campaign categories are
automatic by type (broadcast = One-time, event/form journey = Trigger based, segment journey =
Automation) plus free tags; AMP email gets an AMP part with validation and a visual block builder;
Gupshup gets SMS, WhatsApp and RCS.

**Defaults chosen for the open design questions:**

- Event schemas start in **warn** mode (violations are counted and shown, events are kept); **enforce**
  (the server API refuses bad events) must be switched on per event, with a confirmation. Browser
  events are never refused.
- Behaviour ranges older than 365 days are evaluated by the sweep, not in real time; the UI says so.
- Event properties copied into messages are capped at 16 KB, and properties the schema marks personal
  are left out. Only events from a proven sender (server API key, or a verified web/app identity) are
  copied; webhook bodies JSON-escape every value.
- The product contact field can be set by imports, servers and a journey step, and an event schema can
  add a property's value to a list field automatically (e.g. a purchase adds `product_code` to
  `products`).
- Gmail / Google Workspace SMTP are offered for low volume with their limits shown; bulk mail should go
  through the relay or a sending service.
- "Blocked" on WhatsApp is reported as "undeliverable (possibly blocked)": the APIs only give error codes.

## Status of the sheet's rows (checked against the code at 8b22c78)

| # | Requirement | Status | Milestone |
|---|---|---|---|
| 1 | Campaign types | ✅ M3: Campaigns hub with One-time / Trigger based / Automation (automatic) and free tags | M3 ✅ |
| 2 | One-time campaign per channel, audience inside | ✅ M3: a wizard per channel, content written inside the campaign | M3 ✅ |
| 3 | Dashboard by campaign / communication | Partial: per-campaign reports only | M4 |
| 4 | Reporting of everything | Partial: no failure reasons, deliverability, segment growth | M4 |
| 5 | Publish / unpublish times | Already (broadcasts, journeys, on-site, landing pages); ✅ M3: send deadlines on broadcasts and send steps | M3 ✅ |
| 6 | Wrong users get emails | Already (real-time membership, two compilers checked against each other) | M1 ✅ adds "why is this contact in this segment" |
| 7 | Audience vs sent vs bounced | Partial: bounces aren't linked to campaigns; no breakdown | M4 |
| 8 | Delivered / read / click / download / spam | Partial: no email delivered, complaints not in reports | M4 |
| 9 | Segment date from–to | ✅ M1: between / since dates on behaviour, "is between" on date fields | M1 ✅ |
| 10 | Reachable channels | ✅ M3: reachable by channel per segment and per campaign; M4 adds the per-contact matrix | M3 ✅, M4 |
| 11 | Standard variable names | ✅ M2: `{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`, `{{links.x}}` with defaults and formats, a picker on every templated field | M2 ✅ |
| 12 | Web push / personalisation activation | Already | M6 adds a no-code opt-in prompt and install check |
| 13 | Deliverability per campaign | Partial (as row 7) | M4 |
| 14 | Live segment | Already | |
| 15 | Exclude in segmentation | Already | M1 ✅ adds a "none of" group |
| 16 | Audience count after every filter | ✅ M3: exact count after exclusions, consent, reachability and caps | M3 ✅ |
| 17 | Test WhatsApp / push | Already; ✅ M3: saved test contacts in every Send a test | M3 ✅ |
| 18 | Timely delivery, daily quota | ✅ M3: per-provider rates and daily quotas, send deadlines, pooled SMTP | M3 ✅ |
| 19 | A/B testing | Already | |
| 20 | DAU / WAU / MAU | Missing | M4 |
| 21 | Real-time activity | Partial: no live feed | M4 |
| 22 | WhatsApp / push delivered, read, blocked | Partial: no blocked class or reasons | M4 |
| 23 | Complete user profile | Partial | M4 |
| 24 | Tags in segmentation | Already | |
| 25 | Daily refresh time for segments | ✅ M1: Refresh daily at HH:MM in a time zone | M1 ✅ |
| 27 | Copy blocks | ✅ M3: copy/paste journey steps and segment groups; duplicate broadcasts, journeys, templates, segments | M3 ✅ |
| 28 | Web content by audience without overlap | Partial: first match wins, no ordering or overlap view | M6 |
| 29 | AMP mailers | Missing | M6 |
| 30 | User and product dashboards | Missing | M4 |

(The sheet has no row 26.)

## Milestones

Each milestone is built, tested (unit, `make it` when segment rules change, end-to-end twice), documented
with screenshots, and committed on its own.

1. **M1 Events and segments (done).** Event schemas (typed properties, enums, dependent enums such as
   signup method Social → provider), Data → Events page with volumes and violations, starter schemas
   (purchase, signup, login, add_to_cart, cart_abandoned, checkout_abandoned), nested property paths
   in segment rules, absolute date ranges, a "none of" group, list and enum contact fields (products),
   `phone_country`, property filters on event triggers, daily-refresh segments. New tutorial 12.
2. **M2 Personalisation (done).** Standard variables `{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`,
   `{{links.x}}` with defaults and formats, a picker on every templated field, the triggering event
   carried to every message of a journey, a missing-value policy, channel choice by condition in
   "Send marketing message" (Indian number → SMS, otherwise WhatsApp). Old syntax keeps working.
3. **M3 Campaigns (done).** A Campaigns hub with One-time / Trigger based / Automation tabs and tags, a
   one-time wizard per channel with content inline, an exact audience estimate (after exclusions,
   consent and reachability), duplicate and copy/paste of journey steps and segment groups, per-provider
   rates and daily quotas, a send deadline instead of late bursts, pooled SMTP connections, test
   contacts.
4. **M4 Delivery truth and dashboards.** Bounces and complaints linked to the message and campaign,
   email delivered from provider webhooks, failure reasons, the audience → sent → bounced breakdown,
   User / Campaign / Product dashboards with DAU/WAU/MAU, a live event feed, a full contact page
   (1/7/30/365-day activity, reachability, purchases, journeys, messages).
5. **M5 Channels and settings.** Custom HTTP authentication (basic, bearer, API key, OAuth2 client
   credentials, login request → token → main request, refresh on 401) and a custom-request mode;
   Gmail / Workspace SMTP (app password or XOAUTH2) and Workspace SMTP relay presets; Gupshup SMS,
   WhatsApp and RCS; Settings → Automation & limits for retention per log type and per event name,
   anonymous visitors, retries, schedules and quotas.
6. **M6 AMP and on-site.** AMP email part with validation, preview and a visual block builder
   (carousel, accordion, forms and polls that post back to Pulse), web content variants with rules,
   ordering and an overlap view, a no-code push opt-in prompt and install check.

## M1 as built

Built as designed, with these choices:

- **Event schemas** live in `event_schemas` and `internal/eventschema` (cache reloaded on `eventschemas:v`, like
  `scoring:v`). The profile role checks and counts (it sees every event, from every source); the API role only
  refuses, in enforce mode, on `POST /v1/events`. Checks are counted on the day they happen, not the event's time, so
  history sent late shows with today's problems. Turning on enforce needs `"confirm": "ENFORCE"` in the API too (not
  only the UI's typed confirmation) and settings edit access, because it changes what the API accepts; so
  does any change to, or deletion of, an enforced schema. A schema that creates a new list field needs segments
  edit, and can't target Pulse's own, consent or identity fields. An enforced
  event that doesn't fit refuses its whole request, so a batch is all or nothing.
- **Events page** is Audience → Events (next to Segments) rather than a new Data group: one more group for one page
  read worse. Pulse's own events (message outcomes, stage changes…) are listed too.
- **Ranges older than a year:** rather than a flag on the segment, the streaming index leaves out any rule whose range
  starts before what the counters hold (checked at every index refresh), and the Redis counter answers
  `ErrNeedsSweep`; the sweep evaluates those segments, `explain` marks such filters, journey `check` steps refuse them.
- **Daily segments** are built once when saved, then only at their time (the post-backfill catch-up is skipped for
  them); their membership changes are published with source `daily`, which journeys treat as live.
- **phone_country** uses `github.com/nyaruka/phonenumbers`; only numbers with + or 00 are placed (no default country).
- **List fields** are edited from the contact page with an `identify` event, so edits stay ordered with other events.
- **Event trigger filters** use the segment property filters (`where`) and their paths.
- Docs: [tutorial 12](tutorials/12-events-and-properties.md), tutorials 2 and 3, the API guide and the web SDK guide.

## M2 as built

Built as designed, with these choices:

- **Renderer.** The standard variables are translated into Go template actions (`internal/personalize`), so the older
  `{{.Attrs.x}}` syntax, `{{if}}` and friends keep working in the same field and HTML is still escaped by
  `html/template` for where each value lands. In HTML, entities inside a tag are decoded first (an editor may write
  `default:&quot;x&quot;`). `urlencode` output is marked as already encoded so links aren't encoded twice, and it can't
  form a `javascript:` link. Numbers print without exponents, lists join with ", ", objects as JSON. Pipes:
  `default`, `upper`, `lower`, `title`, `date` (Go layout, the contact's `timezone`/`geo_timezone`, a plain
  `YYYY-MM-DD` stays that day), `money` (INR by default, ₹ with lakh grouping; halves round up), `number[:decimals]`,
  `truncate:N` (ends with …), `urlencode`, and `json` (added for webhook bodies). Unknown pipes, `campaign.*` /
  `links.*` keys and unclosed quotes are refused on save.
- **Variables.** `contact.*` reads `email` and `external_id` from the contact and anything else from its attributes,
  with dotted paths into objects. `event.items.0.name` reads one item, `event.items[].name` every item (joined).
  `campaign.*` is `name`, `id`, `type` (journey, broadcast, test, inapp), `step`, `channel`, `template`; `links.*` is
  `unsubscribe` and `preferences` (email only; no web-view link exists yet).
- **Trigger context.** Migration 021 adds `journey_enrollments.trigger`: the event's (or form submission's) properties
  when it enrolls, without the schema's personal paths, at most 16 KB (largest top-level properties dropped first;
  `eventschema.MessageProps`). The journey role has its own schema cache for that. Send steps load it per step (only
  for event and form journeys) and put it on the Kafka message (`model.Message.Event`, with `Campaign` and `Step`), so
  the dispatcher stays stateless, waits and timers need nothing extra, and the SMS fallback carries it too. Contacts
  added by another journey (change journey) have no trigger event.
- **Missing values** are a template setting (`templates.missing_values`: `empty` default, or `skip`), not a step
  setting: the template is where the variables are. A skipped contact is recorded as `<channel>_failed` with
  `reason: "skipped: no value for …"` and `props.skipped = "missing_value"`, so reports count it under Failed without
  a new ClickHouse view (M4 adds failure reasons). Variables with a default and the older syntax never count as missing.
- **Picker and lint.** One catalog (`web/src/personalize.ts`) feeds the `{ } Variable` picker on every templated
  field, the rich-text **Personalize…** menu (now the same variables) and copy-to-clipboard in the visual builder
  (GrapesJS can't be typed into from outside). Templates don't know their journey, so the editor has a **Trigger event
  for {{event.…}}** choice (remembered per browser); journey steps use the journey's trigger event. Unknown contact
  fields and event properties are warnings (the value may still arrive); syntax mistakes are errors, mirrored by the
  server. Email has no preheader field, so there is none to personalise.
- **Preview and test.** `POST /v1/templates/preview` and `POST /v1/channels/test` take `event` (≤ 16 KB);
  `GET /v1/templates/sample-event?event=&external_id=` returns the contact's latest real event of that name (personal
  properties removed; needs contacts view), else an example built from the schema.
- **Channel by condition.** `send_message` channels became rules: each may have `when` (segment property filters on
  `contact.*` and `event.*`) and a `fallback_template_id`; the same channel may appear in several rules when conditions
  differ (at most 10 rules). The first rule whose conditions match and whose channel the contact can receive sends; a
  rule without conditions is the default. No match records `message_skipped`; matched but unreachable keeps
  `message_unreachable`. The chosen channel is counted as step output `ch:<channel>` (`ch:none`) and shown on the
  canvas. Old steps (no conditions) behave exactly as before.
- **Elsewhere.** Webhook bodies, `push_dwc` HTML (escaped) and `update_attr` values take the variables with the trigger
  event; in-app campaigns take contact variables per app user. Focus items aren't personalised per contact.
- **Public routes show contact data only to a verified identity.** `POST /v1/web/inapp` and `POST /v1/web/decide` are
  called with a site/app key and a person's ID, which anyone can send. Personalised in-app text and journey-pushed DWC
  use the contact's data only when `verifiedIdentity` holds: the workspace has an identity secret and the request's
  `user_hash` for that (non-`anon-`) ID checks out — stricter than `webIdentityOK`, which also passes when signed
  identify isn't required. Otherwise the variables take their defaults. A `push_dwc` step with variables stores both
  versions (`onsite.PersonalDWC`: filled, and filled with an empty contact and no event) and `decide` picks one
  (`onsite.ResolveDWC`). Web push and messages go to the contact's own address or device; landing pages and focus items
  aren't personalised, so no other public route renders `{{contact.x}}`.
- Docs: [tutorial 13](tutorials/13-personalising-messages.md), tutorials 5, 6, 7, 8 and 12, the API guide.

## M3 as built

Built as designed, with these choices:

- **Hub.** Campaigns → **All campaigns** (`/campaigns`) is a new entry above Journeys and Broadcasts, which stay as they
  were (their lists, editors and reports). Categories are derived on every request (`api.CampaignRow`), never stored.
  "Audience" is the audience or trigger segment's size now (one `BITCOUNT`), not a per-row estimate, so the list stays
  cheap; "sent" and "last activity" come from one grouped ClickHouse query. The route needs no area: the handler lists
  each kind only to roles that may view it (the nav entry shows with either). Tags are `text[]` on broadcasts and
  journeys, changed through their own `PUT …/tags` (allowed in any status, so live campaigns can be tagged), normalised
  like the server (case-insensitive duplicates dropped, 20 × 40 characters).
- **Wizard.** No wizard-specific server object: the content is saved as a template with `hidden = true` (kept out of
  every list and picker until "save as reusable"), then an ordinary broadcast. Broadcasts gained `include_segment_ids`
  (several audience segments, union). The broadcast editor got the same deadline, estimate and extra-segment fields and
  "Open in the wizard"; the template editor's body was extracted (`TemplateBody`) so both use the same editors with the
  M2 variable picker. Editing an existing one-time campaign in the wizard is possible while it is a draft, cancelled
  or completed.
- **Estimate.** Reachability is eleven bitmaps per workspace (`internal/reach`; known, email, phone, sms, push, and an
  opt-out bitmap per channel), maintained by the segments role from profile snapshots on events that change the
  relevant attributes, by device registration, and by a Postgres rebuild when missing and then daily (re-applying
  contacts changed during it). All maths is `BITOP` inside Redis (A AND NOT B = A XOR (A AND B)), so nothing big
  crosses the network except, with a frequency cap on the channel, the reachable bitmap: caps are per-contact sorted
  sets, so they are counted exactly up to 50,000 reachable contacts and from a 5,000-contact sample above (the UI says
  "about"). Quiet hours only delay, so they aren't a step. The same numbers appear in the wizard, the broadcast
  editor, the journey Start panel (segment journeys, per channel of its send steps) and the segment page (per
  channel). Cached 30 s per request; cache misses limited to 60 a minute per workspace.
- **Quotas.** Limits live on the provider (`channel_providers.limits`) and are set with their own route and channels
  edit access (provider credentials still need settings edit). The quota counts messages handed to the provider
  (refunded when none of a message's targets took it) per day in the provider's zone; over it, a message is held in
  the existing deferred set until midnight there, where it meets the rate limits again. Transactional messages are
  held too: the provider can't send more. Held messages are now released up to 20,000 per 5 s round instead of 500.
- **Deadline.** `at` (first HH:MM after the send starts, in a zone; the contact's zone for local-time broadcasts via
  `DeadlineLocal`) and/or `hours` late; the earlier wins. Checked when a message is handled and before any hold, so
  a quota or quiet-hours hold past the deadline drops at once. Dropped messages are `<channel>_failed` with
  `reason: "deadline"`; reports count them as `late` from the event log (no new ClickHouse view; M4 adds failure
  reasons).
- **SMTP pool.** Pools live in a process-wide registry keyed by relay, login and limits, since the dispatcher rebuilds
  adapters every 30 s; defaults 4 connections and 100 messages per connection; limits are per Pulse process.
  Benchmark (fake relay with a 3 ms greeting): about 7,800 messages/s pooled vs 260 unpooled.
- **Copy and paste.** Journey steps travel as clipboard JSON; the server checks them (`POST /v1/journey-steps/check`:
  settings validated as a save would, links and jumps outside the selection dropped, other workspaces' references
  cleared with notes) and the app inserts them (new ids, the old path continuing after the pasted main path). When
  the browser refuses clipboard access, a box opens to paste into (and a copy is kept in local storage).
  Segment groups are copied as rule JSON and checked in the app; the server checks them again on save. Static
  segments can't be duplicated (export and import instead).
- **Test contacts.** A test contact is either an existing contact (normal test send) or bare addresses; tests to bare
  addresses carry `TestTo`, are personalised with the name only, skip consent, caps and tracking, and record no
  events (no contact to record them on). At most 50 per workspace; content edit access, like test sends.
- Docs: [tutorial 14](tutorials/14-campaigns-hub-and-one-time-campaigns.md), tutorials 3, 4, 6, 7, the API guide.

## Risks

- Gmail shows AMP only for senders Google has registered (SPF, DKIM, DMARC); that is outside Pulse.
- Plain SMTP relays give no delivery webhooks, so "delivered" exists only for sending services; their
  bounces come only through the bounce mailbox. Bounces from before M4 can't be linked to campaigns.
- Gupshup endpoints and parameters must be confirmed with a real account; adapters are tested against
  mocks.
- At 100M contacts, audience estimates (Redis bitmap operations) are cached and rate-limited, and
  per-event-name retention runs as daily batched ClickHouse deletes.
