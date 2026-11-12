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
| 3 | Dashboard by campaign / communication | ✅ M4: Analytics → Campaign performance compares every campaign; each report has a funnel | M4 ✅ |
| 4 | Reporting of everything | ✅ M4: failure reasons, deliverability (delivered, bounces, complaints) per campaign and per contact; segment growth not built | M4 ✅ |
| 5 | Publish / unpublish times | Already (broadcasts, journeys, on-site, landing pages); ✅ M3: send deadlines on broadcasts and send steps | M3 ✅ |
| 6 | Wrong users get emails | Already (real-time membership, two compilers checked against each other) | M1 ✅ adds "why is this contact in this segment" |
| 7 | Audience vs sent vs bounced | ✅ M4: audience at send time → targeted → sent → delivered → opened → clicked, with skips, bounces and failures by reason | M4 ✅ |
| 8 | Delivered / read / click / download / spam | ✅ M4: email delivered from SendGrid / Mailgun / Postmark / SES / SparkPost webhooks, downloads (file-link clicks), complaints in reports | M4 ✅ |
| 9 | Segment date from–to | ✅ M1: between / since dates on behaviour, "is between" on date fields | M1 ✅ |
| 10 | Reachable channels | ✅ M3: reachable by channel per segment and per campaign; ✅ M4: the per-contact matrix on the contact page | M3 ✅, M4 ✅ |
| 11 | Standard variable names | ✅ M2: `{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`, `{{links.x}}` with defaults and formats, a picker on every templated field | M2 ✅ |
| 12 | Web push / personalisation activation | Already; ✅ M6: no-code opt-in prompt (bell, bar or pop-up) and an install check (server fetch + browser self-check) | M6 ✅ |
| 13 | Deliverability per campaign | ✅ M4 (as row 7) | M4 ✅ |
| 14 | Live segment | Already | |
| 15 | Exclude in segmentation | Already | M1 ✅ adds a "none of" group |
| 16 | Audience count after every filter | ✅ M3: exact count after exclusions, consent, reachability and caps | M3 ✅ |
| 17 | Test WhatsApp / push | Already; ✅ M3: saved test contacts in every Send a test | M3 ✅ |
| 18 | Timely delivery, daily quota | ✅ M3: per-provider rates and daily quotas, send deadlines, pooled SMTP; ✅ M5: preset quotas (Gmail, Workspace, relay) and workspace default limits | M3 ✅, M5 ✅ |
| 19 | A/B testing | Already | |
| 20 | DAU / WAU / MAU | ✅ M4: Analytics → Users (DAU / WAU / MAU, stickiness, new vs returning, platform, source) | M4 ✅ |
| 21 | Real-time activity | ✅ M4: Analytics → Live events | M4 ✅ |
| 22 | WhatsApp / push delivered, read, blocked | ✅ M4: WhatsApp delivered / read / failed with reason classes ("undeliverable (possibly blocked)"); push failures classed (FCM reports no delivery) | M4 ✅ |
| 23 | Complete user profile | ✅ M4: full contact page | M4 ✅ |
| 24 | Tags in segmentation | Already | |
| 25 | Daily refresh time for segments | ✅ M1: Refresh daily at HH:MM in a time zone | M1 ✅ |
| 27 | Copy blocks | ✅ M3: copy/paste journey steps and segment groups; duplicate broadcasts, journeys, templates, segments | M3 ✅ |
| 28 | Web content by audience without overlap | ✅ M6: ordered variants with segment, page, device and contact-field rules, never-shown warnings and an overlap view; focus items ordered, one per kind at a time | M6 ✅ |
| 29 | AMP mailers | ✅ M6: AMP part with validation, a visual block builder (carousel, accordion, gallery, polls, forms posting back to Pulse), preview and test send | M6 ✅ |
| 30 | User and product dashboards | ✅ M4: Analytics → Users and Products | M4 ✅ |

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
4. **M4 Delivery truth and dashboards (done).** Bounces and complaints linked to the message and campaign,
   email delivered from provider webhooks, failure reasons, the audience → sent → bounced breakdown,
   User / Campaign / Product dashboards with DAU/WAU/MAU, a live event feed, a full contact page
   (1/7/30/365-day activity, reachability, purchases, journeys, messages).
5. **M5 Channels and settings (done).** Custom HTTP authentication (basic, bearer, API key, OAuth2 client
   credentials, login request → token → main request, refresh on 401) and a custom-request mode;
   Gmail / Workspace SMTP (app password or XOAUTH2) and Workspace SMTP relay presets; Gupshup SMS,
   WhatsApp and RCS; Settings → Automation & limits for retention per log type and per event name,
   anonymous visitors, retries, schedules and quotas.
6. **M6 AMP and on-site (done).** AMP email part with validation, preview and a visual block builder
   (carousel, accordion, forms and polls that post back to Pulse), web content variants with rules,
   ordering and an overlap view, a no-code push opt-in prompt and install check.

7. **M7 Clarity review (done)** (owner's request, 2026-11-11; after M6, before the final docs pass). Every
   contact count anywhere (segments, audience estimate steps, reach by channel, funnels, journey
   steps, dashboards, reports, product buyers) opens the contact list filtered to exactly those
   contacts. One clear campaign navigation instead of "All campaigns" next to separate Journeys and
   Broadcasts entries. A review of everything built for wording, duplicate entry points and unclear
   states that would confuse non-technical marketers, and fixes for all of it.

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

## M4 as built

Built as designed, with these choices:

- **Message references.** Every email with tracking on carries a signed reference (`tracking.Codec.Ref`: `p1` +
  base64url of varint ids — workspace, contact, template, journey, broadcast, run, variant, message id (rebuilt for
  broadcasts) — + 10 bytes of HMAC; usually 45-70 characters). It is the Message-ID's local part
  (`<ref@sender-domain>`), `X-Pulse-Ref`, and the sending service's own metadata header, chosen by the SMTP
  provider's new **Sending service** setting (auto-detected from the host: `smtp.sendgrid.net`, `*.mailgun.org`,
  `*.postmarkapp.com`, `email-smtp.*.amazonaws.com`, `*.sparkpostmail.com`): SendGrid `X-SMTPAPI` unique_args, Mailgun
  `X-Mailgun-Variables`, Postmark `X-PM-Metadata-pulse_ref` (split into `pulse_ref2` past 80 characters), SES
  `X-SES-MESSAGE-TAGS`, SparkPost `X-MSYS-API`. Signed, not sealed: it holds ids only. The custom gateway gets `ref` in
  its JSON. Chosen over VERP: no return-path domain to set up, and every service echoes one of these.
