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
| Text and number operators (contains / doesn't contain, starts / ends with, is one of / not one of, is empty / not empty, is set / not set, between, on a date) | ✅ | ✅ | ✅ `not_contains`, `starts_with`, `ends_with`, `not_in`, `not_exists`, `between`, `on` in both compilers (checked by `make it`); empty = `eq ""` | |
| Searchable, grouped filter picker; typed rows; explicit nested ALL/ANY groups | ✅ | ✅ | ✅ batch 7b, see "Segment builder" below | |
| Device, locale, email domain, date added / identified / modified, owner | ✅ | ✅ (device) | ✅ profile attributes kept by Pulse from SDK events and the profile service | |
| Browsing sessions | ✅ | ✅ | ✅ `session_start` from the SDKs (30 minutes without activity), counted like any event | |
| SMS bounces, manual vs self-service unsubscribes | ✅ | 🟡 | ✅ `sms_bounced` (suppresses SMS until the number changes); `unsubscribed` carries `method: manual / self` | |
| SMS / RCS link click tracking | ✅ | ✅ | ✅ per-template "Track link clicks": short `/t/s/{code}` links, `sms_clicked` / `rcs_clicked` | WhatsApp buttons: not tracked (approved templates hold the URL) |

## Journeys / campaigns

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Visual canvas | ✅ | ✅ | ✅ "+" on every path opens Action · Decision · Condition · Wait · Split, then a searchable list (disabled items say why); drag-and-drop palette; Yes/No colours and labels, step numbers, Tidy up, zoom to fit, minimap, undo/redo, per-step counts. See [Campaign builder](#campaign-builder-batch-7b) | |
| Triggers: segment entry, event | ✅ | ✅ | ✅ | |
| Schedule: start required, end optional (empty = runs forever) | ✅ | ✅ | ✅ this change | |
| Wait for a duration | ✅ | ✅ | ✅ durable timers | |
| Wait until a date/time, or until an event (with timeout) | ✅ | ✅ | ✅ date, next time of day in the contact's zone, wait for event with timeout | |
| Condition: in segment | ✅ | ✅ | ✅ | |
| Condition: attribute value, opened/clicked, visited page | ✅ | ✅ | ✅ "Check contact" with any segment rule, including email_opened/clicked and page_view counts | |
| Decision on this journey's own message (email opened/clicked, WhatsApp/RCS read, SMS/push delivered, inbox opened) | ✅ | ✅ | ✅ `message_decision` step with a timeout, yes/no branches | |
| A/B split | ✅ | ✅ | ✅ 2-5 weighted paths, stable per enrollment | |
| Journey A/B winner (automatic) | ⬜ | ✅ | ✅ split step `winner`: by open, click or goal conversion rate after a test window and a minimum of contacts per path; recorded on the journey, then every later contact takes the winning path; shown in the inspector and the report | P2 |
| Send email, SMS, WhatsApp (SMS fallback), RCS (SMS fallback), push | 🟡 | ✅ | ✅ | |
| Update attribute, add/remove tag, change points, change stage | ✅ | ✅ | ✅ update profile, tag contact, adjust points, add to / remove from list, change stage | |
| Webhook step | ✅ | ✅ | ✅ SSRF-guarded, idempotency key, failures recorded without stalling | |
| Conversion goal and exit on goal | 🟡 | ✅ | ✅ goal event converts and ends the enrollment | |
| Re-entry rules (once, every time, after N days) | 🟡 | ✅ | ✅ one live enrollment at a time | |
| Frequency capping across journeys | ⬜ | ✅ | ✅ per channel, per contact, across journeys and broadcasts; transactional exempt | |
| Quiet hours / DND, send in contact's time zone | ⬜ | ✅ | ✅ messages are held and sent when quiet hours end, in the contact's zone | |
| Per-step stats | ✅ | ✅ | ✅ live: entered, Yes / No, per split path (Redis counters), contacts at the step now, sends and opens per step | |
| Every Mautic campaign decision, action and condition | ✅ | 🟡 | ✅ batch 7b, see [Campaign builder](#campaign-builder-batch-7b) | |
| Multi-channel message with fallback (first reachable channel) | ✅ marketing messages | ✅ | ✅ `send_message`: ordered channel → template list | |
| Jump to step (loops) | ✅ | ⬜ | ✅ `jump`, capped at 25 jumps per enrollment | |

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
| Funnels | ⬜ | ✅ | ✅ 2-8 steps with property filters, conversion window, date range, segment filter; live while editing | |
| Funnel breakdown by attribute | ⬜ | ✅ | ✅ any profile attribute (geo_country, plan, stage...): step counts for the top 10 values plus everyone else | P2 |
| Cohorts / retention | ⬜ | ✅ | ✅ cohorts by first seen or first time doing an event (with filters), by day, week or month; coming back = any activity or an event; segment filter; heat-map triangle with an all-cohorts row; saved reports | P2 |
| Revenue attribution, UTM | 🟡 | ✅ | ✅ web SDK keeps first/last-touch UTM (`first_utm_*`, `last_utm_*`, referrer domain; first kept once set) on identify and forms, the session's campaign on page views; revenue event and amount property per workspace; last-touch, first-touch and linear attribution to broadcasts, journeys and UTM campaigns over an N-day window; recipients' revenue in broadcast and journey reports | P2 |
| Custom report builder | ✅ | 🟡 | ✅ events, message outcomes or contacts; count, unique contacts, sum or average; group by event, property, attribute, outcome, channel, campaign or day/week/month; property/attribute, channel and segment filters; table, bars or line; saved reports; at most 100 groups, a year of data | P2 |

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
   - **Batch 7, analytics (done):** cohorts and retention (Analyze → Cohorts), revenue attribution and UTM capture
     (Analyze → Revenue; revenue in broadcast and journey reports), funnel breakdown by attribute, the custom report
     builder (Analyze → Reports), and automatic A/B winners on journey split steps.
   - **Batch 7b, builders like Mautic and WebEngage (requested 2026-11-11, right after batch 7), done ✅:**
     - ✅ **Segment builder (done 2026-11-11)**, see [Segment builder](#segment-builder-batch-7b) for the Mautic filter mapping.
       Requested: one searchable filter picker grouped as Contact (every built-in and custom field, with icons), Primary
       company, Behaviour and actions, Points; a clear nested "Match ALL / ANY" group UI. New data for Mautic's filters: device
       brand/model/OS/type (SDKs), email domain, date added / identified / modified, contact owner, preferred locale, SMS bounces,
       manual vs self-service unsubscribes, browsing sessions; behaviour filters for clicks (any channel / email / SMS, with
       dates), asset downloads, reads of a specific email or X times, sends, URL visits (X times, landing page, referrer,
       source, page title).
     - ✅ **Campaign builder (done 2026-11-11)**, see [Campaign builder](#campaign-builder-batch-7b) for the Mautic
       element mapping. "+" asks Action, Decision, Condition, Wait or Split, then a searchable list with descriptions;
       every Mautic decision, action and condition is there (push to integration waits for batch 8).
   - **Batch 8, integrations and reach:** CRM / Shopify / Zapier-style connectors, Facebook and Google audience sync, native
     Kotlin and Swift SDKs, RCS replies, SMTP bounce mailbox, web push click tracking, form file uploads and multi-page
     forms, audit log export.

9. **After batch 8 (requested 2026-11-11), in this order:**
   - **Batch 9, contact views and one clear navigation:**
     - Contacts: "View contacts" from any segment or list; Mautic-style search filters (`is:anonymous`, `is:identified`,
       `is:unsubscribed`, `is:bounced`, `segment:`, `list:`, `tag:`, `stage:`, `company:`, `owner:`, `points:>N`) as chips;
       save a filtered view as a segment.
     - Review the whole app for duplicated or ambiguous concepts and fix them to industry standard. Decided so far:
       Focus items + In-app messages become one "On-site & in-app" area (Website or App chosen first); Segments + Lists become
       one Segments page with Dynamic and Static segments (Mautic / WebEngage style); the Imports page and "Import CSV" become
       one Import flow under Contacts (upload, map columns, choose segment, review; large files run in the background; one
       history).
   - **Then:** update every doc to match the final app, and write step-by-step tutorials with the product owner.

## Campaign builder (batch 7b)

**What it is.** Journeys → a journey. Every outgoing path (an edge, or a free Yes / No / split handle) has a "+". It opens
"Add a step": first **Action** (do something), **Decision** (wait for the contact to do something: Yes if they do within
the time limit, No if not), **Condition** (check something now: Yes or No), **Wait** or **Split** (A/B or percentage), then
a searchable list of that kind's steps with a description each. Steps that can't go there are greyed with the reason
("Opens email: add a Send email step above it first", "Ends the path: add it at the end of a path", "Needs edit access to
contacts"). The step is inserted on that path and connected; its Yes and No paths get their own "+". The palette (drag and
drop, or Enter to add after the selected step) stays. The canvas numbers steps (used by "Jump to step"), colours Yes green
and No red with labels ("No — after 2 days"), shows split paths as "A · 50%", and draws jumps as dashed lines; the toolbar
has Undo / Redo (Ctrl/Cmd+Z, Ctrl/Cmd+Shift+Z), Tidy up (automatic layout), Zoom to fit, a minimap and "Show stats"
(entered, Yes / No, here now, sent / opened per step). The issues list ("Step 4 · Send SMS: choose a template.") mirrors
the server's validation. Web: `web/src/journeyCatalog.ts` (the chooser's items), `web/src/journeyGraph.ts` (graph edits,
insert, tidy, validation), `web/src/pages/journey/*`. Server: `internal/journey/nodes.go` (validation) and `steps.go`
(execution).

### Mautic campaign element mapping

| Mautic element | Pulse step (chooser label) | Node | Note |
|---|---|---|---|
| **Decisions** | | | all take a time limit (1 minute to 365 days); Yes when it happens, No at the limit |
| Opens email | Opens email | `message_decision` outcome `opened` | needs a Send email step above it (validated) |
| Clicks email | Clicks email | `message_decision` outcome `clicked`, optional `url` (link contains) | any link or a specific URL |
| Replies to email | Replies to email | `message_decision` outcome `replied` | needs reply tracking: Channels → Delivery "Reply tracking address" and the provider's inbound webhook (see architecture, "Inbound replies") |
| Downloads asset | Downloads asset | `on_asset`, optional `asset_id` | gated downloads by known contacts (`asset_downloaded`) |
| Submits form | Submits form | `on_form`, optional `form_id` | the submission is kept on the enrollment for "Form field value" |
| Visits a page | Visits a page | `on_page`: `url` contains, or a landing page `page_id` | web SDK `page_view` / `landing_page_viewed` |
| Device visit | Device visit | `on_device`: `device_type` and/or `os` | a page view from that device (SDK device properties) |
| Request dynamic content | Requests dynamic content | `on_dwc` `slot` | the decide API records `dwc_requested` for known contacts; put "Push dynamic content" on Yes |
| **Actions** | | | |
| Add Do Not Contact | Add Do Not Contact | `dnc_add` channel (or all), reason | `unsubscribed` with method manual, source journey |
| Remove Do Not Contact | Remove Do Not Contact | `dnc_remove` | `resubscribed` |
| Add to company | Add to company | `add_company` | links the contact (`company_linked`) |
| Update contact's primary company | Update contact's primary company | `add_company` | approximate: a Pulse contact has one company, so "primary" is the company |
| Add to company's score | Add to company's score | `company_points` | adds to the company's `score` attribute (once per step, even on retries); contacts see it as `company_score` |
| Update company (Mautic's company fields) | Update company | `update_company` `company_attrs` | the contact's linked company; skipped without one |
| Adjust contact points | Adjust contact points | `adjust_points` | |
| Change campaigns | Change campaigns | `change_journey` add / remove journeys | through a `journey_change` event; only active journeys enroll (their schedule and re-entry apply); removed enrollments get status `removed` |
| Change contact's stage | Change contact's stage | `change_stage` | |
| Delete contact | Delete contact | `delete_contact` `confirm: "DELETE"` | permanent, through the cleanup deletion path; typed confirmation; saving or activating it needs edit access to contacts |
| Jump to Event | Jump to step | `jump` `target` | ends the path; at most 25 jumps per enrollment, then the contact leaves (`journey_jump_limit`) |
| Modify contact's segments | Modify contact's segments | `update_list` `add_lists` / `remove_lists` | static lists only: dynamic segments follow their rules (the inspector says so) |
| Modify contact's tags | Modify contact's tags | `update_tags` | |
| Push contact to integration | Push contact to integration | — | **missing**: shown disabled, "coming with integrations (batch 8)" |
| Push dynamic content | Push dynamic content | `push_dwc` slot, HTML, days | per contact, returned by the decide API for that slot (wins over the slot's variants) |
| Send a webhook | Send a webhook | `webhook` | |
| Send email | Send email | `send_email` | also SMS, WhatsApp, RCS, push, app inbox steps |
| Send email to user | Send email to user | `notify_user` | users, the contact's owner and/or fixed addresses; the template rendered with the contact's data; not consent-gated, not tracked |
| Send marketing message | Send marketing message | `send_message` channels in order | approximate: Mautic's "marketing message" is a saved object; Pulse keeps the ordered channel → template list on the step and sends on the first channel the contact can receive and has consented to (address, consent, bounce, push device) |
| Show Focus Item | Show focus item | `show_focus` | shown to this contact on their next page view, once (even a draft item: journey-only) |
| Update contact | Update contact | `update_attr` | values may use `{contactfield=x}`, `{{.Attrs.x}}`, `now`, `now+3hours`, `now-2days` … |
| Update contact owner | Update contact owner | `update_owner` | `owner_changed`; 0 unassigns |
| **Conditions** | | | Yes / No right now |
| Contact campaigns | Contact campaigns | `if_journey` active / completed / entered | |
| Contact device | Contact device | `check` preset device | last-seen device attributes |
| Contact field value | Contact field value | `check` preset field | the segment builder's filters |
| Contact owner | Contact owner | `check` preset owner | |
| Contact points | Contact points | `check` preset points | |
| Contact segments | Contact segments | `condition` | segments and lists |
| Contact stages | Contact stages | `check` preset stage | |
| Contact tags | Contact tags | `check` preset tags | any / all of |
| Form field value | Form field value | `if_form_field` | the submission that started or resumed the enrollment, else the latest (ClickHouse) |
| Has active notification | Has active notification | `if_notification` push / inbox / any | defined as: a push device (and not opted out of push), or an unread, unexpired app inbox message |
| Has valid email address | Has valid email address | `if_valid_email` | syntax, a domain that accepts mail (MX, or A per RFC 5321; null MX refused; 3 s timeout, cached 6 h; DNS failures pass), optional disposable-domain list |
| Marked as DNC | Marked as Do Not Contact | `if_dnc` channel or any | unsubscribed from it or everything, or bounced (email, SMS) |
| Prevent action after date | Prevent action after date | `if_before` | Yes until the date |
| Visited page | Visited page | `if_visited` url contains, days (0 = ever) | ClickHouse page views ("ever" = all history kept) |

WebEngage extras in the same chooser: message outcome on any channel (delivered / read / clicked), wait for any event,
wait until a date or time of day, A/B split with an automatic winner, percentage split, goal exit, frequency caps and
quiet hours (delivery settings).

**Not covered / approximate:** push to integration (batch 8); Mautic's saved "marketing message" object (the list lives
on the step); a message decision can't watch a "Send marketing message" step (its channel is chosen per contact); device
visit uses web page views (not app screens); per-step counts are best effort (a retried step can count twice).

## Segment builder (batch 7b)

**What it is.** Segments → New segment. The rule is a set of nested boxes: the top one reads "Contacts who match
[ALL ▾ / ANY] of the following:", every nested box "Match [ALL ▾ / ANY] of the following" with its own "+ Add filter",
"+ Add group" and "Remove group", an indent and coloured border (ALL dark, ANY violet), and the joining word (AND / OR)
shown between its rows. Groups nest as deep as the backend allows (7 group levels; the filters take the 8th); "+ Add
group" is replaced by a note at the deepest level. Removing a nested group's last filter removes the group. The live
count (preview endpoint, debounced) and the rule in words (`describe()`) stay beside the builder.

**Picker.** "+ Add filter" opens one searchable list (search box = combobox; ↑ ↓ move, Enter adds, Esc closes and
returns focus) grouped Contact, Company, Location, Device, Consent & deliverability, Behaviour (messages, web, forms &
assets, journeys, any event), Points & stage, Segments & tags, with a group rail, an icon and a one-line hint per item,
and the six most recently used filters first (per browser). Search covers labels, hints, attribute keys and group names.
Every attribute definition (`GET /v1/attributes`) and every key seen on contacts is listed with its type (date, number,
yes/no, text), so workspace fields such as "MFI Subscription End Date" or "User is Fund Subscriber" appear typed without
being hard-coded. Clicking a row's field button reopens the picker to change that filter.

**Rows.** [Field] [operator for its type] [value input for its type] [×]: text (is, is not, contains, doesn't contain,
starts / ends with, is one of / is not one of as chips, is empty / is not empty) with the field's common values
suggested (`GET /v1/attributes/{key}/values`, a 200k-row sample, cached 10 minutes; event properties use
`GET /v1/events/{name}/values?prop=`); numbers (=, ≠, >, ≥, <, ≤, between); yes/no; dates (in the last N days, more than
N days ago, on, before, after, set / not set); stage, company and owner pick from the workspace's own. Behaviour filters
are sentences: "[Opened an email ▾] [the email ▾ Welcome ▾] [at least ▾] [1] times in the last [30] days", with
"+ Only count events where…" for property filters and "+ A specific message" for message events.

### Mautic filter mapping

Every filter in Mautic's list maps to a picker entry. "Approximate" says how Pulse's meaning differs.

| Mautic filter | Pulse picker entry (group) | Rule | Note |
|---|---|---|---|
| Address Line 1 | Address line 1 (Contact) | `address1` text | standard field name; filled by your data |
| Attribution | Attribution (Contact) | `attribution` number | approximate: Mautic's campaign-set field; Pulse fills it only if your data sets it (revenue attribution is a report, not a contact field) |
| Attribution Date | Attribution date (Contact) | `attribution_date` date | approximate, as above |
| Bounced - Email | Bounced: email (Consent) | `email_bounced` yes/no | |
| Bounced - SMS | Bounced: SMS (Consent) | `sms_bounced` yes/no | new: a permanent "bad number" failure |
| Campaign Membership | Entered a journey / Completed a journey (Behaviour: journeys) | `journey_entered` / `journey_completed` with `journey_id` | approximate: "entered (or completed) in the last N days", at most 365; "currently in a journey" isn't a rule |
| City / Country / State / Zip Code | City, Country, State, Zip code (Location); also City / Country / State or region / Postal code (from IP) | `city`, `country`, `state`, `zip`; `geo_*` | |
| Date Added | Date added (Contact) | `date_added` date | new; existing contacts get it at their next event |
| Date Identified | Date identified (Contact) | `date_identified` date | new: first event with a known id or an email |
| Date Last Active | Date last active (Contact) | `last_seen` date | |
| Device Brand / Model / OS / Type | Device brand, Device model, Device OS, Device type (Device); also OS version, Browser, App version | `device_*`, `browser`, `app_version` | new; last-seen device from the SDKs |
| Email | Email (Contact) | `email` | |
| Email Domain | Email domain (Contact) | `email_domain` | new, lower case |
| First Name / Last Name | First name, Last name (Contact) | `first_name`, `last_name` | |
| Mobile | Mobile (Contact) | `phone` | |
| Modified Date | Modified date (Contact) | `date_modified` date | new; a day, not a time (avoids a write per event) |
| Owner | Owner (Contact) | `owner_id` picked from users | new: `PUT /v1/contacts/{id}/owner`, drawer picker |
| Points | Points (Points & stage) | `points` number | |
| Preferred Locale | Preferred locale (Contact) | `locale` | new; SDKs send it, servers may set it |
| Segment Membership | Segment membership (Segments & tags) | `segment` in / not in | |
| Stage | Stage (Points & stage) | `stage` picked from stages | |
| Tags | Tags (Segments & tags) | `tag` has / not_has | |
| UTM Campaign / Content / Medium / Source / Term | UTM campaign, content, medium, source, term (Contact); also First UTM source / campaign | `last_utm_*`, `first_utm_*` | Mautic keeps a UTM history; Pulse keeps first and last touch |
| Unsubscribed - Email / SMS | Unsubscribed from email / SMS (Consent); also WhatsApp, RCS, push, everything | `optout_*` yes/no | |
| Unsubscribed Manual - Email / SMS | Unsubscribed manually: email / SMS (Consent) | group: `optout_<ch>` is yes AND `unsubscribed` where channel and `method = manual` in 365 days | approximate: the manual unsubscribe must be within a year |
| Every custom field | listed by name with its type (Contact / Company / Consent) | attribute rule | from attribute definitions and keys seen on contacts |
| Company Name (primary company) and company fields | Company, Company name, every `company_*` key, Other company field (Company) | `company_id`, `company_name`, `company_*` | |
| Clicked any link from any channel | Clicked any link from any channel (Behaviour: messages) | ANY group of `email_clicked`, `sms_clicked`, `rcs_clicked`, `inbox_clicked`, `inapp_clicked` ≥ 1 | exact for "at least once"; push clicks aren't tracked yet |
| Clicked any link from any email (+ date) | Clicked any link from any email | `email_clicked` count in N days | approximate: "in the last N days" instead of a date comparison |
| Clicked any link from any text message (+ date) | Clicked any link from any text message | `sms_clicked` count in N days | needs "Track link clicks" on the SMS template; same date note |
| Downloaded a specific asset | Downloaded a specific asset (Behaviour: forms & assets) | `asset_downloaded` where `asset_id` | form-gated downloads by known contacts (open downloads are anonymous) |
| Has X number of browsing sessions | Has X number of browsing sessions (Behaviour: web) | `session_start` count in N days | |
| Has been sent any push notification | Has been sent any push notification | `push_sent` ≥ 1 | |
| Read a specific email | Read a specific email | `email_opened` where `template_id` | also by campaign or journey |
| Read any email (date) | Read any email | `email_opened` ≥ 1 in N days | date note as above |
| Read any email X number of times | Read any email X number of times | `email_opened` count | |
| Sent any email (date) | Sent any email | `email_sent` ≥ 1 in N days | date note as above |
| Visited X URL | Visited a URL | `page_view` where `url` contains | any text operator |
| Visited a specific landing page | Visited a specific landing page | `landing_page_viewed` where `page_id` | |
| Visited any URL (date) | Visited any URL | `page_view` ≥ 1 in N days | date note as above |
| Visited any URL X number of times | Visited any URL X number of times | `page_view` count | |
| Visited any URL from X referrer | Visited any URL from a referrer | `page_view` where `referrer` contains | |
| Visited any URL from X source | Visited any URL from a source | `page_view` where `utm_source` | |
| Visited any URL from X source ID | Visited any URL from a campaign (source ID) | `page_view` where `utm_campaign` | approximate: Mautic's source id is the id of the email/page that sent the visit; Pulse uses the campaign name |
| Visited any URL with X page title | Visited a page with a title | `page_view` where `title` contains | |
| Was sent a specific email | Was sent a specific email | `email_sent` where `template_id` | |
| Contact group points | Points | `points` | approximate: Pulse has one score, no point groups |

WebEngage extras in the same picker: reachable by email / SMS (has the address, not unsubscribed, not bounced), first
seen, inactive (last active more than N days ago), any custom event done / not done with up to 5 property filters,
WhatsApp read, SMS delivered, app inbox and in-app engagement, form submissions, stage changes.

**Not covered:** event windows are "the last 1-365 days" (no absolute date range for behaviour, no "ever"); "currently in
a journey"; point groups; Mautic's regexp operator.
