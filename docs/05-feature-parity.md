# Feature parity: Mautic 7 + WebEngage → Pulse

Sources: the Mautic 7.2.2 install in `../mautic` (its bundles, plugins and console commands) and WebEngage's product surface.
Status: ✅ built and tested · 🟡 partial · ⬜ not built. Priority: **P0** blocks real-world use (compliance, tracking),
**P1** core marketer workflow, **P2** advanced or nice to have.

## Contacts and data

| Feature | Mautic | WebEngage | Pulse | Priority / note |
|---|---|---|---|---|
| Contact profiles, custom attributes | ✅ | ✅ | ✅ JSON attributes, no schema needed | |
| Anonymous visitor tracking, GeoIP location | ✅ | ✅ | ✅ anonymous visitors (on/off per workspace), MaxMind GeoIP city/state/country/time zone | |
| Data retention and cleanup | ✅ | 🟡 | ✅ Data & privacy: anonymous visitors, events, audit log; hourly cleanup | |
| Real-time event tracking API | 🟡 | ✅ | ✅ 20k events/s on one machine | |
| Activity timeline | ✅ | ✅ | ✅ | |
| Unsubscribe, do-not-contact, per-channel consent | ✅ | ✅ | ✅ per channel and "everything", honored by every send (`*_suppressed`), editable per contact | |
| Preference center | ✅ | 🟡 | ✅ per-channel switches and public lists; `{{.PreferencesURL}}`, unsubscribe page link, API-minted links | |
| SMS / WhatsApp reply keywords (STOP, START) | ✅ | ✅ | ✅ Twilio and Meta inbound webhooks; editable keyword lists; replies recorded as `<channel>_received` | RCS replies: P2 |
| CSV import and export | ✅ | ✅ | ✅ import through the live pipeline; export with one column per attribute; background imports of any size (up to 10 GB) with progress and cancel | |
| Attribute definitions (type, label, PII flag) | ✅ | ✅ | ✅ Settings, Attributes; typed inputs in the segment builder; imports convert numbers and yes/no | |
| Tags | ✅ | 🟡 | ✅ `tag_added` / `tag_removed` events, API, contact drawer, CSV `tags` column, "is tagged" rules | |
| Points / lead scoring | ✅ | ⬜ | ✅ scoring rules per event, manual adjust, journey step; `points` attribute in rules | |
| Stages (lifecycle) | ✅ | ⬜ | ✅ ordered stages (Workspace, Stages); `stage` attribute changed by hand, API, `stage_changed` events, the `change_stage` journey step, or a points threshold (forward only); "In stage" segment preset | P2 |
| Companies (B2B accounts) | ✅ | ⬜ | ✅ companies with attributes copied to contacts as `company_<key>`; link by hand or by a server identify with `attrs.company`; company changes fan out to contacts; "Works at company" and "Company attribute" presets | P2 |
| Identity merge (anonymous → known) | ✅ | ✅ | ✅ identify with `anonymous_id`: profile, counters and event history move; later visitor events follow | |
| Identity verification for browser SDKs (signed user ID) | ⬜ | ✅ | ✅ HMAC user hash; can be required per workspace; secret rotation | |
| Duplicate merge | ✅ | ✅ | ✅ `POST /v1/contacts/merge` on the identity-merge path (attributes, tags, points, history, counters, lists move to the winner); Duplicates view by email or phone; "Merge into…" in the contact drawer | P2 |

## Segments

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Attribute and event-count rules, AND/OR groups | ✅ | ✅ | ✅ | |
| Real-time membership (no cron) | ⬜ (15-min cron) | ✅ | ✅ ~60 ms | |
| Live size and preview | 🟡 | ✅ | ✅ | |
| "In / not in another segment" rule | ✅ | ✅ | ✅ evaluated in dependency order in the same pass; cycles refused | |
| Static lists (manual add/remove, import to list) | ✅ | ✅ | ✅ Lists page, API, CSV import into a list, journey step | |
| Event property filters (purchase where amount > 1000) | 🟡 | ✅ | ✅ up to 5 filters per event rule; history seeded from ClickHouse | |
| Date rules (before/after a date, within the last N days, more than N days ago) | ✅ | ✅ | ✅ on any date attribute and on first_seen / last_seen, which every profile keeps | |