- **Linking.** `bounces.Link`: a reference signed by this server *for the workspace whose secret URL received the
  report* names the message and its contact (tenant ids in bodies are never trusted); otherwise the contact with that
  address, unlinked. A hard bounce marks `email_bounced` only when the contact still has the bounced address. Linked
  events get ids from the message (`fb:{message}:{kind}`), so repeated webhooks record once. Deliveries without a
  reference are dropped (they would count nowhere). Bounce emails (DSN) and feedback loops (ARF) are linked from the
  quoted original's `X-Pulse-Ref` or Message-ID. **Bounces from before M4 stay unlinked** (no reference was sent).
- **Delivered.** SendGrid `delivered`, Postmark `Delivery`, Mailgun `delivered`, SES `Delivery`, SparkPost `delivery`
  and generic `{"type":"delivered"}` record `email_delivered`; SparkPost bounces/complaints were added (hard classes 10,
  25, 30, 90). Plain SMTP relays report no delivery: `providers.ReportsDelivery` says so per provider, and the Campaign
  performance page shows "—" ("not reported by this provider"). FCM and web push report no delivery either; push
  failures are classed.
- **Webhook verification.** The per-workspace secret URL picks the workspace. On top: SES through SNS is always
  verified (SNS signature v1/v2, certificate only from `sns.<region>.amazonaws.com`, cached); SendGrid's signed event
  webhook (ECDSA over timestamp + body, ±24 h) once its verification key is saved; Mailgun's HMAC (timestamp + token,
  ±24 h) once its signing key is saved — Mailgun doesn't sign the body, so each token is accepted once (Redis, 25 h);
  Postmark, SparkPost, generic posts and SES-without-SNS need HTTP basic auth once a user/password is saved. Keys live
  sealed in `tenants.email_webhook_keys` (migration 023), set with `PUT /v1/settings/bounces/signing` (settings edit;
  never returned). Deviation: a provider without a saved key is still accepted on the secret URL alone (as before M4),
  so existing setups keep working; the settings page shows "URL secret only" for those.
- **Reason codes** (`internal/reasons`): every `<channel>_failed`, `_suppressed`, `_capped`, `email_bounced` and
  `email_complained` carries `props.reason_code` next to the raw `props.reason`. Codes: invalid_number, invalid_email,
  no_address, not_on_channel, blocked ("Undeliverable (possibly blocked)", WhatsApp 131026), invalid_token, hard_bounce,
  soft_bounce, complaint, consent, bounced_before, capped, deadline, missing_value, template, no_provider, provider_auth,
  provider_rejected, spam_policy (WhatsApp 131048/131049, Twilio 30007), rate_limited, timeout, provider_error, unknown.
  They're derived from the dispatcher's and adapters' own error texts (and Meta/Twilio codes); receipts may carry a
  code (`Receipt.Code`; Meta receipts set it). Skips group as consent, reachability, cap, missing_value, deadline.
  Old rows read "Not recorded".
- **ClickHouse** (migration 006): `message_events.reason_code`, `message_events_mv3` (adds `complained`; the old
  views' CREATE statements were removed from 003/004 and 003's backfill names its columns), `active_users_daily`
  (AggregatingMergeTree of `uniqState(contact_id)` per workspace, UTC day, platform, UTM source) and `first_active`
  (min day per contact), both backfilled once (400 days) and fed by views. WAU/MAU merge 7/30 days of states in one
  `ARRAY JOIN range(30)` query. Active = `analytics.ActiveCondition` (not identify, message outcomes/engagement or
  bookkeeping; a test checks the migration uses the same text). `first_active` rows go with deleted contacts; the
  uniq states can't drop one contact, so DAU history keeps counting deleted contacts.
- **Funnel.** Reports return `funnel` (`ch.BuildFunnel`) and `reasons` (and `reasons_by_node` for journeys): audience
  at send time (broadcasts: `reach.Count` for the broadcast's channel stored on `broadcast_runs.audience` when a run
  starts; summed over runs; journeys: enrolled), targeted, skipped by group, sent, delivered, opened/read, clicked,
  downloads (clicks on `/a/{key}` assets or links ending in .pdf, .zip, .docx…, counted in clicked too), bounced
  (hard/soft), complaints, failed (tried), unsubscribed. Gateway and sandbox receipts may be `bounced` (kind hard/soft)
  or `complained`.
- **Dashboards** (`/v1/analytics/users|campaigns|products`, reports view; cached a minute, 60 misses/min per
  workspace). Campaign performance credits revenue within 7 days of a recipient's first message. Products read
  `product_code` / `product_category` with view event `product_viewed` and cart `add_to_cart` (changeable on the page)
  and the revenue event; abandoned = added to cart, didn't buy it in the range; "Segment of buyers" posts a segment
  rule (`event`/`where product_code`/`from`/`to`). New vs returning isn't split by platform or source. CSV exports are
  built in the browser.
- **Live feed.** The ClickHouse sink appends stored events to a capped Redis stream per workspace (`live:{tenant}`,
  ~1,000 entries, at most 50 events/s per workspace, props over 4 KB dropped); `GET /v1/live/events?cursor=` reads it
  (contacts view, 120 polls/min per workspace) and removes schema-personal properties. Polling every 2 s rather than
  SSE: no long-lived connections through proxies, and the cursor makes it lossless within the cap.
- **Contact page** `/contacts/:id` (contacts view): `GET /v1/contacts/{id}/profile` (reach matrix by the dispatcher's
  rules, devices, journeys with step, 1/7/30/365-day activity, purchases, products), `/messages` (one row per message
  from the event log with status, reason, campaign, opens/clicks) and `/events?name=&kind=activity|pulse&before=`.
  Index `journey_enrollments (tenant_id, contact_id, updated_at)` (migration 023).
- **Seed.** `make docs-seed` adds (once) the "Spring catalogue" email with delivered / bounced / complained / opened /
  clicked outcomes (written as the linked events webhooks record), a WhatsApp "Monsoon preview" with sandbox receipts
  (delivered, read, blocked, spam policy), product views and app screen views.
- **Docs** are deferred to the final docs pass (product owner, after M6); see the M4 report for the list.

## M5 as built

Built as designed, with these choices:

