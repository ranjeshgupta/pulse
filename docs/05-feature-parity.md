# Feature parity: Mautic 7 + WebEngage → Pulse

Sources: the Mautic 7.2.2 install in `../mautic` (its bundles, plugins and console commands) and WebEngage's product surface.
Status: ✅ built and tested · 🟡 partial · ⬜ not built. Priority: **P0** blocks real-world use (compliance, tracking),
**P1** core marketer workflow, **P2** advanced or nice to have.

## Contacts and data

| Feature | Mautic | WebEngage | Pulse | Priority / note |
|---|---|---|---|---|
| Contact profiles, custom attributes | ✅ | ✅ | ✅ JSON attributes, no schema needed | |
| Real-time event tracking API | 🟡 | ✅ | ✅ 20k events/s on one machine | |
| Activity timeline | ✅ | ✅ | ✅ | |
| Unsubscribe, do-not-contact, per-channel consent | ✅ | ✅ | ✅ per channel and "everything", honored by every send (`*_suppressed`), editable per contact | |
| Preference center | ✅ | 🟡 | ✅ per-channel switches and public lists; `{{.PreferencesURL}}`, unsubscribe page link, API-minted links | |
| SMS / WhatsApp reply keywords (STOP, START) | ✅ | ✅ | ✅ Twilio and Meta inbound webhooks; editable keyword lists; replies recorded as `<channel>_received` | RCS replies: P2 |
| CSV import and export | ✅ | ✅ | ✅ import through the live pipeline; export with one column per attribute | Async jobs for 10M+ rows: P1 |
| Attribute definitions (type, label, PII flag) | ✅ | ✅ | ✅ Settings, Attributes; typed inputs in the segment builder; imports convert numbers and yes/no | |
| Tags | ✅ | 🟡 | ✅ `tag_added` / `tag_removed` events, API, contact drawer, CSV `tags` column, "is tagged" rules | |
| Points / lead scoring | ✅ | ⬜ | ✅ scoring rules per event, manual adjust, journey step; `points` attribute in rules | |
| Stages (lifecycle) | ✅ | ⬜ | ⬜ | P2 |
| Companies (B2B accounts) | ✅ | ⬜ | ⬜ | P2 |
| Identity merge (anonymous → known) | ✅ | ✅ | ✅ identify with `anonymous_id`: profile, counters and event history move; later visitor events follow | |
| Identity verification for browser SDKs (signed user ID) | ⬜ | ✅ | ✅ HMAC user hash; can be required per workspace; secret rotation | |
| Duplicate merge | ✅ | ✅ | ⬜ | P2 |

## Segments

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Attribute and event-count rules, AND/OR groups | ✅ | ✅ | ✅ | |
| Real-time membership (no cron) | ⬜ (15-min cron) | ✅ | ✅ ~60 ms | |
| Live size and preview | 🟡 | ✅ | ✅ | |
| "In / not in another segment" rule | ✅ | ✅ | ✅ evaluated in dependency order in the same pass; cycles refused | |
| Static lists (manual add/remove, import to list) | ✅ | ✅ | ✅ Lists page, API, CSV import into a list, journey step | |
| Event property filters (purchase where amount > 1000) | 🟡 | ✅ | ✅ up to 5 filters per event rule; history seeded from ClickHouse | |

## Journeys / campaigns

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Visual canvas | ✅ | ✅ | ✅ | |
| Triggers: segment entry, event | ✅ | ✅ | ✅ | |
| Schedule: start required, end optional (empty = runs forever) | ✅ | ✅ | ✅ this change | |
| Wait for a duration | ✅ | ✅ | ✅ durable timers | |
| Wait until a date/time, or until an event (with timeout) | ✅ | ✅ | ✅ date, next time of day in the contact's zone, wait for event with timeout | |
| Condition: in segment | ✅ | ✅ | ✅ | |
| Condition: attribute value, opened/clicked, visited page | ✅ | ✅ | ✅ "Check contact" with any segment rule, including email_opened/clicked and page_view counts | Per-message "opened *this* email": P2 |
| A/B split | ✅ | ✅ | ✅ 2-5 weighted paths, stable per enrollment | Winner auto-selection: P2 |
| Send email, SMS, WhatsApp (SMS fallback), RCS (SMS fallback), push | 🟡 | ✅ | ✅ | |
| Update attribute, add/remove tag, change points | ✅ | ✅ | ✅ update profile, tag contact, adjust points, add to / remove from list | |
| Webhook step | ✅ | ✅ | ✅ SSRF-guarded, idempotency key, failures recorded without stalling | |
| Conversion goal and exit on goal | 🟡 | ✅ | ✅ goal event converts and ends the enrollment | |
| Re-entry rules (once, every time, after N days) | 🟡 | ✅ | ✅ one live enrollment at a time | |
| Frequency capping across journeys | ⬜ | ✅ | ✅ per channel, per contact, across journeys and broadcasts; transactional exempt | |
| Quiet hours / DND, send in contact's time zone | ⬜ | ✅ | ✅ messages are held and sent when quiet hours end, in the contact's zone | |
| Per-step stats | ✅ | ✅ | ✅ live | |

## Broadcasts (one-time and recurring campaigns)

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Send to a segment on any channel | ✅ email/SMS | ✅ | ✅ this change | |
| Schedule: start required, optional end, repeat daily/weekly/monthly | 🟡 | ✅ | ✅ this change | |
| A/B test with winner selection | ✅ | ✅ | ✅ 2-5 variants, test group %, winner by open or click rate after a wait, or by hand; plain splits | |
| Conversion goal per broadcast | 🟡 | ✅ | ✅ recipients who do an event within N days | |