## Journeys / campaigns

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Visual canvas | ✅ | ✅ | ✅ grouped palette: Actions, Conditions, Decisions, Waits | |
| Triggers: segment entry, event | ✅ | ✅ | ✅ | |
| Schedule: start required, end optional (empty = runs forever) | ✅ | ✅ | ✅ this change | |
| Wait for a duration | ✅ | ✅ | ✅ durable timers | |
| Wait until a date/time, or until an event (with timeout) | ✅ | ✅ | ✅ date, next time of day in the contact's zone, wait for event with timeout | |
| Condition: in segment | ✅ | ✅ | ✅ | |
| Condition: attribute value, opened/clicked, visited page | ✅ | ✅ | ✅ "Check contact" with any segment rule, including email_opened/clicked and page_view counts | |
| Decision on this journey's own message (email opened/clicked, WhatsApp/RCS read, SMS/push delivered, inbox opened) | ✅ | ✅ | ✅ `message_decision` step with a timeout, yes/no branches | |
| A/B split | ✅ | ✅ | ✅ 2-5 weighted paths, stable per enrollment | Winner auto-selection: P2 |
| Send email, SMS, WhatsApp (SMS fallback), RCS (SMS fallback), push | 🟡 | ✅ | ✅ | |
| Update attribute, add/remove tag, change points, change stage | ✅ | ✅ | ✅ update profile, tag contact, adjust points, add to / remove from list, change stage | |
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
| Email: drag-and-drop builder, HTML, personalization | ✅ | ✅ | ✅ GrapesJS builder, TipTap rich text editor (also on landing pages, focus items, web content), HTML; merge-tag menu | |
| Preview (with a real contact's data) and send a test, every channel | ✅ | ✅ | ✅ this change | |
| Provider connection test, per provider | 🟡 | ✅ | ✅ this change | |
| Email open and click tracking | ✅ | ✅ | ✅ sealed tokens; click links signed (no open redirect); `email_opened` / `email_clicked` events | |
| Unsubscribe link and List-Unsubscribe header | ✅ | ✅ | ✅ footer or `{{.UnsubscribeURL}}`, RFC 8058 one-click, confirm page | |
| Bounce and complaint handling | ✅ | ✅ | ✅ SendGrid, Postmark, Mailgun, Amazon SES (SNS) and generic webhooks; hard bounces stop email, complaints unsubscribe | SMTP DSN mailbox parsing: P2 |
| Plain-text part, attachments | ✅ | 🟡 | ✅ every email is multipart/alternative: the template's own text or one generated from the HTML (links as "text (url)", direct, plus the unsubscribe link); up to 10 asset attachments (10 MB together) in multipart/mixed; the custom gateway gets signed 7-day download links; preview shows the text part and the files | P2 |
| SMS (Twilio, MSG91) | ✅ | ✅ | ✅ adapters, not tried on live accounts | |
| WhatsApp (Meta Cloud) with SMS fallback | ⬜ | ✅ | ✅ | |
| RCS (Google RBM) with SMS fallback | ⬜ | ✅ | ✅ | |
| Mobile push (FCM) | ✅ | ✅ | ✅ adapter, tokens via API | SDK next |
| Web push | ✅ | ✅ | ✅ standard Web Push with per-workspace VAPID keys (no Firebase); `Pulse.enablePush()` and `/sdk/pulse-sw.js`; FCM web tokens still work | Click tracking: P2 |
| App inbox | ⬜ | ✅ | ✅ `inbox` channel for journeys and broadcasts (templates, consent, caps, reports); SDK API to list, read, delete, click; per-message expiry; `Pulse.inbox` in the web SDK | |
| In-app messages (modal, banner, full screen) | ⬜ | ✅ | ✅ campaigns with segment, platform, trigger event or screen, delay, frequency, priority, schedule; editor with phone preview, "see what a contact gets" and test send; rendered by the Flutter SDK; shown/clicked/dismissed stats | |
| Webhook channel | ✅ | ✅ | ✅ custom HTTP gateway provider for every channel (signed JSON, receipts and inbound replies back); journey webhook step | |
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
| Landing pages | ✅ | ⬜ | ✅ `/p/{workspace}/{page}`, drag-and-drop, rich text or HTML, title and meta description, embedded forms (`{form:KEY}`), draft/published with a schedule; nonce CSP (only the Pulse SDK runs, scripts and handlers stripped), frameable; `landing_page_viewed` via the web SDK; views, unique visitors, form submissions; script-free preview | P2 |
| Assets (gated downloads) | ✅ | ⬜ | ✅ uploads up to 25 MB (type allowlist checked against file signatures) kept in Postgres; `/a/{key}` open or gated by a form (15-minute sealed link, `asset_downloaded` for the submitter); always `Content-Disposition: attachment`, nosniff, sandbox CSP; download counts; email attachments | P2 |

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
| Roles | ✅ granular | ✅ | ✅ admin, editor, viewer and custom roles: none/view/edit/publish per area, enforced on every route | |
| Audit log | 🟡 | ✅ | ✅ every change through the app or API (secrets redacted) and every sign-in | Retention settings, export: P2 |
| REST API with keys | ✅ | ✅ | ✅ | |
| Outbound webhooks on events | ✅ | ✅ | ✅ any event or segment entry/exit; HMAC-signed; retries over ~7 hours; delivery log; test send | |
| Integrations (CRM, Zapier, Shopify) | ✅ plugins | ✅ | ⬜ | P2 |
| Web, Android, iOS, Flutter SDKs | 🟡 JS | ✅ | ✅ web SDK (track, identify, page views, on-site, forms, web push); Flutter SDK for Android and iOS apps (`sdks/flutter/pulse_sdk`) | Native Kotlin and Swift SDKs: P2 |

## Order to close the gaps

1. **P0, done:** consent and unsubscribe, email open and click tracking with unsubscribe links, CSV import and export,
   and the JS SDK (track, identify, on-site rendering, push tokens).
2. **P1 batch 1, done:** journey steps (wait until, wait for event, check contact, A/B split, update profile,
   webhook), goals, re-entry rules, frequency caps, quiet hours, transactional templates.
3. **P1 batch 2, done:** segment-in-segment rules, static lists, event-property filters, tags, points and scoring,
   attribute definitions, anonymous-to-known identity merge, and identity verification (signed user IDs) for the web SDK.
4. **P1 batch 3, done:** forms, preference center, SMS and WhatsApp reply keywords, broadcast A/B tests and conversion
   goals, journey and broadcast reports, funnels.
5. **P1 batch 4, done:** custom roles, audit log, outbound webhooks, background imports, the Flutter SDK, bounce and
   complaint handling, standard web push, and the custom HTTP gateway (webhook channel).
   **P1 is complete:** in-app messages and the app inbox are done too.
6. **Batch 5 (requested 2026-11-10), done:**
   - Anonymous visitors: GeoIP with a MaxMind GeoLite2/GeoIP2 City database (`PULSE_GEOIP_DB`) sets geo_country,
     geo_country_code, geo_region, geo_city, geo_postal, geo_timezone; quiet hours fall back to geo_timezone; Data &
     privacy switch to track anonymous visitors or not.
   - Cleanup like Mautic's maintenance (hourly): anonymous visitors idle for N days with their events, events older than N
     days, audit entries older than N days; expired inbox messages and old webhook deliveries are always pruned.
   - Journeys: palette grouped into Actions, Conditions, Decisions and Waits; `message_decision` on a step's own message
     (email opened/clicked/delivered, SMS/push delivered, WhatsApp/RCS delivered/read, inbox opened/clicked).
   - Segments: date rules, first_seen / last_seen, and an "Add condition" menu with presets for profile, location, first
     seen / last active / inactive, events done or not done, page visits, form submissions, email/WhatsApp/SMS engagement
     with a campaign or journey, unsubscribes, bounces, journey entered/completed, tags and segment/list membership.
   - Decision kept: contact records stay in Postgres (atomic identity, point reads and updates); ClickHouse keeps its
     mirror for every query. Revisit with a Redis profile cache if Postgres becomes the bottleneck.
7. **P2, in this order:**
   - **Batch 6, contacts and content (done):** stages, companies and duplicate merge; plain-text email part and
     attachments, landing pages, assets (gated downloads).
   - **Requested 2026-11-11 (done):** a CKEditor-style rich text editor (`web/src/RichText.tsx`: headings, bold/italic/
     underline/strike, colour and highlight, links with a new-tab option, lists, quotes, alignment, rules, images by URL or
     from Assets, tables with row/column/merge tools, remove formatting, undo/redo, merge tags, a Source view) on every
     HTML field: email templates (a Rich text tab beside the drag-and-drop builder and HTML), landing pages (Rich text tab,
     "Insert form" puts `{form:KEY}` at the cursor), focus item bodies and web content slots. Built on TipTap (MIT) to avoid
     CKEditor 5's GPL/commercial licence. HTML the editor can't keep (inline-styled layout tables, `<style>`, comments)
     opens in Source with a note instead of being rewritten. Left as plain text, because they're rendered as text: form
     descriptions (hosted form and web SDK) and in-app / inbox message bodies (the Flutter SDK draws `Text`).
   - **Batch 7, analytics:** cohorts and retention, revenue attribution and UTM, funnel breakdown by attribute, custom report
     builder, automatic A/B winners in journeys.
   - **Batch 7b, builders like Mautic and WebEngage (requested 2026-11-11, right after batch 7):**
     - Segment builder: one searchable filter picker grouped as Contact (every built-in and custom field, with icons), Primary
       company, Behaviour and actions, Points; a clear nested "Match ALL / ANY" group UI. New data for Mautic's filters: device
       brand/model/OS/type (SDKs), email domain, date added / identified / modified, contact owner, preferred locale, SMS bounces,
       manual vs self-service unsubscribes, browsing sessions; behaviour filters for clicks (any channel / email / SMS, with
       dates), asset downloads, reads of a specific email or X times, sends, URL visits (X times, landing page, referrer,
       source, page title).
     - Campaign builder: "+" asks Decision, Action or Condition, then a searchable list with descriptions. Decisions: opens /
       clicks / replies to email, downloads asset, submits form, visits page, device visit, requests dynamic content. Actions:
       send email / email to user / marketing message / webhook, show focus item, push dynamic content, update contact /
       owner / company, change stage, adjust points, tags, segments and lists, add to company, company score, change
       campaigns, jump to step, add/remove Do Not Contact, delete contact (confirmed), push to integration (with batch 8).
       Conditions: contact field value, segments, tags, stages, points, owner, device, campaigns, form field value, visited
       page, valid email, DNC, active notification, prevent action after date.
   - **Batch 8, integrations and reach:** CRM / Shopify / Zapier-style connectors, Facebook and Google audience sync, native
     Kotlin and Swift SDKs, RCS replies, SMTP bounce mailbox, web push click tracking, form file uploads and multi-page
     forms, audit log export.