- **Custom HTTP authentication** is `internal/httpauth`: `none`, `basic`, `bearer`, `api_key` (header or query,
  name configurable), `oauth2` (client credentials; also the refresh-token grant, used by Gmail XOAUTH2; the client
  secret in the form body or as basic auth; scope, audience) and `login` (method, URL, headers and a body template
  with `{{secrets.x}}` — JSON-escaped in a JSON body, percent-encoded in a form or the URL; the token read by JSON path
  (`data.token`, `items[0].t`, `items.0.t`), a response header (a leading `Bearer ` dropped) or a regular expression
  (its group, or the whole match); expiry from a JSON path (seconds left, a Unix time in s or ms, or RFC 3339), a
  fixed lifetime, or unknown = 1 hour). The token goes into a header (template, default `Authorization: Bearer
  {{token}}`), a query parameter or a top-level body field (JSON object or form). The settings are JSON in the
  provider's `config.auth`; secrets live with the provider's sealed secrets (`auth_username`, `auth_password`,
  `auth_token`, `auth_api_key`, `auth_client_secret`, `auth_refresh_token` and every `{{secrets.x}}` the login
  names, each its own masked field in the form).
- **Token cache.** Redis key `httpauth:{tenant}:{provider}:{fingerprint}` (the fingerprint hashes the auth settings and
  secrets, so a changed password never reuses a token), value sealed with the server key. Each process also keeps its
  copy. A missing or nearly expired token (margin: a tenth of its lifetime, at most a minute) is fetched under a
  `SET NX` lock (15 s); other instances poll the cache for up to 8 s, then log in themselves. A 401 on the main
  request drops the token (compare-and-delete, so a token another instance just refreshed survives) and retries once;
  a second 401 is returned as is. A login or token endpoint answering 4xx (not 429) is permanent (`provider_auth`),
  and so is a login answer without the token where the settings say (`httpauth.ErrConfig`); 5xx and network errors
  are retried like any send. Only adapters built by the dispatcher (and test sends) share the cache; "Test connection"
  and previews never read or write it.