## Channels

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Email: drag-and-drop builder, HTML, personalization | ✅ | ✅ | ✅ GrapesJS | |
| Preview (with a real contact's data) and send a test, every channel | ✅ | ✅ | ✅ this change | |
| Provider connection test, per provider | 🟡 | ✅ | ✅ this change | |
| Email open and click tracking | ✅ | ✅ | ✅ sealed tokens; click links signed (no open redirect); `email_opened` / `email_clicked` events | |
| Unsubscribe link and List-Unsubscribe header | ✅ | ✅ | ✅ footer or `{{.UnsubscribeURL}}`, RFC 8058 one-click, confirm page | |
| Bounce and complaint handling | ✅ | ✅ | 🟡 receipts plumbing only | P1 |
| Plain-text part, attachments | ✅ | 🟡 | ⬜ | P2 |
| SMS (Twilio, MSG91) | ✅ | ✅ | ✅ adapters, not tried on live accounts | |
| WhatsApp (Meta Cloud) with SMS fallback | ⬜ | ✅ | ✅ | |
| RCS (Google RBM) with SMS fallback | ⬜ | ✅ | ✅ | |
| Mobile push (FCM) | ✅ | ✅ | ✅ adapter, tokens via API | SDK next |
| Web push | ✅ | ✅ | 🟡 FCM web tokens via `Pulse.registerPushToken`; no service-worker helper yet | P1 |
| In-app messages, app inbox | ⬜ | ✅ | ⬜ | P1, needs mobile SDKs |
| Webhook channel | ✅ | ✅ | ⬜ | P1 |
| Facebook / Google audience sync | 🟡 | ✅ | ⬜ | P2 |

## On-site

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Focus items: pop-up, bar, slide-in; segment and page targeting; triggers; frequency | ✅ | ✅ | ✅ | |
| Dynamic Web Content: per-segment variants | ✅ | ✅ | ✅ | |
| Schedule on-site content (start, optional end) | 🟡 | ✅ | ✅ this change | |
| "See what a contact would see" check | 🟡 | 🟡 | ✅ this change | |
| Rendering on websites (JS SDK) | ✅ | ✅ | ✅ `/sdk/pulse.js` (3 KB gzipped): focus items with all triggers and frequency caps, content slots | |
| Forms (with field mapping, progressive profiling) | ✅ | 🟡 | ✅ hosted page (iframe-able) and web SDK embed; lists, tags, consent opt-in; honeypot, fill time, rate limit; submissions log | File uploads, multi-page forms: P2 |
| Landing pages | ✅ | ⬜ | ⬜ | P2 |
| Assets (gated downloads) | ✅ | ⬜ | ⬜ | P2 |

## Analytics

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Live dashboard, message outcomes | 🟡 | ✅ | ✅ | |
| Journey and broadcast reports (sent, delivered, opened, clicked, converted) | ✅ | ✅ | ✅ per run, A/B variant, journey step and channel; top links; daily; from a ClickHouse `message_events` view | |
| Funnels | ⬜ | ✅ | ✅ 2-8 steps with property filters, conversion window, date range, segment filter; live while editing | Breakdown by attribute: P2 |
| Cohorts / retention | ⬜ | ✅ | ⬜ | P2 |
| Revenue attribution, UTM | 🟡 | ✅ | ⬜ | P2 |
| Custom report builder | ✅ | 🟡 | ⬜ | P2 |

## Platform

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Multi-tenant workspaces | ⬜ | ✅ | ✅ | |
| Password sign-in, SSO (OIDC: Keycloak, Entra ID, Okta, Google) | 🟡 SAML | ✅ | ✅ tested against Keycloak | |
| Roles | ✅ granular | ✅ | 🟡 admin / editor | P1: custom roles |
| Audit log | 🟡 | ✅ | ⬜ | P1 |
| REST API with keys | ✅ | ✅ | ✅ | |
| Outbound webhooks on events | ✅ | ✅ | ⬜ | P1 |
| Integrations (CRM, Zapier, Shopify) | ✅ plugins | ✅ | ⬜ | P2 |
| Web, Android, iOS, Flutter SDKs | 🟡 JS | ✅ | 🟡 web SDK (track, identify with identity verification, page views, on-site, push token); mobile SDKs P1 | |

## Order to close the gaps

1. **P0, done:** consent and unsubscribe, email open and click tracking with unsubscribe links, CSV import and export,
   and the JS SDK (track, identify, on-site rendering, push tokens).
2. **P1 batch 1, done:** journey steps (wait until, wait for event, check contact, A/B split, update profile,
   webhook), goals, re-entry rules, frequency caps, quiet hours, transactional templates.
3. **P1 batch 2, done:** segment-in-segment rules, static lists, event-property filters, tags, points and scoring,
   attribute definitions, anonymous-to-known identity merge, and identity verification (signed user IDs) for the web SDK.
4. **P1 batch 3, done:** forms, preference center, SMS and WhatsApp reply keywords, broadcast A/B tests and conversion
   goals, journey and broadcast reports, funnels.
5. **P1, still to do — batch 4, platform:** custom roles, audit log, outbound webhooks, background imports, mobile SDKs,
   bounce and complaint handling, web push service worker, webhook channel.
6. **P2:** stages, companies, landing pages, assets, ad audiences, cohorts, revenue attribution, report builder, integrations.
