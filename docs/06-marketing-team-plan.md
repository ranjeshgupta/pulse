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
  are left out.
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
| 6 | Wrong users get emails | Already (real-time membership, two compilers checked against each other) | M1 adds "why is this contact in this segment" |
| 7 | Audience vs sent vs bounced | Partial: bounces aren't linked to campaigns; no breakdown | M4 |
| 8 | Delivered / read / click / download / spam | Partial: no email delivered, complaints not in reports | M4 |
| 9 | Segment date from–to | Partial: behaviour only "last N days" | M1 |
| 10 | Reachable channels | Partial: no per-contact matrix or per-campaign counts | M3, M4 |
| 11 | Standard variable names | Partial: raw `{{.Attrs.x}}`, picker only in rich text | M2 |
| 12 | Web push / personalisation activation | Already | M6 adds a no-code opt-in prompt and install check |
| 13 | Deliverability per campaign | Partial (as row 7) | M4 |
| 14 | Live segment | Already | |
| 15 | Exclude in segmentation | Already | M1 adds a "none of" group |
| 16 | Audience count after every filter | Partial: segment size only | M3 |
| 17 | Test WhatsApp / push | Already | M3 adds saved test contacts |
| 18 | Timely delivery, daily quota | Partial: no per-provider quotas or deadline | M3 |
| 19 | A/B testing | Already | |
| 20 | DAU / WAU / MAU | Missing | M4 |
| 21 | Real-time activity | Partial: no live feed | M4 |
| 22 | WhatsApp / push delivered, read, blocked | Partial: no blocked class or reasons | M4 |
| 23 | Complete user profile | Partial | M4 |
| 24 | Tags in segmentation | Already | |
| 25 | Daily refresh time for segments | Partial: one server-wide sweep | M1 |
| 27 | Copy blocks | Missing | M3 |
| 28 | Web content by audience without overlap | Partial: first match wins, no ordering or overlap view | M6 |
| 29 | AMP mailers | Missing | M6 |
| 30 | User and product dashboards | Missing | M4 |

(The sheet has no row 26.)

## Milestones

Each milestone is built, tested (unit, `make it` when segment rules change, end-to-end twice), documented
with screenshots, and committed on its own.

1. **M1 Events and segments.** Event schemas (typed properties, enums, dependent enums such as
   signup method Social → provider), Data → Events page with volumes and violations, starter schemas
   (purchase, signup, login, add_to_cart, cart_abandoned, checkout_abandoned), nested property paths
   in segment rules, absolute date ranges, a "none of" group, list and enum contact fields (products),
   `phone_country`, property filters on event triggers, daily-refresh segments. New tutorial 12.
2. **M2 Personalisation.** Standard variables `{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`,
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

## Risks

- Gmail shows AMP only for senders Google has registered (SPF, DKIM, DMARC); that is outside Pulse.
- Plain SMTP relays give no delivery webhooks, so "delivered" exists only for sending services; their
  bounces come only through the bounce mailbox. Bounces from before M4 can't be linked to campaigns.
- Gupshup endpoints and parameters must be confirmed with a real account; adapters are tested against
  mocks.
- At 100M contacts, audience estimates (Redis bitmap operations) are cached and rate-limited, and
  per-event-name retention runs as daily batched ClickHouse deletes.