- **Host pinning** (`providers.Endpoints`): every URL setting (`url`, `*_url` except Twilio's `status_callback_url`,
  which is Pulse's), an SMTP `host`, the custom request's URL and the auth's login/token URLs, as scheme://host.
  When a provider is saved with a different set, its stored secrets are dropped and every secret must be entered again
  (the save is refused until they are; the response says `secrets_cleared`). Provider secrets are now **merged key by
  key** on update (an empty field keeps its saved value, as the form always said); before M5 any secret sent replaced
  them all. Login and token URLs go through `safehttp` (private addresses refused unless
  `PULSE_ALLOW_PRIVATE_WEBHOOKS`, which also allows plain http); otherwise https only. A `{{secrets.x}}` (or any
  template) is refused in the scheme, user info, host or port of the login URL and the OAuth2 token URL — only the path
  and query may hold one — both when the settings are validated and again when the request is built (the filled-in
  URL must keep the written origin), so a secret can never choose, or leak through, the host that is called (security
  review during M5); the custom request's URL follows the same fixed-origin rule.
- **Custom-request mode** (`http_*` providers, `config.mode = custom`, the template in `config.request`): method
  (GET/POST/PUT/PATCH), URL, headers, content type, body; the success rule (status range, default 200–299, plus an
  optional JSON path that must equal a value, or be present and not false); the provider message id by JSON path; a
  receipt mapping (optional list path, id path, status path, a status value map to delivered / read / failed /
  undeliverable (failed with the SMS fallback) / bounced, reason and code paths). Templates take `{{contact.x}}`,
  `{{event.x}}`, `{{campaign.x}}` and a new `{{message.x}}` namespace (to, text, subject, html, title, body,
  sender_id, dlt_template_id, template_name, params, suggestions, card, ref…; `personalize.MessageKeys`); it's empty in
  message templates. JSON bodies use `Renderer.JSON` (every value JSON-escaped); form bodies and query values are
  rendered value by value and percent-encoded; header values with a line break refuse the send. The URL's scheme and
  host can't be templated, so contact data never picks the destination. The dispatcher passes the message's
  personalisation context to providers (`Outbound.Vars`). Answers: 404/410 unreachable, 429/5xx retried, a 2xx
  outside the success rule or a JSON value mismatch permanent. Standard mode is unchanged, but its endpoint and
  signing secret are now required only in that mode, and it can also carry custom auth.
- **Receipts fail closed.** Custom-mode receipts need the provider's `receipt_token`: as a header, a query parameter,
  or the key of an HMAC-SHA256 of the body (hex or base64, `sha256=` prefix allowed). Without a saved token every
  receipt is refused. Gupshup webhooks need the provider's `webhook_token` (`?token=` or `X-Pulse-Token`), required
  when saving; the coordinator's fail-closed rule (91a365e) applies.
- **Preview and Test connection.** `POST /v1/channels/providers/preview` returns the exact request (method, URL,
  headers, body) a provider would send for Pulse's test message, or for a template rendered for a contact
  (`template_id`, `external_id`, `event`; a contact's data needs contacts view); secrets are masked (••••, the whole
  `Authorization` value) and the token shows as `{{token}}`. `POST /v1/channels/providers/check-auth` runs the login or
  token request now and returns the token masked (first and last four characters at most) and its expiry; 20 a minute
  per workspace. Both take the settings being edited (`provider_id` for saved secrets, not used when a host changed),
  need channels edit and settings edit (like saving a provider) and are audited like other changes (secrets
  redacted). Send test is the existing provider test, now built with the provider's token scope and
  `AllowPrivate`.
- **Email presets.** `gmail_smtp` (Gmail, 500/day, 1/s) and `gworkspace_smtp` (Workspace, 2,000/day, 2/s): smtp.gmail.com
  587 STARTTLS or 465 TLS, an app password (spaces removed) or XOAUTH2 (OAuth client + refresh token → access token
  from Google's token endpoint through httpauth, cached per provider; AUTH only over TLS). `gworkspace_relay`
  (10,000/day): smtp-relay.gmail.com 587/465/25, allowed by IP (EHLO with your domain, a new `helo_domain`
  setting, `SMTP.Helo`) or SMTP auth. A new provider starts with its kind's limits (`Kind.DefaultLimits`) unless
  limits are sent with it; otherwise with the workspace's default limits for the channel (Automation & limits). The
  form shows each preset's limits and that bulk mail belongs on the relay or a sending service. Gmail/relay report no
  delivery (plain SMTP).
- **Gupshup** (`gupshup_sms`, `gupshup_whatsapp`, `gupshup_rcs`), from Gupshup's public docs, tested against mocks only
  (every base URL editable; the form says to confirm the endpoints with a real account):
  SMS through the Enterprise gateway (`GatewayAPI/rest`, `method=SendMessage`, userid/password, `msg_type` TEXT or
  UNICODE_TEXT chosen from the text, `mask` = sender ID, DLT `principalEntityId` / `dltTemplateId` from the template —
  new SMS template fields `dlt_template_id` and `dlt_entity_id` — or the provider's defaults); delivery reports by POST
  or GET (GET webhooks reach the parser for these kinds, `providers.GetReceiver`), causes mapped to reason codes
  (unknown subscriber → invalid_number and sms_bounced, DND → consent, blocked → blocked, template mismatch → template…).
  WhatsApp through `/wa/api/v1/template/msg` (Gupshup template id = the template's name, body params, header image) or
  `/wa/api/v1/msg` (session text), `apikey` header; v2 message events delivered / read / failed matched on `gsId`;
  Gupshup codes (1002 not on WhatsApp → fallback, 1006–1012 consent, 4001 rate limited…) and Meta codes (131026 →
  "undeliverable (possibly blocked)") to reason codes; inbound messages feed reply keywords. RCS through the Enterprise
  gateway with an RBM-style `contentMessage` (text, rich card, suggestions) — the least certain part, see Risks;
  "RCS not supported" answers fall back to SMS. Each kind has Preview (`PreviewRequest`, secrets masked) and Send test,
  and the M3 rate/quota fields.
- **Settings → Automation & limits** (`internal/automation`, `tenant_settings.automation`, migration pg 024; settings
  view to read, settings edit to change; audited; the page shows each value with its default and an explanation):
  retention per log type — events, message events, page/screen views (`page_view`, `screen_view`,
  `landing_page_viewed`; null = like events), audit log (365), import history, webhook deliveries (7), integration log
  (7), sandbox test inbox, live feed size (1,000 entries) — and per event-name overrides (0 = forever) that always win
  over a type-wide delete (each name belongs to exactly one delete group); anonymous visitors (days idle, 0 = off);
  send retries (attempts 8 and longest wait 1 h: the old 30 s … 1 h schedule cut, capped or extended by doubling;
  Retry-After capped by the same wait); segment sweep interval (0 = the server's `PULSE_SWEEP_INTERVAL`, honoured in
  the claim SQL), reachability rebuild hour (default: a day after the last rebuild, as before), default time and zone
  for new daily segments (06:00, browser zone), app inbox expiry (30 days), default provider limits per channel.
  Every default equals the behaviour before M5. Changing a retention to something shorter opens "What will be
  deleted" (`POST /v1/settings/automation/preview`: ClickHouse counts over the old partitions and Postgres counts,
  cached 60 s, one uncached preview per workspace every 5 s) and needs a confirmation. `POST
  /v1/settings/automation/run-retention` runs a workspace's cleanup now (settings edit; used by the e2e test).
  `PUT /v1/settings/data` still edits the three old retention values, merged into the new document.
- **Retention at scale.** Cleanup runs once a day per workspace (in UTC hour `tenant % 24`, claimed in
  `retention_runs` so several workers never repeat it; a workspace that missed its hour runs after 47 h) instead of
  every hour: one `DELETE … WHERE tenant_id = ? AND ts < cutoff AND name IN/NOT IN (…)` per group (lightweight
  deletes, which hide rows at once), each skipped when a `SELECT 1 … LIMIT 1` finds nothing older; Postgres logs in
  `LIMIT 5000` batches. Global random 7-day prunes of webhook deliveries and the integration log moved into it.
  Workspaces that existed without a settings row keep their audit log forever (migration 024 writes
  `audit_days: 0` for them: the old cleanup never read them); new workspaces get the 365 days the app always showed.
- **Hard-coded automatic behaviour found:** made configurable — everything listed above. Deliberately fixed (most listed on
  the page under "Fixed by the platform"): per-channel workspace send rates (`dispatch.DefaultRates`,
  `PULSE_EMAIL_RATE`: shared server capacity), "already sent" de-duplication 7 days and receipt matching 3 days,
  frequency-cap counters 32 days, held-message release every 5 s, purge of deleted inbox messages after 7 days, journey
  effects 30 days (idempotency), SMS short links 30 days, attachment links 7 days, live feed 50 events/s and 4 KB of
  properties, outbound webhook retries (30 s … 6 h; a candidate for later), segment claim expiry 10 min, cache lifetimes
  (estimates 30 s, dashboards 1 min, event catalog 2 min, schemas 5 min), sessions 14 days, sign-in rate window
  15 min, RCS reply window 7 days, schema violation counters, test sends 30/min and connection tests 20/min (abuse
  limits), and the global `PULSE_MAINTENANCE_INTERVAL` / `PULSE_AUDIENCE_RECONCILE_INTERVAL` passes.
- **The M1 e2e flake** (`TestListEnumFieldsAndPhoneCountry`, waiting for a contact to leave segments) was a bug, not
  timing: a new segment's catch-up rebuild (5 s after its backfill) read ClickHouse before the contact's second
  identify had reached it and replaced the bitmap, putting back a contact the stream had just taken out; nothing
  corrected it until the next sweep. `segengine.Rebuild` now checks diffs of up to 20,000 contacts of a streamed
  segment against the contacts' current profiles (Postgres) and counters (Redis), evaluated exactly as the stream
  does, and applies only the changes that still hold, bit by bit; larger diffs (a first backfill of a big segment)
  replace the bitmap as before. The test is unchanged.
- **Not built:** Gupshup RCS delivery-report and inbound formats are inferred (no public docs found);
  per-workspace maintenance interval (a global pass); a no-secret preview of SMTP presets (they have Test connection
  and Send test, not a request preview, since SMTP has no HTTP request).

## M6 as built

Built as designed, with these choices:

- **AMP part storage.** No new template columns: the AMP code is `templates.content.amp` and the builder's document
  `content.amp_blocks` (`channel.Content`). When blocks are sent, the API builds the code from them on every save
  (`channel.PrepareAMP` → `amp.Doc.Build`), so the blocks are the source and the generated code is never edited by
  hand alongside them; "Code" mode in the app turns the blocks into code once and drops them. The template preview,
  the wizard and duplicates go through the same path.
- **Validator** (`internal/amp/validate.go`): a rules table over the HTML tokenizer with line numbers — doctype,
  `<html ⚡4email>` (or `amp4email`), `<meta charset="utf-8">` first in head, the runtime script, the boilerplate,
  only AMP component scripts from cdn.ampproject.org for the allowed components (accordion, anim, autocomplete,
  bind, carousel 0.2, date-picker, fit-text, form, image-lightbox, inputmask, lightbox, list, selector, sidebar,
  timeago, mustache 0.2) and each used component's script present (unused ones are a warning), no other script,
  no event-handler attributes or `javascript:` URLs, disallowed tags (img, iframe, video, link, svg…), one
  `<style amp-custom>` in head, no `!important`/`@import`, CSS ≤ 75,000 bytes, `amp-img` sized and https, forms
  `method="post"` with `action-xhr` (never `action`), document ≤ 200 KB. Errors stop a save (400 "AMP part: line N:
  …"); warnings don't. Full validation stays Google's job (AMP validator, Gmail sender registration).
- **Post-back placeholders.** Forms must post to `action-xhr="pulse:submit/<block>"` (amp-list may read
  `pulse:results/<block>`); any other form target is refused. The dispatcher replaces them per recipient with
  `https://<public URL>/v1/amp/<token>` (`amp.Fill`), where the token is a sealed `tracking.AMPToken` (workspace,
  contact id and external id, message, template, journey/broadcast/run/variant, block, expiry 60 days, a test flag),
  with its own prefix so a tracking token can't be used as one or vice versa. Without a server key the AMP part
  (if it has forms) is left out; one over 200 KB once personalised is left out too. Internal notifications carry no
  AMP part.
- **Personalisation.** The AMP part is rendered by the same html/template renderer as the HTML part (escaped for
  where each value lands). amp-mustache `<template>`s are cut out before and put back after (`amp.Protect`): their
  `{{…}}` belong to the email client, so Pulse variables don't work inside them. Click tracking, the unsubscribe
  footer and an `<amp-img>` open pixel are added like in the HTML part (`tracking.InstrumentAMP`).
- **MIME and providers.** `multipart/alternative` is text, AMP (`text/x-amp-html`), HTML (also inside
  `multipart/mixed` with attachments). Pulse has no API email providers: every SMTP relay passes the part as built
  (SES, SendGrid, SparkPost, Mailgun, Gmail/Workspace and plain relays) except Postmark, which doesn't support AMP —
  the part is left out there (`providers.SupportsAMP`, shown in the editor per provider). The custom gateway gets it
  as `content.amp` and `{{message.amp}}`; the sandbox stores it.
- **Builder blocks:** heading, text, image, button, carousel (`amp-carousel` slides, autoplay), gallery
  (`amp-carousel` strip), accordion (`amp-accordion`), poll and form (`amp-form` + `amp-mustache` success and
  error messages); style: page, text and accent colours and font. Form fields: text, email, number, textarea,
  select, checkbox; each may copy its answer to a contact field (`save_to`), checked on save and again at post time
  with `eventschema.ListTargetAllowed` (custom fields only; never identity, consent or Pulse's own fields).
- **Post-back endpoint** `POST /v1/amp/{token}` (public): token opened and expiry checked; CORS per the AMP for Email
  spec — v2 `AMP-Email-Sender` → `AMP-Email-Allow-Sender`, or v1 `Origin` of a known email client (Gmail, AMP
  playground, Yahoo, Outlook, Mail.ru) plus `__amp_source_origin` → `Access-Control-Allow-Origin`,
  `AMP-Access-Control-Allow-Source-Origin`, `Access-Control-Expose-Headers`; the sender's domain must be a domain
  of the workspace's email providers' From addresses or the server's default sender (`PULSE_SMTP_FROM`), cached a
  minute. Rate limits: 60 a minute per client IP and workspace, 10 a minute per token. Only the body's form fields
  of the block the template defines are read (hand-written forms: up to 20 simple field names). Polls: one answer
  per contact per poll (Redis `SETNX`, 400 days; later answers get "You already answered"), counts in a Redis hash
  per template and block; `amp_poll_answered` {block, question, answer, message ids}. Forms: `amp_form_submitted`
  with one property per field and the allowed contact-field updates as the event's attributes; the event id hashes
  token and values, so a replay records once. Test tokens (bare-address test sends) answer without recording. JSON
  answers carry `message` (and `items`/`total` for polls) for the AMP templates. `GET /v1/amp/{token}/results` serves
  `items` for `amp-list`. The two events are platform events (refused on `POST /v1/events`, server-only for
  browsers) but may have schemas; starter schemas added.
- **Editor:** `POST /v1/templates/amp/check` (content view) validates code or builds blocks, returns problems with
  lines, size, a script-free preview (`amp.PreviewHTML`: images, scrolling carousels, `<details>` accordions,
  forms that can't be sent; shown in a sandboxed iframe) and the providers' AMP support; the template preview
  returns `amp` / `amp_preview`; `GET /v1/templates/{id}/amp/results` shows poll counts. The app explains Gmail's
  sender registration and the 60-day link lifetime. Send test includes the AMP part (owner rule: preview and test).
- **Content variants** (`model.DWCVariant`, still JSON in `dwc_slots.variants`): id (assigned on save), name,
  audience (`model.OnsiteAudience`: segments in, segments out, page URL contains / doesn't contain, devices,
  contact-field conditions as segment property filters) and HTML. Array order is the priority; the first match wins,
  else the default. Pre-M6 `segment_id` variants are folded into the audience on save and read as such before.
  Contact-field conditions match only verified identities (`verifiedIdentity`, M2 rule); segment rules keep
  `webIdentityOK` as before. The SDK sends `device` (desktop/mobile/tablet) to `/v1/web/decide`, which now also
  returns `variants` (slot → variant id).
- **Never shown** (`onsite.Shadowed`, from rules alone): a variant is shadowed when an earlier one covers it (no or a
  superset of its segments, a subset of its exclusions, page rules that contain its pages, devices and conditions
  that include its own); an "everyone" variant hides the rest and the default. Returned by GET and save, mirrored
  live in the editor.
- **Overlap view** `POST /v1/dwc/overlap` ({slot_id} or unsaved {variants}) and `POST /v1/focus-items/overlap`
  ({kind} or {ids}): one Redis pipeline of BITOPs (`onsite.CountOverlap`) — per variant audience (segments or all
  known contacts, minus exclusions), pair ANDs, who would see each in order, contacts in more than one, the
  default's share; page, device and field rules aren't counted ("approximate"). Cached a minute per audience set;
  30 uncached counts a minute per workspace; content view.
- **Focus items** got the same audience rules and a priority (`targeting.priority`, no column; `PUT
  /v1/focus-items/order`). The decision returns them in order and the SDK shows one pop-up, one bar and one slide-in
  per page — the first whose frequency still allows it (an older cached SDK shows all, as before). Never-ordered
  items (priority 0) sort first until an order is saved.
- **Web push prompt** (`internal/webcheck`, `tenant_settings.webpush`, migration pg 025; Settings → Web push, channels
  view to read, channels edit to change): bell, bar or pop-up, texts and colours, delay / scroll / pages-this-visit
  triggers, ask again after N days (0 = never), pages to show on / hide on, the site address and service-worker path.
  `/v1/web/push-config` returns the prompt; the SDK shows it only while permission is undecided, then "Allow" opens
  the browser's prompt (a click) and subscribes; browsers that already allowed are kept subscribed. SDK events
  `push_prompt_shown`, `push_prompt_allowed`, `push_prompt_dismissed`, `push_subscribed`, `push_permission_refused`.
- **Install check** `POST /v1/settings/web-push/check` (channels edit, 10 a minute): the server fetches the page and
  the service worker through `safehttp` wrapped in `safehttp.SameOrigin` (public addresses only, checked per
  connection; https only; redirects only within the site; 1 MB; 10 s; private addresses and http only with
  `PULSE_ALLOW_PRIVATE_WEBHOOKS`) and reports HTTPS, page opens, snippet found (and from which host), site key is
  this workspace's, service worker present and JavaScript, imports Pulse's worker, VAPID keys set. **Browser
  self-check:** `POST /v1/settings/web-push/check-token` gives a 30-minute `?pulse_check=` link; the SDK on that page
  runs `Pulse.diagnose()` and posts it to `POST /v1/web/diagnose` (site key must belong to the token's workspace;
  fields capped); the app polls `GET /v1/settings/web-push/diagnosis`.
- **Deviations:** "every API provider that supports AMP" — Pulse has no email API adapters (SES, SendGrid and
  SparkPost are reached over SMTP), so AMP goes through their SMTP relays; the poll results show in the post-back
  answer and through `amp-list` (the builder uses the answer; hand-written AMP can use the list). Focus items have no
  per-audience content variants (variants are a content-slot feature); they get ordering, the same rules and the
  overlap view per kind instead.
- **Not built:** a real AMP renderer in the app (the preview is an approximation; real behaviour shows in Gmail
  or the AMP playground); AMP for transactional internal notifications.

## M7 as built

Built as designed, with these choices:

- **Contact lists behind numbers.** The Contacts search box gained filters that name the sets the app counts
  elsewhere (`internal/api/contactsets.go`), shown as removable chips in words ("Campaign “Spring catalogue”",
  "Opened", "Not in segment “VIPs”", "Reachable by email"): `in:` (any of the listed segments; several `in:` chips
  intersect), `not_in:`, `subscribed:<channel>`, `reachable:<channel>`, `under_cap:<channel>`, `journey:` with
  `at:<step>` and `journey_status:in_progress|completed|converted|exited|active|waiting`, `campaign:broadcast/<id>` or
  `campaign:journey/<id>` with `run:`, `step:`, `outcome:` (targeted, sent, delivered, opened, read, clicked,
  downloaded, bounced, hard/soft_bounced, complained, failed, failed_all, unsubscribed, fallback, skipped and
  skipped_<group>) and `reason:<code>` (with a reason, `outcome:` is the raw kind, like the reasons table),
  `event:` / `no_event:` with `product:` / `category:`, `active:any|web|android|ios|other|new|returning` with `utm:`,
  `amp_poll:<template>/<block>` with `answer:`, `form:<id>`, and `period:<from>..<to>` (UTC days, both included).
  Wrong combinations are refused with the reason (`at:` needs `journey:`…).
- **Two engines.** Set filters run in Redis on the bitmaps the numbers come from (segment bitmaps, the M3 reach
  bitmaps; a journey's enrollments are read from Postgres, streamed into a bitmap): one `BITOP` pipeline per view,
  the result cached a minute under `cvset:{tenant}:{hash}`, paged newest contact first by `GETRANGE` chunks (8 KB
  growing to 1 MB, so a 100M-contact bitmap is at most ~15 round trips per page), counted by `BITCOUNT`. Outcome,
  event, activity, poll and form filters are ClickHouse subqueries over `message_events` / `events` /
  `first_active`, each with `tenant_id = ?`, added to the existing view condition. A view with both checks the
  bitmap's candidates in ClickHouse 5,000 at a time (`ContactFilter`, `has(?, id)` within the id range); its count
  is exact up to 10,000 candidates (50,000 for the frequency cap alone, like the estimate) and sampled above
  ("about"). Bitmap builds and first pages of these views are limited to 120 a minute per workspace (`rlview`).
- **Equal counts.** Every linked number equals its list on seeded data (checked by the e2e test and in the browser
  on every page): estimate steps, reach by channel, segment sizes, the broadcast/journey funnels, reasons, per-step
  messages, journey "here now" and in-progress, product viewers / carters / buyers / abandoned, Users dashboard
  numbers, company and stage counts, event contacts, form contacts, AMP poll answers, on-site overlap. Where it can
  differ the list says why (`note`): reports count contacts merged or deleted since (the Users dashboard keeps
  counting deleted contacts, M4); a journey's numbers count entries, the list people. Deviation: the Campaign
  performance totals add up each campaign's contacts (a person in two campaigns counts twice), so they aren't one
  list and stay plain with a tooltip; each campaign's numbers and the cross-campaign reasons link. The Events page's
  "Last 30 days" became 30 whole UTC days so its contact count matches its list.
- **Not linked (no list of people exists):** journey canvas "Entered", Yes/No and split path counts (Redis counters
  of passes, not of people; tooltip says so); the broadcast's "audience at send time" (a snapshot); the segment
  builder's live preview of an unsaved rule (save, then "In segment now" links); funnel and cohort reports (computed
  per run over a chosen range); assets' downloaders, landing page visitors, in-app "seen by", imports' rows,
  duplicate groups (listed on the page itself), messages-per-channel totals (messages, not people).
- **Permissions** (`viewFilterNeeds` in `perms.go`): a list needs contacts view, plus for `campaign:broadcast`
  broadcasts or reports view, `campaign:journey` and `journey:` journeys or reports view, outcomes across every
  campaign reports view, `amp_poll:` and `form:` content or reports view. The app shows a number the role can't open
  unlinked with a tooltip saying which access is missing (`contactLinks.ts`, the same rules).
- **Create segment from this list.** Lists built from these filters can't be a dynamic segment's rule, so the
  Contacts page offers "Create segment from this list" instead of "Save as segment": `POST /v1/contacts/view-segment`
  {q, name} (segments edit, plus contacts view and each filter's need; audited) makes a static segment and adds the
  list's contacts now (up to 500,000) through `list_joined` events in the background, from a 15-minute copy of the
  view's bitmap. Rule-only lists keep "Save as segment" (dynamic). Segments' "View contacts" now opens `in:<id>` so
  the list has exactly the segment's size.
- **Campaign navigation.** One sidebar entry, **Campaigns** (highlighted on campaign editors too); the Journeys and
  Broadcasts entries are gone. Three types everywhere, named by when they send: **One-time** ("one message to
  everyone in your chosen segments, sent once or repeated"), **Triggered** ("starts for each person when they do
  something") and **Automation** ("starts for each person when they join a segment"); triggered and automation
  campaigns are "built as journeys: a flow of messages, waits and checks". "Broadcast" no longer appears in the app
  (one-time campaign); "journey" stays only as the name of the step builder. **New campaign** asks what the
  marketer wants to do (send a message to a segment → channel → the wizard; react when someone does something →
  the builder with an event trigger; follow up when someone joins a segment → the builder with a segment trigger;
  `/journeys/new?start=event|form|segment`). The hub took over the journey list's columns and actions (in progress,
  duplicate, archive / restore / delete) and links its numbers. `/journeys` and `/broadcasts` redirect (301 from the
  server, and in the app) to `/campaigns` and `/campaigns?tab=one_time`; `/campaigns/new` opens the chooser;
  `/journeys/:id` and `/broadcasts/:id` are unchanged. The one-time editor is "All settings and results"; the
  wizard is "Edit step by step"; each links to the other. Roles name the areas "One-time campaigns" and "Triggered &
  automation campaigns". "On-site & in-app" is "Website & app messages".
- **Clarity fixes from the review** (the full list is in the M7 report): one word per state everywhere (Live,
  Finished, Sending, Draft, Scheduled, Paused, Cancelled, Archived; the hub's status filter uses the same words); a
  finished one-time campaign offers "Send again" with a confirmation instead of "Schedule"; provider forms list the
  usual services first (not the custom gateway), show option labels in words ("An app password", "Pulse's standard
  request (signed JSON)"), and no longer call required gateway fields optional; the Channels page explains what a
  provider is and no longer overflows at 1280 px; the AMP block list no longer runs under the preview; settings
  jargon reworded (segment sweep → "Re-check time-window segments every", reachability rebuild → "Recount who can
  be reached", the GeoIP note); numbers got time ranges (Overview messages: 14 days) and units.
- Tests: Go `contactsets_test.go` (parsing, labels, every filter's SQL placeholders and tenant scoping, permissions,
  bitmap paging, estimate-step equality with `reach.Count`, sampling, old-address redirects), perms rows;
  vitest `contactLinks.test.ts` (link building, chips round trip, who may open a list, goals, redirects, nav
  highlighting); e2e `test/e2e/m7_test.go`.

## Docs for the final pass

From M4 on, docs and screenshots are updated in one pass after M6 (owner's decision, 2026-11-11).
Each milestone adds what it needs here.

**M4:**
- Webhook verification fails closed once any key is saved (a format with no check of its own needs
  basic auth, else it's refused; the settings page shows "Refused"); the Campaigns dashboard and the
  contact page's message list show campaign names only to roles that may view broadcasts / journeys.
- README: M4 features; SMTP "Sending service" setting.
- docs/02-architecture.md: message references and linking, webhook verification, reason codes,
  ClickHouse `message_events.reason_code`, `message_events_mv3`, `active_users_daily`, `first_active`,
  migrations pg 023 / ch 006, live feed stream, dashboard caching; mark DAU pre-aggregation done.
- docs/05-feature-parity.md: delivered, complaints, DAU/WAU/MAU, live feed, contact page, failure reasons.
- Developer 03 (API): `/v1/analytics/users|campaigns|products`, `/v1/live/events`,
  `/v1/contacts/{id}/profile|messages`, `/events` filters, `PUT /v1/settings/bounces/signing`, report
  fields `funnel`, `reasons`, `reasons_by_node`, `targeted` and counts `bounced`/`complained`/`downloaded`,
  sandbox receipt statuses `bounced`/`complained` and `code`.
- Developer 06 (webhooks/providers): per-provider webhook set-up and signatures, reference headers,
  SparkPost, gateway `ref` and receipt `bounced`/`complained`/`kind`/`reason_code`, plain SMTP reports
  no delivered.
- Tutorials: 2 (full contact page), 4 (delivery webhooks, signing keys), 7 and 14 (funnel and reasons),
  10 (Users, Campaign performance, Products, Live events); new 15 "Delivery and dashboards" in the index.
- Screenshots: new `15-*.mjs`; retake 02, 04, 07, 10, 14 and every shot showing the sidebar.

**M5:**
- Provider requests that carry credentials (custom auth logins and token requests, the gateway,
  Gupshup) never follow a redirect to another site (`safehttp.SameOrigin`). Test connection with saved
  secrets works only for the saved settings (changed settings need every secret entered again), and
  its error text never contains a secret.
- Saving a provider with any changed setting except harmless ones (sender name/number, pool sizes,
  DLT ids…) needs every secret entered again (`providers.CredentialSettingsChanged`): URLs including
  path and query, modes, auth and request templates, receipt mappings, user names.
- README: M5 features; provider list (Gmail, Google Workspace, Workspace relay, Gupshup SMS/WhatsApp/RCS, gateway
  custom request and authentication); the new settings page with every field, default and range; retention moved
  off Data & privacy and now daily per workspace; provider secrets merged key by key and cleared on a host change.
- docs/02-architecture.md: `internal/httpauth` (types, token cache keys, lock, 401 retry, sealing), host pinning,
  gateway custom-request mode and `{{message.x}}`, `Outbound.Vars`, Gupshup adapters and GET receipts, SMTP `Helo`
  and XOAUTH2, `internal/automation` and `tenant_settings.automation`, migration pg 024 and `retention_runs`, daily
  per-workspace retention with event groups, per-workspace retries / sweep interval / rebuild hour / live feed size,
  the rebuild verification in segengine (M1 flake).
- docs/05-feature-parity.md: custom auth, custom requests, Gmail/Workspace/relay, Gupshup, automation settings.
- Developer 03 (API): `POST /v1/channels/providers/check-auth`, `POST /v1/channels/providers/preview`, provider save
  (merge, `secrets_cleared`, preset limits, `limits` on create), `GET|PUT /v1/settings/automation`,
  `POST /v1/settings/automation/preview`, `POST /v1/settings/automation/run-retention`, the `PUT /v1/settings/data`
  merge, SMS template `dlt_template_id` / `dlt_entity_id`.
- Developer 06 (webhooks/providers): authentication types and the login flow (JSON path / header / regex, expiry,
  injection, refresh on 401), the custom request (template variables and escaping, success rule, id path, receipt
  mapping and verification, fail-closed), Gupshup webhook set-up (`?token=`, v2 events, delivery report URL by GET or
  POST), endpoints unconfirmed; Gmail XOAUTH2 setup (OAuth client, refresh token for https://mail.google.com/) and
  relay setup (Admin console, IP allowlist, EHLO domain).
- Tutorials: 4 (Channels: Gmail / Workspace / relay presets with their limits, Gupshup, custom gateway with
  authentication, Test connection, Preview request), 5 (SMS template DLT fields), 11 (or the settings tutorial:
  Automation & limits, the "What will be deleted" dialog, Data & privacy now linking there); a new tutorial 16
  "Connecting any provider" (custom request + login flow walk-through) in the index.
- Screenshots: new `16-*.mjs` (provider form with authentication, custom request, Test connection result, request
  preview; Automation & limits page; What will be deleted dialog); retake the Channels provider form (presets, notes),
  the SMS template editor, Data & privacy, Segments "Daily at" default, and every shot showing the settings menu.

**M6:**
- README: M6 features (AMP emails with the block builder and post-back events, content variants with ordering and
  overlap, focus item order, web push prompt and install check); Postmark sends no AMP; `PULSE_SMTP_FROM` is also an
  AMP sender domain.
- docs/02-architecture.md: `internal/amp` (validator rules, builder, placeholders, Protect/Fill, preview), AMP
  MIME order and `providers.SupportsAMP`, `tracking.AMPToken` and `InstrumentAMP`, the `/v1/amp/{token}` post-back
  (CORS v1/v2, sender domains, rate limits, poll Redis keys `amp:ans:*` / `amp:poll:*`), `onsite.OnsiteAudience`,
  `DecideFor`, `Shadowed`, `CountOverlap` and keys `ovl:*` / `ovltmp:*`, focus priority, `internal/webcheck` and the
  fetcher's safety rules, `tenant_settings.webpush` (migration pg 025), diagnose keys `pchk:*` / `pchkr:*`.
- docs/05-feature-parity.md: AMP email, content variants with rules and overlap, web push opt-in prompt and install
  check.
- Developer 03 (API): `POST /v1/templates/amp/check`, `GET /v1/templates/{id}/amp/results`, `content.amp` /
  `content.amp_blocks` (block schema) and the 400 with lines, preview `amp` / `amp_preview`, `POST /v1/amp/{token}`
  and `GET /v1/amp/{token}/results` (headers, answers, errors), DWC variant `audience`/`id`/`name` and GET
  `shadowed`, `POST /v1/dwc/overlap`, `POST /v1/focus-items/overlap`, `PUT /v1/focus-items/order`, focus targeting
  fields, decide `device` and `variants`, `GET|PUT /v1/settings/web-push`, `/check`, `/check-token`, `/diagnosis`,
  `POST /v1/web/diagnose`, `push-config` `prompt` / `service_worker`, events `amp_form_submitted` /
  `amp_poll_answered` and the starter schemas.
- Developer 06 (webhooks/providers): AMP support per provider, gateway `content.amp` / `{{message.amp}}`.
- Web SDK guide: `device` in decide, one focus item per kind in order, the opt-in prompt (no code), `Pulse.diagnose()`
  and `?pulse_check=`, the new `push_prompt_*` events, the service worker path setting.
- Tutorials: 5 or 6 (email templates: the AMP section — blocks, code mode, problems, preview, providers, Gmail
  registration, poll results; segments/journeys on `amp_poll_answered`), 8 or the on-site tutorial (variants: rules,
  order, never-shown warnings, overlap; focus item order), the web push tutorial (Settings → Web push: prompt and
  install check); a new tutorial 17 "Interactive AMP emails" in the index.
- Screenshots: new `17-*.mjs` (AMP blocks editor, code mode with problems, preview and providers, poll results;
  content variants with rules and a never-shown warning; overlap panel; focus item order drawer; Web push settings
  with prompt preview, install check results and browser check); retake the template editor, the on-site editors and
  list, and every shot showing the settings menu (new "Web push" item).

**M7:**
- Navigation changed (one Campaigns entry, "Website & app messages", campaign types renamed), so **every
  screenshot showing the sidebar must be retaken** (in practice most of them), plus every shot of the Campaigns hub,
  the old Journeys / Broadcasts lists (gone: retake as the hub with the type tab), the journey and one-time editors
  (headers, back links, "Edit step by step" / "All settings and results"), the New campaign chooser (new), status
  badges (Active → Live, Completed → Finished), report funnels and estimate steps (numbers are now links), the
  Contacts page with link chips and "Create segment from this list" (new), the provider form (option labels,
  order), Channels, Automation & limits, the AMP editor, Roles.
- README: M7 features (contacts behind every number, one campaign model); the Journeys/Broadcasts → Campaigns
  wording everywhere.
- docs/02-architecture.md: `internal/api/contactsets.go` (set and outcome filters, the two engines, `cvset:*` keys
  and `rlview` limit, `ContactFilter`, view-segment snapshots `cvset:*:snap`), `JourneyContacts`, the server-side
  redirects of `/journeys` and `/broadcasts`, provider `option_labels`, the Events catalog's whole-day window.
- docs/05-feature-parity.md: drill-down from every report number to its contacts; campaign types.
- Developer 03 (API): the new `GET /v1/contacts?q=` filters (each key, values, combinations, errors, `note`,
  permissions), `POST /v1/contacts/view-segment`, the export taking the same filters, provider field
  `option_labels`.
- Tutorials: 1 (getting started: navigation, New campaign chooser), 2 (contacts: the new filters and chips, lists
  opened from numbers, Create segment from this list), 3 (segments: View contacts / sizes as links), 4 (channels:
  provider wording), 6 and 7 (journeys → triggered and automation campaigns; canvas numbers that open lists), 14
  (Campaigns hub and one-time campaigns: types, chooser, two editors, redirects), 15 (dashboards: drill-down,
  which totals aren't linked and why), 16 and 17 (provider form labels, AMP editor), and any tutorial text saying
  "Journeys", "Broadcasts", "On-site & in-app", "Active" or "Completed".
- Screenshot scripts: every `*.mjs` that clicks the "Journeys" or "Broadcasts" sidebar entries or "New journey" /
  "New broadcast" / "New one-time campaign…" must go through Campaigns → New campaign; selectors for status badge
  text change.

## Risks

- Gmail shows AMP only for senders Google has registered (SPF, DKIM, DMARC); that is outside Pulse.
- Plain SMTP relays give no delivery webhooks, so "delivered" exists only for sending services; their
  bounces come only through the bounce mailbox. Bounces from before M4 can't be linked to campaigns.
- Gupshup endpoints and parameters must be confirmed with a real account; adapters are tested against
  mocks. RCS formats (send body, delivery reports, inbound) are inferred from RBM, not documented by Gupshup.
- At 100M contacts, audience estimates (Redis bitmap operations) are cached and rate-limited, and
  per-event-name retention runs as daily batched ClickHouse deletes.
- M7 lists from report numbers run ClickHouse subqueries over `message_events` / `events`; a very broad one (every
  campaign's sends over a year) on a huge workspace can take seconds. They are limited per workspace and time out
  after 15 s; sparse bitmap views read up to ~12.5 MB of bitmap per page in the worst case.
