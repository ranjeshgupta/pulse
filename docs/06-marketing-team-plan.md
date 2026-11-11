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
| 1 | Campaign types | Partial: no categories, no combined list, no tags | M3 |
| 2 | One-time campaign per channel, audience inside | Partial: content must be a saved template first | M3 |
| 3 | Dashboard by campaign / communication | Partial: per-campaign reports only | M4 |
| 4 | Reporting of everything | Partial: no failure reasons, deliverability, segment growth | M4 |
| 5 | Publish / unpublish times | Already (broadcasts, journeys, on-site, landing pages) | M3 adds a send deadline |
| 6 | Wrong users get emails | Already (real-time membership, two compilers checked against each other) | M1 ✅ adds "why is this contact in this segment" |
| 7 | Audience vs sent vs bounced | Partial: bounces aren't linked to campaigns; no breakdown | M4 |
| 8 | Delivered / read / click / download / spam | Partial: no email delivered, complaints not in reports | M4 |
| 9 | Segment date from–to | ✅ M1: between / since dates on behaviour, "is between" on date fields | M1 ✅ |
| 10 | Reachable channels | Partial: no per-contact matrix or per-campaign counts | M3, M4 |
| 11 | Standard variable names | ✅ M2: `{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`, `{{links.x}}` with defaults and formats, a picker on every templated field | M2 ✅ |
| 12 | Web push / personalisation activation | Already | M6 adds a no-code opt-in prompt and install check |
| 13 | Deliverability per campaign | Partial (as row 7) | M4 |
| 14 | Live segment | Already | |
| 15 | Exclude in segmentation | Already | M1 ✅ adds a "none of" group |
| 16 | Audience count after every filter | Partial: segment size only | M3 |
| 17 | Test WhatsApp / push | Already | M3 adds saved test contacts |
| 18 | Timely delivery, daily quota | Partial: no per-provider quotas or deadline | M3 |
| 19 | A/B testing | Already | |
| 20 | DAU / WAU / MAU | Missing | M4 |
| 21 | Real-time activity | Partial: no live feed | M4 |
| 22 | WhatsApp / push delivered, read, blocked | Partial: no blocked class or reasons | M4 |
| 23 | Complete user profile | Partial | M4 |
| 24 | Tags in segmentation | Already | |
| 25 | Daily refresh time for segments | ✅ M1: Refresh daily at HH:MM in a time zone | M1 ✅ |
| 27 | Copy blocks | Missing | M3 |
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
3. **M3 Campaigns.** A Campaigns hub with One-time / Trigger based / Automation tabs and tags, a
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

## Risks

- Gmail shows AMP only for senders Google has registered (SPF, DKIM, DMARC); that is outside Pulse.
- Plain SMTP relays give no delivery webhooks, so "delivered" exists only for sending services; their
  bounces come only through the bounce mailbox. Bounces from before M4 can't be linked to campaigns.
- Gupshup endpoints and parameters must be confirmed with a real account; adapters are tested against
  mocks.
- At 100M contacts, audience estimates (Redis bitmap operations) are cached and rate-limited, and
  per-event-name retention runs as daily batched ClickHouse deletes.
