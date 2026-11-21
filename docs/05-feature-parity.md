# Feature parity: Mautic 7 + WebEngage → Pulse

Sources: a local Mautic 7.2.2 reference install in `../mautic`, next to this repository and not part of it (its bundles, plugins and console commands), and WebEngage's product surface.
Status: ✅ built and tested · 🟡 partial · ⬜ not built. Priority: **P0** blocks real-world use (compliance, tracking),
**P1** core marketer workflow, **P2** advanced or nice to have. Everything planned is built; what is still missing or
approximate is listed once under [Known gaps](#known-gaps).

## Contacts and data

| Feature | Mautic | WebEngage | Pulse | Priority / note |
|---|---|---|---|---|
| Contact profiles, custom attributes | ✅ | ✅ | ✅ JSON attributes, no schema needed | |
| Anonymous visitor tracking, GeoIP location | ✅ | ✅ | ✅ anonymous visitors (on/off per workspace), MaxMind GeoIP city/state/country/time zone | |
| Data retention and cleanup | ✅ | 🟡 | ✅ M5: Settings → Automation & limits: events, message events, page / screen views, per event name, anonymous visitors, audit log, import history, webhook deliveries, integration log, test inbox, live feed size; "What will be deleted" before a shorter retention is saved; cleanup once a day per workspace | |
| Delete a contact | ✅ | ✅ | ✅ **Delete contact** on the profile (typed confirmation) and `DELETE /v1/contacts/{external_id}`: the cleanup deletion path (ClickHouse events and memberships, then profile, devices, inbox, journey state, form uploads); contacts edit access; audited; also the `delete_contact` journey step | Ad audiences aren't recalled |
| Export one contact's data (access request) | 🟡 | ✅ | ✅ **Download data** on the profile and `GET /v1/contacts/{external_id}/data`: profile, consent per channel, segments and up to 100,000 events as JSON; contacts edit access (personal data, like exports); audited | |
| Real-time event tracking API | 🟡 | ✅ | ✅ 20k events/s on one machine | |
| Activity timeline | ✅ | ✅ | ✅ | |
| Unsubscribe, do-not-contact, per-channel consent | ✅ | ✅ | ✅ per channel and "everything", honored by every send (`*_suppressed`), editable per contact | |
| Preference center | ✅ | 🟡 | ✅ per-channel switches and public static segments (shown to contacts as subscription lists); `{{.PreferencesURL}}`, unsubscribe page link, API-minted links | |
| SMS / WhatsApp reply keywords (STOP, START) | ✅ | ✅ | ✅ Twilio and Meta inbound webhooks; editable keyword lists; replies recorded as `<channel>_received`; RCS replies and suggestion taps (Google RBM webhook) with STOP/START and `rcs_replied` for journeys | RCS: a reply is matched to the latest RCS message of the last 7 days (RBM doesn't link them) |
| CSV import and export | ✅ | ✅ | ✅ one Import flow under Contacts (upload, map columns, choose or create a static segment, review, run) for files of any size up to 10 GB, in the background with progress, cancel and one history; export of the whole workspace or of a filtered view, with the columns shown | |
| Attribute definitions (type, label, PII flag) | ✅ | ✅ | ✅ Settings → Contact fields; typed inputs in the segment builder; imports convert numbers and yes/no | |
| List and choice (enum) contact fields | 🟡 | ✅ | ✅ M1: `list` fields (e.g. `products`, has / doesn't have, imports split on \| or commas, filled by event schemas) and `enum` fields with options (a dropdown in segments and on the profile); shown and edited on the contact page | |
| Phone country | ⬜ | ✅ | ✅ M1: `phone_country` (ISO code) kept by Pulse from numbers written with + or 00; "Phone country" segment filter | National numbers without a country code get none |
| Event schemas (typed properties, enums, dependent enums, checks) | ⬜ | ✅ | ✅ M1: Audience → Events (volume, discovered properties, schema editor, problems with samples), warn (default) or enforce per event, six suggested e-commerce schemas, personal-data flag, "add to list field"; see [architecture](02-architecture.md#event-schemas-m1) | Browser events are never refused |
| Contact list views: search filters as chips, columns, save as segment | ✅ | ✅ | ✅ batch 9, see [Contact views](#contact-views-batch-9) | |
| Contact page: reach per channel and why not, devices, campaigns, activity, purchases, messages with reasons, events | 🟡 | ✅ | ✅ M4: `/contacts/:id` (`GET /v1/contacts/{id}/profile`, `/messages`, `/events`) | |
| Drill-down from every number to its contacts | ⬜ | 🟡 | ✅ M7: estimate steps, reach, segment sizes, campaign funnels and reasons, journey steps, dashboards, events, forms, AMP poll answers and on-site overlap open the Contacts list filtered to exactly those people (set filters on Redis bitmaps, outcome filters in ClickHouse; chips in words); "Create segment from this list" makes a static segment; see [Contacts behind every number](#contacts-behind-every-number-m7) | Not linked where no list of people exists: journey path counts, audience at send time, funnels and cohorts, Campaign performance totals (each campaign's numbers link) |
| Tags | ✅ | 🟡 | ✅ `tag_added` / `tag_removed` events, API, contact drawer, CSV `tags` column, "is tagged" rules | |
| Points / lead scoring | ✅ | ⬜ | ✅ scoring rules per event, manual adjust, journey step; `points` attribute in rules | |
| Stages (lifecycle) | ✅ | ⬜ | ✅ ordered stages (Settings → Stages); `stage` attribute changed by hand, API, `stage_changed` events, the `change_stage` journey step, or a points threshold (forward only); "In stage" segment preset | P2 |
| Companies (B2B accounts) | ✅ | ⬜ | ✅ companies with attributes copied to contacts as `company_<key>`; link by hand or by a server identify with `attrs.company`; company changes fan out to contacts; "Company", "Company name" and "Other company field" segment filters | P2 |
| Identity merge (anonymous → known) | ✅ | ✅ | ✅ identify with `anonymous_id`: profile, counters and event history move; later visitor events follow | |
| Identity verification for browser SDKs (signed user ID) | ⬜ | ✅ | ✅ HMAC user hash; can be required per workspace; secret rotation | |
| Duplicate merge | ✅ | ✅ | ✅ `POST /v1/contacts/merge` on the identity-merge path (attributes, tags, points, history, counters, static segments move to the winner); Duplicates view by email or phone; "Merge into…" in the contact drawer | P2 |
| Identity keys: which fields identify a person, in priority order, automatic merge | 🟡 | ✅ | ✅ O1: Settings → Identity: external id, email, phone and any contact field (user name, customer number…), ordered; per key "merge automatically" or "only list under Duplicates"; matches on new events in real time (`contact_keys`), existing contacts filled in by a background backfill; Duplicates gets a tab per key; an external id that is an email or phone also fills `email` / `phone` | Merging can't be undone; shared family emails/phones should use "only list"; only server events (API key, imports, integrations) merge — browser / app SDK events and anonymous visitors never do; contacts sharing a value before a key is added aren't merged in bulk; two contacts created in the same batch with the same new value aren't merged (they show under Duplicates) |

## Segments

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Attribute and event-count rules, AND/OR groups | ✅ | ✅ | ✅ | |
| Real-time membership (no cron) | ⬜ (15-min cron) | ✅ | ✅ ~60 ms | |
| Live size and preview | 🟡 | ✅ | ✅ | |
| "In / not in another segment" rule | ✅ | ✅ | ✅ evaluated in dependency order in the same pass; cycles refused | |
| Static segments (manual add/remove, import into one) | ✅ | ✅ | ✅ Segments page (type Static), the `/v1/lists` API, the Import flow, forms, journey step; offered on the preference center when public | |
| Event property filters (purchase where amount > 1000) | 🟡 | ✅ | ✅ up to 5 filters per event rule; history seeded from ClickHouse; M1: nested paths (`product.code`) and any list item (`items[].code`), inputs typed by the event's schema | |
| Absolute date ranges for behaviour (between / since dates) | 🟡 | ✅ | ✅ M1: `from` / `to` on event rules; ranges starting more than 365 days back are evaluated by the sweep (the builder says so); "is between" two dates on date fields | |
| Exclusion group ("none of") | 🟡 | ✅ | ✅ M1: `none` groups (Match NONE / "+ Exclude (none of)") | |
| Daily-refresh segments | ⬜ | ✅ | ✅ M1: Refresh Live (default) or Daily at HH:MM in a time zone; daily segments leave the streaming index and are rebuilt by the batch path at that time | |
| Why is this contact in this segment | ⬜ | 🟡 | ✅ M1: per segment on the contact page, rule by rule with the contact's values and event counts (`GET /v1/contacts/{id}/segments/{id}/explain`) | |
| Reachable contacts per channel | ⬜ | ✅ | ✅ M3: "Reachable by channel" on a segment (not unsubscribed, and an address that hasn't bounced, not Do Not Contact), exact, from Redis reachability bitmaps (`GET /v1/segments/{id}/reach`) | |
| Copy filter groups, duplicate segments | 🟡 | 🟡 | ✅ M3: Copy / Paste on every group (any segment, any tab; checked against the rule limits); Duplicate for dynamic segments | Static segments: export and import |
| Date rules (before/after a date, within the last N days, more than N days ago) | ✅ | ✅ | ✅ on any date attribute and on first_seen / last_seen, which every profile keeps | |
| Text and number operators (contains / doesn't contain, starts / ends with, is one of / not one of, is empty / not empty, is set / not set, between, on a date) | ✅ | ✅ | ✅ `not_contains`, `starts_with`, `ends_with`, `not_in`, `not_exists`, `between`, `on` in both compilers (checked by `make it`); empty = `eq ""` | |
| Searchable, grouped filter picker; typed rows; explicit nested ALL/ANY groups | ✅ | ✅ | ✅ batch 7b, see "Segment builder" below | |
| Device, locale, email domain, date added / identified / modified, owner | ✅ | ✅ (device) | ✅ profile attributes kept by Pulse from SDK events and the profile service | |
| Browsing sessions | ✅ | ✅ | ✅ `session_start` from the SDKs (30 minutes without activity), counted like any event | |
| SMS bounces, manual vs self-service unsubscribes | ✅ | 🟡 | ✅ `sms_bounced` (suppresses SMS until the number changes); `unsubscribed` carries `method: manual / self` | |
| SMS / RCS link click tracking | ✅ | ✅ | ✅ per-template "Track link clicks": short `/t/s/{code}` links, `sms_clicked` / `rcs_clicked` | WhatsApp buttons: not tracked (approved templates hold the URL) |

## Journeys / campaigns

Since M7 the app calls journeys **triggered** (event or form trigger) and **automation** (segment trigger) campaigns,
and broadcasts **one-time campaigns**, all under one **Campaigns** entry.

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Visual canvas | ✅ | ✅ | ✅ "+" on every path opens Action · Decision · Condition · Wait · Split, then a searchable list (disabled items say why); drag-and-drop palette; Yes/No colours and labels, step numbers, Tidy up, zoom to fit, minimap, undo/redo, per-step counts. See [Campaign builder](#campaign-builder-batch-7b) | |
| Triggers: segment entry, event, form submission | ✅ | ✅ | ✅ `segment_entered`, `event`, `form_submitted` (any form or `form_id`; the submission is kept for form field conditions); activation reaches the journey workers at once (Redis pub/sub `journeys:changed`, plus a 2-second refresh) | |
| Delete or archive a journey | ✅ | ✅ | ✅ delete one that never ran (list and `DELETE /v1/journeys/{id}`); archive one that ran (`POST /v1/journeys/{id}/archive` / `unarchive`): hidden from the list unless "Show archived", can't go live, enrollments and reports kept | |
| Schedule: start required, end optional (empty = runs forever) | ✅ | ✅ | ✅ Phase 2 | |
| Wait for a duration | ✅ | ✅ | ✅ durable timers | |
| Wait until a date/time, or until an event (with timeout) | ✅ | ✅ | ✅ date, next time of day in the contact's zone, wait for event with timeout | |
| Condition: in segment | ✅ | ✅ | ✅ | |
| Condition: attribute value, opened/clicked, visited page | ✅ | ✅ | ✅ "Check contact" with any segment rule, including email_opened/clicked and page_view counts | |
| Decision on this journey's own message (email opened/clicked, WhatsApp/RCS read, SMS/push delivered, inbox opened) | ✅ | ✅ | ✅ `message_decision` step with a timeout, yes/no branches | |
| A/B split | ✅ | ✅ | ✅ 2-5 weighted paths, stable per enrollment | |
| Journey A/B winner (automatic) | ⬜ | ✅ | ✅ split step `winner`: by open, click or goal conversion rate after a test window and a minimum of contacts per path; recorded on the journey, then every later contact takes the winning path; shown in the inspector and the report | P2 |
| Send email, SMS, WhatsApp (SMS fallback), RCS (SMS fallback), push | 🟡 | ✅ | ✅ | |
| Update attribute, add/remove tag, change points, change stage | ✅ | ✅ | ✅ update profile, tag contact, adjust points, add to / remove from a static segment, change stage | |
| Webhook step | ✅ | ✅ | ✅ SSRF-guarded, idempotency key, failures recorded without stalling | |
| Conversion goal and exit on goal | 🟡 | ✅ | ✅ goal event converts and ends the enrollment | |
| Re-entry rules (once, every time, after N days) | 🟡 | ✅ | ✅ one live enrollment at a time | |
| Frequency capping across journeys | ⬜ | ✅ | ✅ per channel, per contact, across journeys and broadcasts; transactional exempt | |
| Quiet hours / DND, send in contact's time zone | ⬜ | ✅ | ✅ messages are held and sent when quiet hours end, in the contact's zone | |
| Per-step stats | ✅ | ✅ | ✅ live: entered, Yes / No, per split path (Redis counters), contacts at the step now, sends and opens per step | |
| Every Mautic campaign decision, action and condition | ✅ | 🟡 | ✅ batch 7b, see [Campaign builder](#campaign-builder-batch-7b) | |
| Multi-channel message with fallback (first reachable channel) | ✅ marketing messages | ✅ | ✅ `send_message`: ordered rules, each channel → template, optionally with conditions on the contact and trigger event (Indian number → SMS, otherwise WhatsApp); the choice shows in step stats | |
| Jump to step (loops) | ✅ | ⬜ | ✅ `jump`, capped at 25 jumps per enrollment | |
| One list of every campaign, by type, with tags | 🟡 | ✅ | ✅ M3, renamed in M7: Campaigns: One-time (broadcasts), Triggered (event or form journeys), Automation (segment journeys), set automatically; free tags with suggestions; search, status, channel and tag filters; audience, sent, last activity, in progress; duplicate, archive, restore, delete | |
| Campaign types named by when they send; a "what do you want to do" chooser | 🟡 | ✅ | ✅ M7: **New campaign** asks: send a message to a segment (→ channel → the one-time wizard), react when someone does something (→ builder with an event trigger), follow up when someone joins a segment (→ builder with a segment trigger); one word per status (Live, Finished, Sending, Draft, Scheduled, Paused, Cancelled, Archived) | |
| Copy and paste steps, duplicate journeys | 🟡 (clone) | 🟡 | ✅ M3: Copy / Paste of a selection of steps between journeys and workspaces (clipboard JSON checked by the server: links outside the selection and other workspaces' references dropped, with notes); Duplicate | |
| Send deadline on send steps | 🟡 ("prevent after date") | 🟡 | ✅ M3: don't send after HH:MM and/or more than N hours late: late messages are dropped as `<channel>_failed` with reason `deadline` | |

## Broadcasts (one-time and recurring campaigns)

In the app: **one-time campaigns** (since M7 the word "broadcast" appears only in the API).

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Send to a segment on any channel | ✅ email/SMS | ✅ | ✅ Phase 2 | |
| Schedule: start required, optional end, repeat daily/weekly/monthly | 🟡 | ✅ | ✅ Phase 2 | |
| A/B test with winner selection | ✅ | ✅ | ✅ 2-5 variants, test group %, winner by open or click rate after a wait, or by hand; plain splits | |
| Conversion goal per broadcast | 🟡 | ✅ | ✅ recipients who do an event within N days | |
| Excluded segments | ✅ | ✅ | ✅ `exclude_segment_ids` (up to 10): members of any are left out of every send and the A/B winner step | |
| Send at the same local time in each contact's time zone | ⬜ | ✅ | ✅ one-time broadcasts, `local_time` + `timezone` (the zone the time was chosen in): the run starts when that clock time comes in UTC+14 and each message is held until it in the contact's `timezone` / `geo_timezone` (else the chosen zone) | Not for repeating broadcasts |
| One-time campaign wizard, content written inside the campaign | 🟡 | ✅ | ✅ M3: per channel: audience (segments in and out) → content in the template editor (kept with the campaign unless saved as reusable) → schedule → review, with the exact estimate at every step | |
| Exact audience estimate (exclusions, consent, reachability, caps) | ⬜ | 🟡 | ✅ M3: Redis `BITOP` over segment and reachability bitmaps, step by step; frequency caps exact up to 50,000 contacts, sampled above; cached 30 s, 60 a minute per workspace | |
| Send deadline | 🟡 | 🟡 | ✅ M3: time of day and/or hours late (local time for local-time broadcasts); late messages dropped and counted in the report | |
| Duplicate a broadcast | ✅ | ✅ | ✅ M3: a draft copy, with its own copy of a message written in the campaign | |
| Choose the provider per campaign | 🟡 (one transport per instance) | ✅ | ✅ after M7: a **Send through** picker on one-time campaigns, journey send steps (and each rule of Send message) and test sends; the channel's default otherwise, and again if the chosen provider is removed | |
| Report funnel and failure reasons | 🟡 | ✅ | ✅ M4: audience at send time → targeted → skipped by reason → sent → delivered → opened → clicked (downloads), bounced, complaints, failed, unsubscribed; reasons table; each number opens its contacts (M7) | |

## Channels

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Per-provider rate and daily quota | 🟡 (per transport) | ✅ | ✅ M3: messages per second and a daily quota (day in a chosen zone) per provider, shared by every dispatcher through Redis; over the quota messages wait for the next day or their deadline; usage today on Settings → Channels | |
| Pooled SMTP connections | ✅ | n/a | ✅ M3: per relay and login, bounded (max connections, messages per connection), TLS kept; ~30x the throughput of a connection per message in the benchmark | Per Pulse server |
| Saved test contacts | 🟡 | ✅ | ✅ M3: contacts or bare addresses per channel, offered by every Send a test dialog (with the sample trigger event) | |
| Email: drag-and-drop builder, HTML, personalization | ✅ | ✅ | ✅ GrapesJS builder, TipTap rich text editor (also on landing pages, focus items, web content), HTML; variables `{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`, `{{links.x}}` with defaults and formats, a picker on every templated field (M2) | |
| Personalisation from the triggering event (Liquid-style variables, defaults, formats) | 🟡 tokens, no event data | ✅ | ✅ the trigger event's properties travel with every message of the journey (personal properties removed, 16 KB); pipes `default`, `date`, `money`, `number`, `truncate`…; missing-value policy per template; preview and test with a real contact's event (M2) | |
| Preview (with a real contact's data) and send a test, every channel | ✅ | ✅ | ✅ Phase 2 | |
| Provider connection test, per provider | 🟡 | ✅ | ✅ Phase 2 | |
| Email open and click tracking | ✅ | ✅ | ✅ sealed tokens; click links signed (no open redirect); `email_opened` / `email_clicked` events | |
| Unsubscribe link and List-Unsubscribe header | ✅ | ✅ | ✅ footer or `{{links.unsubscribe}}` (`{{.UnsubscribeURL}}`), RFC 8058 one-click, confirm page | |
| Bounce and complaint handling | ✅ | ✅ | ✅ SendGrid, Postmark, Mailgun, Amazon SES (SNS), SparkPost and generic webhooks; hard bounces stop email, complaints unsubscribe; an IMAP bounce mailbox read every 5 minutes for DSN (RFC 3464) and ARF (RFC 5965) reports; M4: linked to the message and campaign by a signed message reference; webhooks verified (SNS, SendGrid signature, Mailgun HMAC, basic auth) and failing closed once a key is saved | Non-standard plain-text bounces aren't read; one mailbox per workspace; bounces from before M4 aren't linked to campaigns |
| Delivered status, complaints in reports | ✅ | ✅ | ✅ M4: `email_delivered` from SendGrid, Postmark, Mailgun, SES and SparkPost; complaints counted per campaign | Plain SMTP relays, Gmail, FCM and web push report no delivery ("—") |
| Failure reasons | 🟡 | ✅ | ✅ M4: a reason code on every failed, skipped, capped, bounced or complained message (invalid number, not on WhatsApp, possibly blocked, spam policy, consent, deadline…), in reports and on the contact page | |
| Custom provider authentication and requests | 🟡 | 🟡 | ✅ M5: the custom HTTP gateway with basic, bearer, API key, OAuth2 or a login request (token by JSON path, header or regex, with expiry; cached and refreshed on 401), and a custom request (method, URL, headers, templated body, success rule, id path, receipt mapping, verified receipts); Preview request and Test connection; after M7 the form fills itself from the API's pasted curl or Postman examples (send and login) | |
| Gmail, Google Workspace, Workspace SMTP relay | ✅ (SMTP) | ⬜ | ✅ M5: presets with Google's limits, app password or XOAUTH2, relay by IP with an EHLO domain; a three-step app-password guide and only the chosen sign-in method's fields | |
| SMTP set-up like Listmonk: presets, encryption, login type, custom headers | ✅ | n/a | ✅ after M7: "Fill in for:" Amazon SES, SendGrid, Mailgun, Postmark, Mailjet, Brevo, SparkPost, Microsoft 365, Zoho Mail, Gmail; encryption automatic / STARTTLS / SSL/TLS / none; login PLAIN / LOGIN / CRAM-MD5 / none; More settings for connections, HELO, skip certificate check, sending service, custom headers | |
| Gupshup SMS, WhatsApp and RCS | ⬜ | ✅ | ✅ M5: SMS with DLT template and entity ids, WhatsApp templates and session messages, RCS with SMS fallback; delivery reports and inbound | Built from public docs, tested against mocks; RCS formats inferred |
| Workspace settings for automatic behaviour (retention, retries, schedules, default limits) | 🟡 | 🟡 | ✅ M5: Settings → Automation & limits, each value with its default and range, plus what stays fixed by the platform | |
| AMP email (interactive) | ⬜ | 🟡 | ✅ M6: block builder (carousel, gallery, accordion, poll, form…) or code, validated with line numbers, script-free preview, send test; polls and forms post back as `amp_poll_answered` / `amp_form_submitted` (form answers may update contact fields); poll results | Postmark doesn't send AMP; Gmail shows AMP only to registered senders; the preview is an approximation |
| Plain-text part, attachments | ✅ | 🟡 | ✅ every email is multipart/alternative: the template's own text or one generated from the HTML (links as "text (url)", direct, plus the unsubscribe link); up to 10 asset attachments (10 MB together) in multipart/mixed; the custom gateway gets signed 7-day download links; preview shows the text part and the files | P2 |
| SMS (Twilio, MSG91) | ✅ | ✅ | ✅ adapters, not tried on live accounts | |
| WhatsApp (Meta Cloud) with SMS fallback | ⬜ | ✅ | ✅ | |
| RCS (Google RBM) with SMS fallback | ⬜ | ✅ | ✅ | |
| Mobile push (FCM) | ✅ | ✅ | ✅ adapter; tokens from the Flutter, Android and iOS SDKs; taps tracked by the native SDKs (`push_clicked`, "clicked" journey decision) | iOS via FCM tokens (no direct APNs); dismissals only where the OS reports them |
| Web push | ✅ | ✅ | ✅ standard Web Push with per-workspace VAPID keys (no Firebase); `Pulse.enablePush()` and `/sdk/pulse-sw.js`; FCM web tokens still work; clicks and dismissals reported by the service worker (`push_clicked` / `push_dismissed`) | Browsers report dismissals unevenly; FCM web tokens (Firebase's own worker) aren't tracked |
| Web push opt-in prompt and install check | 🟡 | ✅ | ✅ M6: Settings → Web push: a no-code bell, bar or pop-up (texts, colours, delay / scroll / page triggers, ask again after N days, pages); an install check run from the server (HTTPS, snippet, site key, service worker, VAPID keys) and a browser self-check link (`Pulse.diagnose()`) | |
| App inbox | ⬜ | ✅ | ✅ `inbox` channel for journeys and broadcasts (templates, consent, caps, reports); SDK API to list, read, delete, click; per-message expiry; `Pulse.inbox` in the web SDK | |
| In-app messages (modal, banner, full screen) | ⬜ | ✅ | ✅ campaigns with segment, platform, trigger event or screen, delay, frequency, priority, schedule; editor with phone preview, "see what a contact gets" and test send; rendered by the Flutter, Android and iOS SDKs; shown/clicked/dismissed stats | |
| Webhook channel | ✅ | ✅ | ✅ custom HTTP gateway provider for every channel (signed JSON, receipts and inbound replies back); journey webhook step | |
| Facebook / Google audience sync | 🟡 | ✅ | ✅ a segment linked to a Facebook Custom Audience or Google Ads Customer Match list (existing or created by Pulse); SHA-256 of normalised email and E.164 phone in batches; streamed entries/exits, consent and address changes; daily full reconcile; contacts unsubscribed from everything never uploaded; status, counts and errors per link | Google consent signals (EEA), list size from the platform: see [Integrations](#integrations-batch-8a) |

## On-site

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Focus items: pop-up, bar, slide-in; segment and page targeting; triggers; frequency | ✅ | ✅ | ✅ | |
| Dynamic Web Content: per-segment variants | ✅ | ✅ | ✅ M6: variants with audiences (segments in and out, page URL, devices, contact-field conditions) in priority order, the first match wins; warnings for variants that can never show; an overlap view of how many contacts would see each | Overlap counts segments only (page, device and field rules aren't counted) |
| Ordering focus items | 🟡 | ✅ | ✅ M6: the same audience rules and an order; one pop-up, one bar and one slide-in per page, the first allowed; overlap per kind | |
| Schedule on-site content (start, optional end) | 🟡 | ✅ | ✅ Phase 2 | |
| "See what a contact would see" check | 🟡 | 🟡 | ✅ Phase 2 | |
| Rendering on websites (JS SDK) | ✅ | ✅ | ✅ `/sdk/pulse.js` (3 KB gzipped): focus items with all triggers and frequency caps, content slots | |
| Forms (with field mapping, progressive profiling) | ✅ | 🟡 | ✅ hosted page (iframe-able) and web SDK embed; static segments, tags, consent opt-in; honeypot, fill time, rate limit; submissions log; file uploads (types checked against content, size limits, private downloads) and multi-page forms (page breaks, per-page checks, progress bar) | Files uploaded on a page with errors must be chosen again; no-script browsers see all pages at once |
| Landing pages | ✅ | ⬜ | ✅ `/p/{workspace}/{page}`, drag-and-drop, rich text or HTML, title and meta description, embedded forms (`{form:KEY}`), draft/published with a schedule; nonce CSP (only the Pulse SDK runs, scripts and handlers stripped), frameable; `landing_page_viewed` via the web SDK; views, unique visitors, form submissions; script-free preview | P2 |
| Assets (gated downloads) | ✅ | ⬜ | ✅ uploads up to 25 MB (type allowlist checked against file signatures) kept in Postgres; `/a/{key}` open or gated by a form (15-minute sealed link, `asset_downloaded` for the submitter); always `Content-Disposition: attachment`, nosniff, sandbox CSP; download counts; email attachments | P2 |

## Analytics

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Home overview: what needs attention, audience, revenue, campaigns | 🟡 | ✅ | ✅ after M7: the Overview's Needs attention (quota, failing channels, bounce spikes, failing campaigns), Audience (added, unsubscribed, week on week), Revenue and One-time campaigns (sending, coming up, recent), from a summary cached a minute | |
| Live dashboard, message outcomes | 🟡 | ✅ | ✅ M4: Analytics → Live events (the workspace's latest events, polled every 2 s) | |
| DAU / WAU / MAU, new vs returning | ⬜ | ✅ | ✅ M4: Analytics → Users, by platform and UTM source, pre-aggregated in ClickHouse (`active_users_daily`, `first_active`) | Deleted contacts still count in past days |
| Campaign and product dashboards | 🟡 | ✅ | ✅ M4: Analytics → Campaign performance (funnel and revenue per campaign) and Products (views, carts, purchases, abandoned, segment of buyers) | |
| Journey and broadcast reports (sent, delivered, opened, clicked, converted) | ✅ | ✅ | ✅ per run, A/B variant, journey step and channel; top links; daily; from a ClickHouse `message_events` view | |
| Funnels | ⬜ | ✅ | ✅ 2-8 steps with property filters, conversion window, date range, segment filter; live while editing | |
| Funnel breakdown by attribute | ⬜ | ✅ | ✅ any profile attribute (geo_country, plan, stage...): step counts for the top 10 values plus everyone else | P2 |
| Cohorts / retention | ⬜ | ✅ | ✅ cohorts by first seen or first time doing an event (with filters), by day, week or month; coming back = any activity or an event; segment filter; heat-map triangle with an all-cohorts row; saved reports | P2 |
| Revenue attribution, UTM | 🟡 | ✅ | ✅ web SDK keeps first/last-touch UTM (`first_utm_*`, `last_utm_*`, referrer domain; first kept once set) on identify and forms, the session's campaign on page views; revenue event and amount property per workspace; last-touch, first-touch and linear attribution to broadcasts, journeys and UTM campaigns over an N-day window; recipients' revenue in broadcast and journey reports | P2 |
| Custom report builder | ✅ | 🟡 | ✅ events, message outcomes or contacts; count, unique contacts, sum or average; group by event, property, attribute, outcome, channel, campaign or day/week/month; property/attribute, channel and segment filters; table, bars or line; saved reports; CSV export (formula-safe) of the result shown; at most 100 groups, a year of data | P2 |

## Platform

| Feature | Mautic | WebEngage | Pulse | |
|---|---|---|---|---|
| Multi-tenant workspaces | ⬜ | ✅ | ✅ | |
| Password sign-in, SSO (OIDC: Keycloak, Entra ID, Okta, Google) | 🟡 SAML | ✅ | ✅ tested against Keycloak; settings can be removed (`DELETE /v1/settings/sso`) | |
| Two-step sign-in (authenticator app) | ⬜ (plugins) | ✅ | ✅ after M7: TOTP with a QR code and 10 recovery codes on Your account; workspace policy (optional, everyone, some roles or people); admins reset a teammate's | Password sign-ins only (SSO leaves it to the identity provider) |
| Roles | ✅ granular | ✅ | ✅ admin, editor, viewer and custom roles: none/view/edit/publish per area, enforced on every route | |
| Audit log | 🟡 | ✅ | ✅ every change through the app or API (secrets redacted) and every sign-in; retention setting; CSV / JSON export with person, type and date filters | |
| REST API with keys | ✅ | ✅ | ✅ | |
| Outbound webhooks on events | ✅ | ✅ | ✅ any event or segment entry/exit; HMAC-signed; retries over ~7 hours; delivery log; test send (listed in the log, marked as a test) | |
| Integrations (CRM, Zapier, Shopify) | ✅ plugins | ✅ | ✅ HubSpot and Salesforce (scheduled pull with field mapping, push on demand and from journeys), Shopify webhooks (orders, customers, checkouts), Zapier / Make REST hooks (triggers and actions with the connector's own key); masked secrets, test connection, previews, sync log | See [Integrations](#integrations-batch-8a) |
| Web, Android, iOS, Flutter SDKs | 🟡 JS | ✅ | ✅ web SDK (track, identify, page views, on-site, forms, web push); Flutter SDK (`sdks/flutter/pulse_sdk`); native Kotlin (`sdks/android`) and Swift (`sdks/ios`) SDKs | Native cores unit-tested in Docker; the Android module compiled and linted, never run on a device; the iOS UIKit code not yet compiled (no Xcode here) |

## Order to close the gaps

The build order as it happened; every step below is done.

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
   - Cleanup like Mautic's maintenance (hourly at first; since M5 once a day per workspace): anonymous visitors idle for N days with their events, events older than N
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
   - **Batch 7, analytics (done):** cohorts and retention (Analytics → Cohorts), revenue attribution and UTM capture
     (Analytics → Revenue; revenue in broadcast and journey reports), funnel breakdown by attribute, the custom report
     builder (Analytics → Reports), and automatic A/B winners on journey split steps.
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
       every Mautic decision, action and condition is there (push to integration came with batch 8a).
   - **Batch 8, integrations and reach:**
     - ✅ **Batch 8a (done 2026-11-11):** CRM / Shopify / Zapier-style connectors, Facebook and Google audience sync, the
       "Push contact to integration" journey step. See [Integrations](#integrations-batch-8a).
     - ✅ **Batch 8b (done 2026-11-11):** native Kotlin and Swift SDKs, RCS replies, SMTP bounce mailbox, web push
       click tracking, form file uploads and multi-page forms, audit log export. See [Batch 8b](#batch-8b).

8. **After batch 8 (requested 2026-11-11), in this order:**
   - ✅ **Batch 9, contact views and one clear navigation (done 2026-11-11):** Contacts search filters as chips with
     suggestions, compiled to segment rules ([Contact views](#contact-views-batch-9)); "View contacts" on every segment;
     save a view as a segment; columns and CSV export of a view. One Segments page (Dynamic and Static), one "On-site &
     in-app" area, one Import flow under Contacts, and a navigation review ([Navigation](#navigation-batch-9)).
   - ✅ **Docs checked against the code** after batch 9.
   - ✅ **Step-by-step tutorials** ([docs/tutorials](tutorials/README.md); all 11 written, with screenshots retaken by
     `make docs-shots`) and the eight [developer guides](developer/README.md).

9. **The marketing team's plan** ([docs/06-marketing-team-plan.md](06-marketing-team-plan.md)), all done:
   - ✅ **M1, events and segments:** event schemas, property paths, date ranges, NONE groups, list and choice fields,
     daily-refresh segments, "why is this contact in this segment".
   - ✅ **M2, personalisation:** standard variables, the trigger event's properties in messages, channel by rule.
   - ✅ **M3, campaigns:** the Campaigns hub, the one-time wizard, exact estimates, provider rates and quotas, send
     deadlines, duplicates and copy / paste, saved test contacts.
   - ✅ **M4, delivery truth and dashboards:** linked bounces and complaints, delivered, reason codes, report funnels,
     the Users / Campaign performance / Products / Live events dashboards, the contact page.
   - ✅ **M5, channels and settings:** custom provider authentication and requests, Gmail / Workspace / relay and
     Gupshup providers, Settings → Automation & limits with daily per-workspace retention.
   - ✅ **M6, AMP and on-site:** AMP emails, content variants with rules, order and overlap, focus item order, the web
     push prompt and install check.
   - ✅ **M7, clarity:** every number opens its contacts, one Campaigns entry with three types named by when they send,
     "Website & app messages", plain wording.
   - ✅ **Docs:** README, architecture, this document, the developer guides and 18 tutorials updated in one final
     pass, with every screenshot retaken.
   - ✅ **After M7 (batch N, 2026-11-14):** the Contacts **Add filter** dialog and the workspace's own events in the
     segment picker, simpler provider set-up (SMTP presets, Gmail guide, pasted API examples for the custom
     gateway), the provider chosen per send, the new Overview, and two-step sign-in.

## Contact views (batch 9)

**What it is.** Contacts has one search box. Words of the form `key:value` become removable chips; the rest is free
text. Suggestions appear as you type (`is:` lists its options, `segment:` the segments, `tag:` the tags in use, `stage:`,
`company:` and `owner:` theirs; teammates are suggested only to users who can view settings); Enter or a space after a finished filter makes the chip, Backspace in an empty box
removes the last one. The view lives in the address (`/contacts?q=…`), so it can be bookmarked; typing waits 300 ms
before asking the server. Above the table: the count, **Save as segment**, **Columns** (built-in fields and every
defined contact field, remembered per browser) and **Export CSV** (the view with the columns shown; contacts edit
access, like every export). Every segment's row and page has **View contacts**, which opens `in:<id>` since M7 (it was `segment:<id>`), so the list has exactly the segment's size.

| Filter | Means | Rule it compiles to |
|---|---|---|
| `is:anonymous` / `is:identified` | no known ID or email yet / identified | `date_identified` not set and no email or phone / any of them (`date_identified` is stamped at the first event with a non-`anon-` ID or an email; profiles older than batch 7b lack it, so email or phone count too) |
| `is:unsubscribed` | unsubscribed from any channel or everything | any `optout_*` = true |
| `is:bounced` | email or SMS bounced | `email_bounced` or `sms_bounced` = true |
| `is:dnc` | Do Not Contact: unsubscribed or bounced | either of the above |
| `segment:<name or id>`, `list:` | in a dynamic or static segment (`list:` is the old name) | `{"segment": id, "op": "in"}` |
| `tag:<tag>` | has the tag | `{"tag": …, "op": "has"}` |
| `stage:<stage>`, `company:<name or id>` | in the stage, at the company | `stage` eq / `company_name` eq (an id: `company_id` eq) |
| `owner:me`, `owner:none`, `owner:<name, email or id>` | owned by (a teammate's name, email or id needs view access to settings, like the team list; without it other values answer 400) | `owner_id` eq / not set |
| `points:>N`, `>=`, `<`, `<=`, `=N` | points compared | `points` gt … ; when 0 passes (`<10`), contacts without points are included |
| `email:<text>`, `name:<text>` | contains (`name` checks name, first_name and last_name) | `contains`, tried as typed, in lower case and capitalised |
| free text | quick find | not part of the rule: case-insensitive match on external ID, email, name, first/last name and phone |

Filters are ANDed; values with spaces are quoted (`segment:"VIP buyers"`); unknown keys are free text (IDs may contain
colons); at most 20 filters. "Save as segment" creates a dynamic segment with exactly the view's rule; it is disabled
while the view has free text (use `email:` or `name:`).

**Approximations.** A profile from before batch 7b with a known ID but no email or phone (and no event since) has no `date_identified` and shows under `is:anonymous`. Rule comparisons are case-sensitive, so `email:` and `name:` try three spellings rather than
ignoring case ("aSHA" finds only "aSHA", "asha", "Asha"). Filtered views read ClickHouse's contacts mirror, which trails
Postgres by the sink's flush (about a second). The count is exact up to 10,000; above that it is `uniq` over the rows
without `FINAL` ("About 1,234,567"), so a contact whose older version matched can count.

**How it stays fast at 10–100M contacts.** `GET /v1/contacts` without filters or text pages through Postgres by its
primary key (`id < after ORDER BY id DESC`, no scan). With them, ClickHouse runs `segment.WhereSQL` (the same compiler
segments use) plus the free text over `contacts FINAL`, keyset-paged by `id`; only the 50 ids of the page are then
loaded from Postgres by primary key. The count stops at 10,001 rows before falling back to the approximate count, and
runs only for the first page.

## Navigation (batch 9)

The sidebar follows the usual marketing-tool layout: who you reach, what you send and show, what it's built from, how it
did, then one Settings entry with its own menu. At narrow widths the sidebar folds behind a Menu button. Old addresses
redirect (`/lists`, `/lists/:id`, `/imports`, `/focus-items(/:id)`, `/web-content(/:id)`, `/in-app(/:id)`, `/channels`,
`/test-inbox`); the APIs are unchanged (`/v1/lists` stays for static segments).

| Old | New | Why |
|---|---|---|
| Audience → Segments and Audience → Lists | Audience → **Segments**, with a Dynamic / Static type filter and badge; "New segment" asks Dynamic or Static | One concept with two types, as in Mautic (filters vs manual) and WebEngage (live vs static); "list" also meant subscription lists |
| "Lists" wording (journey "Modify contact's segments", forms, import, merge, audit log, reports) | "static segment" | One name everywhere |
| Engage → Focus items, Web content, In-app messages | Campaigns → **On-site & in-app** (named **Website & app messages** since M7): one list with a channel badge (Website / Mobile app) and type filter; "New" asks Website (pop-up, bar or slide-in; dynamic web content) or Mobile app (in-app message) | Same job (messages shown inside your site or app), chosen by where they show, like WebEngage's on-site / in-app |
| Audience → Imports, and Contacts → Import CSV (quick import) | Contacts → **Import** (`/contacts/import`): upload, map columns, static segment, review, run in the background; one import history | Two entry points with different limits and histories |
| Engage (campaigns and content mixed) | **Campaigns** (Journeys, Broadcasts, On-site & in-app; since M7 one **Campaigns** entry and **Website & app messages**) and **Content** (Message templates, Landing pages, Forms, Assets) | Separates what runs from what it's built from (HubSpot "Marketing" vs "Content") |
| Templates | **Message templates** | They hold email / SMS / WhatsApp / RCS / push / app inbox content; on-site content and landing pages have their own editors |
| Audience → Forms | Content → Forms | Forms are content you publish (as in Mautic's Components) |
| Analyze → Funnels, Cohorts, Revenue, Reports | **Analytics** → Reports, Funnels, Cohorts, Revenue | Standard name; the custom report builder first |
| Delivery → Channels, Test inbox; Workspace → ten items | **Settings** (one sidebar entry, `/settings`) with a menu: Workspace (Team, Roles, Sign-in & keys, Audit log), Contact data (Contact fields, Scoring, Stages, Data & privacy), Delivery (Channels, Test inbox), Connections (Integrations, Webhooks); since M5 also Automation & limits (Workspace), since M6 Web push (Delivery) | Setup pages out of the daily work menu; Test inbox belongs with the sandbox providers it shows |
| Attributes | **Contact fields** (Settings) | Mautic "Custom fields", HubSpot "Properties": the word marketers use; the API keeps "attributes" |

## Campaign builder (batch 7b)

**What it is.** Campaigns → a triggered or automation campaign (a journey). Every outgoing path (an edge, or a free Yes / No / split handle) has a "+". It opens
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
| Replies to email | Replies to email | `message_decision` outcome `replied` | needs reply tracking: Settings → Channels → Delivery rules → "Reply tracking address" and the provider's inbound webhook (see architecture, "Inbound replies") |
| Downloads asset | Downloads asset | `on_asset`, optional `asset_id` | gated downloads by known contacts (`asset_downloaded`) |
| Submits form | Submits form | `on_form`, optional `form_id` | the submission is kept on the enrollment for "Form field value" |
| Visits a page | Visits a page | `on_page`: `url` contains, or a landing page `page_id` | web SDK `page_view` / `landing_page_viewed` |
| Device visit | Device visit | `on_device`: `device_type` and/or `os` | a page view from that device (SDK device properties) |
| Request dynamic content | Requests dynamic content | `on_dwc` `slot` | the decide API records `dwc_requested` for known contacts; put "Push dynamic content" on Yes |
| **Actions** | | | |
| Add Do Not Contact | Add Do Not Contact | `dnc_add` channel (or all), reason | `unsubscribed` with method manual, source journey |
| Remove Do Not Contact | Remove Do Not Contact | `dnc_remove` | `resubscribed`; needs contacts edit access |
| Add to company | Add to company | `add_company` | links the contact (`company_linked`); needs contacts edit access |
| Update contact's primary company | Update contact's primary company | `add_company` | approximate: a Pulse contact has one company, so "primary" is the company |
| Add to company's score | Add to company's score | `company_points` | adds to the company's `score` attribute (once per step, even on retries); contacts see it as `company_score`; needs contacts edit access |
| Update company (Mautic's company fields) | Update company | `update_company` `company_attrs` | the contact's linked company; skipped without one; needs contacts edit access |
| Adjust contact points | Adjust contact points | `adjust_points` | |
| Change campaigns | Change campaigns | `change_journey` add / remove journeys | through a `journey_change` event; only active journeys enroll (their schedule and re-entry apply); removed enrollments get status `removed` |
| Change contact's stage | Change contact's stage | `change_stage` | |
| Delete contact | Delete contact | `delete_contact` `confirm: "DELETE"` | permanent, through the cleanup deletion path; typed confirmation; saving or activating it needs edit access to contacts |
| Jump to Event | Jump to step | `jump` `target` | ends the path; at most 25 jumps per enrollment, then the contact leaves (`journey_jump_limit`) |
| Modify contact's segments | Modify contact's segments | `update_list` `add_lists` / `remove_lists` | static lists only: dynamic segments follow their rules (the inspector says so) |
| Modify contact's tags | Modify contact's tags | `update_tags` | |
| Push contact to integration | Push contact to integration | `push_integration` | HubSpot or Salesforce, through the integration's field mapping; a failed push records `integration_push_failed` and the journey carries on |
| Push dynamic content | Push dynamic content | `push_dwc` slot, HTML, days | per contact, returned by the decide API for that slot (wins over the slot's variants) |
| Send a webhook | Send a webhook | `webhook` | |
| Send email | Send email | `send_email` | also SMS, WhatsApp, RCS, push, app inbox steps |
| Send email to user | Send email to user | `notify_user` | users, the contact's owner and/or fixed addresses; the template rendered with the contact's data; not consent-gated, not tracked |
| Send marketing message | Send marketing message | `send_message` channels in order | approximate: Mautic's "marketing message" is a saved object; Pulse keeps the ordered channel → template list on the step and sends on the first channel the contact can receive and has consented to (address, consent, bounce, push device). Rules may also carry conditions on `contact.*` / `event.*` (M2: Indian number → SMS, otherwise WhatsApp). |
| Show Focus Item | Show focus item | `show_focus` | shown to this contact on their next page view, once (even a draft item: journey-only) |
| Update contact | Update contact | `update_attr` | values may use `{contactfield=x}`, `{{contact.x}}`, the trigger event's `{{event.x}}`, `{{.Attrs.x}}`, `now`, `now+3hours`, `now-2days` … |
| Update contact owner | Update contact owner | `update_owner` | `owner_changed`; 0 unassigns; needs contacts edit access |
| **Conditions** | | | Yes / No right now |
| Contact campaigns | Contact campaigns | `if_journey` active / completed / entered | |
| Contact device | Contact device | `check` preset device | last-seen device attributes |
| Contact field value | Contact field value | `check` preset field | the segment builder's filters |
| Contact owner | Contact owner | `check` preset owner | |
| Contact points | Contact points | `check` preset points | |
| Contact segments | Contact segments | `condition` | dynamic and static segments |
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

**Not covered / approximate:** push to integration goes to CRMs only (HubSpot, Salesforce), not to Mautic's other plugins; Mautic's saved "marketing message" object (the list lives
on the step); a message decision can't watch a "Send marketing message" step (its channel is chosen per contact); device
visit uses web page views (not app screens); per-step counts are best effort (a retried step can count twice).

## Segment builder (batch 7b)

**What it is.** Audience → Segments → New segment → Dynamic. The rule is a set of nested boxes: the top one reads "Contacts who match
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
| Clicked any link from any channel | Clicked any link from any channel (Behaviour: messages) | ANY group of `email_clicked`, `sms_clicked`, `rcs_clicked`, `push_clicked`, `inbox_clicked`, `inapp_clicked` ≥ 1 | exact for "at least once" |
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

## Integrations (batch 8a)

**What it is.** Settings → Integrations: a connector registry per workspace. Each connector has a type, settings (shown),
credentials (sealed with the server key; the API and app show them masked, e.g. `••••••••a1b2`, and a field left empty or
sent back masked keeps the stored value), a status (new / ok / error with the last error), its last sync and a week of
sync log. Every type has Test connection (except Zapier / Make, which never calls out) and a preview. Every remote base
URL is a setting with the real API as default, limited to the vendor's hosts outside local development; changing it
means entering the credentials again. Creating, changing and deleting connectors needs settings edit access;
viewing them needs settings view (journey editors pick a CRM for the push step).

| Connector | What Pulse does | Approximate / missing |
|---|---|---|
| HubSpot | Pulls contacts changed since the last pull (CRM search API, oldest first, 1,000 per pull, the rest right after) every 15 minutes to 1 day; field mapping (remote field ↔ attribute: both ways, into Pulse only, out to HubSpot only; "Load fields" lists HubSpot's properties); `crm_synced` events with the mapped attributes and `hubspot_id`; the contact with the same email, else a new one named by the email (`hubspot-<id>` without one). Push: update by `hubspot_id`, else by email, else create; empty Pulse values never blank a HubSpot field | Companies, deals and deletions aren't synced; a pull doesn't move consent (map `hs_email_optout` to a plain attribute if needed); private app tokens only (no OAuth app) |
| Salesforce | The same for Contact or Lead (SOQL on `LastModifiedDate`), with OAuth client credentials against your My Domain URL (token cached 30 minutes); push finds by `salesforce_id` or email, and creates with `[not provided]` for a missing last name (and company, for leads) | No refresh-token / web-server OAuth flow; field names are checked to be plain API names (they go into SOQL) |
| Shopify | `POST /v1/webhooks/shopify/{id}` (paste it in Shopify admin → Settings → Notifications → Webhooks, JSON): checks `X-Shopify-Hmac-Sha256` with the signing secret and the shop domain; `orders/create` → `order_completed` {order_id, order_number, amount, revenue, currency, discount, items, item_count}, `customers/create` → `customer_created`, `checkouts/create|update` → `cart_updated` {checkout_token, amount, items, url}; the customer's name, phone, `shopify_customer_id`, order count and total spent go on the contact (found by email, else phone). Event ids come from `X-Shopify-Webhook-Id`, so Shopify's retries record once. The page offers to make `order_completed` / `amount` the workspace's revenue event | Webhooks are registered by hand (no OAuth app install); completed checkouts are skipped (the order arrives separately); Shopify's marketing consent isn't mapped to Pulse consent; refunds and fulfilment topics aren't used |
| Zapier / Make (REST hooks) | The connector's own API key (`pzk_…`, shown once, rotatable; the workspace key isn't needed). Triggers: `POST /v1/hooks/subscriptions {target_url or hookUrl, event, segment_id?, form_id?}` / `DELETE /v1/hooks/subscriptions/{id}`, for `contact_created` (a contact becomes known: `date_identified` is set), `segment_entered` / `segment_exited`, `form_submitted` or any event; deliveries reuse outbound webhooks (signed, retried for 7 hours, delivery log on the connector page), and a 410 answer removes the subscription. `GET /v1/hooks/me`, `/segments`, `/samples?event=` help a Zap's setup. Actions: `POST /v1/hooks/contacts`, `/events`, `/tags`; reserved attributes are ignored and listed back | No published Zapier or Make app yet (use their webhook / API modules with these endpoints); `*` subscriptions don't include `contact_created` |
| Facebook Custom Audiences | Link a segment to an audience id, or have Pulse create one (`customer_file_source: USER_PROVIDED_ONLY`). Uploads `EMAIL_SHA256` / `PHONE_SHA256` (digits with country code, no +) in batches of 5,000 | Facebook reports match rates only in Ads Manager; unlinking stops syncing but leaves the audience as it is |
| Google Ads Customer Match | Link a segment to a user list id (or create a CRM-based list). Each batch is an offline user data job (create, add operations, run) with `hashedEmail` (gmail dots removed) and `hashedPhoneNumber` (E.164); OAuth refresh token, developer token, optional manager account | Google matches in the background (hours); EEA consent signals (`adUserData`, `adPersonalization`) aren't sent; failed rows inside a job (partial failure) aren't read back |

**Audience sync.** The `integrations` role keeps a queue per link (`audience_queue`): segment entries and exits come from
`segment.membership`, and changes of `email`, `phone`, `optout_all` or `merged_into` from `events.enriched`. Every two
seconds it takes up to 5,000 queued contacts per link and decides each from the current state: a segment member with an
email or phone who isn't unsubscribed from everything belongs in the audience. `audience_members` keeps the hashes
sent, so a removal (or a changed address) removes exactly what was added. A full reconcile runs on linking, on "Sync
now", on resume and every `PULSE_AUDIENCE_RECONCILE_INTERVAL` (24 h): it walks the segment bitmap and everyone uploaded,
which also removes deleted contacts. A failing link backs off (up to 10 minutes) and shows the error; the counts are
in the audience now, added, removed, left out (no consent or nothing to match) and waiting.

## Batch 8b

| Item | What Pulse does | Approximate / missing |
|---|---|---|
| Android SDK (`sdks/android`) | `pulse-core` (plain Kotlin/JVM: queue, batching, backoff, sessions, validation, inbox, in-app logic, push click reports, opt-out, storage namespaced by site key) and `pulse-android` (SharedPreferences, lifecycle, device props, notification click / dismiss helpers, FCM token by reflection, in-app Activity) | Core: 44 unit tests in `gradle:8-jdk17`. `pulse-android` compiled and linted in an Android SDK image only: never run on a device, no tests of its own. In-app layouts are basic. Dismissals of notifications FCM shows itself aren't reported |
| iOS SDK (`sdks/ios`) | Swift package: `PulseCore` (Foundation only, same features) and `Pulse` (UIKit / UserNotifications behind `#if canImport`: lifecycle, device props, `PulseNotificationDelegate`, in-app presenter) | Core: 33 unit tests in `swift:5.10` on Linux. The UIKit / UserNotifications code has never been compiled (no Xcode here): expect small fixes on first build. Dismissals need a category with `.customDismissAction`. Push goes through FCM tokens |
| RCS replies | Google RBM webhook: URL check, HMAC-SHA512 signature with the agent's client token, delivered / read receipts, typed replies and suggestion taps (`suggestion`, `postback` props) as `rcs_received`, STOP / START keywords, `rcs_replied`; "replied" in RCS message decisions; the Channels sandbox simulates typed replies and taps | RBM doesn't say which message a reply answers: it counts for the latest RCS message the contact got in the last 7 days. Replies aren't in broadcast / journey report counts. Files and locations sent by users are ignored |
| SMTP bounce mailbox | IMAP (TLS or STARTTLS; plain only in development) through the SSRF guard; password sealed and masked, needed again when server or user change; test connection, check now, last check / error / counts; DSN and ARF parsing (nested reports, base64 parts); stable event ids; read messages flagged `\Seen` | One mailbox per workspace; every unread message in it is marked read (use a dedicated mailbox); non-standard plain-text bounces and VERP addresses aren't read; OAuth (Gmail / Microsoft 365 XOAUTH2) sign-in isn't supported |
| Push click tracking | `data.pulse_track` on every push; `POST /t/p/{token}?a=clicked` (or `dismissed`); the web service worker and the native SDKs report; `push_clicked` counts in reports; "clicked" push decisions | No link filter on push clicks; the Flutter SDK doesn't report taps yet |
| Form uploads | `file` fields: accepted types (PDF, images, Office, text/CSV), 1-10 MB, at most 5 per form; content signature plus content sniffing; private assets within the workspace's 2 GB; download link in the submissions list for users with contacts access; deleted with the form or the contact | No virus scanning; files aren't shown on the contact's profile, only in the form's submissions |
| Multi-page forms | `page_break` fields with page titles (up to 10 pages), Next / Back with per-page checks and a progress bar on the hosted page, gated asset pages and web SDK embeds; preview in the builder and the landing page preview | Without scripts the hosted page shows every page at once; server checks happen on submit (the page with the first error opens) |
| Audit log export | CSV (formula-safe) or JSON, filters for person, object type and date range, streamed in pages of 1,000; buttons on the Audit log page | |


## Contacts behind every number (M7)

Every count of people in the app is a link to the Contacts list filtered to exactly those people, so a marketer can
see who they are, export them or turn them into a segment. The list's filters show as chips in words.

| Number | Opens | Filter |
|---|---|---|
| Segment size, "View contacts" | the segment's members | `in:<id>` |
| Estimate steps (audience, after exclusions, after consent, reachable, under the cap) | the contacts left at that step | `in:`, `not_in:`, `subscribed:`, `reachable:`, `under_cap:` |
| Reach by channel on a segment | reachable members | `in:` + `reachable:<channel>` |
| One-time campaign funnel, reasons, per-run and per-variant numbers | the contacts with that outcome | `campaign:broadcast/<id>` + `run:` / `outcome:` / `reason:` |
| Triggered / automation campaign: per-step messages, "here now", in progress, reasons | the contacts at that step or with that outcome | `journey:` + `at:` / `journey_status:`, `campaign:journey/<id>` + `step:` / `outcome:` |
| Campaign performance (each campaign's numbers, cross-campaign reasons) | as above; outcomes across every campaign need reports view | `outcome:` + `period:` |
| Products dashboard (viewers, carters, buyers, abandoned) | those contacts | `event:` / `no_event:` + `product:` / `category:` + `period:` |
| Users dashboard (active, new, returning, by platform or source) | the active contacts | `active:` + `utm:` + `period:` |
| Events page contacts, form submitters, AMP poll answers, company and stage counts, on-site overlap | those contacts | `event:`, `form:`, `amp_poll:` + `answer:`, `company:`, `stage:`, `in:` / `not_in:` |

**Equal counts.** On seeded data every linked number equals its list (checked by `test/e2e/m7_test.go` and in the
browser). Where they can differ, the list says why: reports count contacts merged or deleted since; a journey's numbers
count entries, the list people. **Not linked**, because no list of people exists: the canvas's entered, Yes / No and
split path counters, a one-time campaign's audience at send time (a snapshot), an unsaved segment rule's preview, funnel
and cohort reports, asset downloaders, landing page visitors, in-app "seen by", import rows, duplicate groups, and
messages-per-channel totals; Campaign performance totals add up each campaign's contacts (a person in two campaigns
counts twice), so they stay plain with a tooltip. A number the role can't open stays plain with a tooltip naming the
missing access. **Create segment from this list** saves such a list as a static segment (up to 500,000 contacts); lists
from rule-only filters keep "Save as segment" (dynamic).

## Known gaps

Everything above is built. What is still missing or only approximate, in one place:

- **Not checked against live accounts:** the provider adapters (Twilio, MSG91, Gupshup SMS / WhatsApp / RCS, Meta
  WhatsApp, Google RBM, FCM, the Gmail XOAUTH2 flow) and the connectors (HubSpot, Salesforce, Shopify, Facebook, Google
  Ads) are tested against `httptest` mocks only. Gupshup's endpoints must be confirmed with a real account, and its RCS
  formats (send body, delivery reports, inbound) are inferred from Google RBM.
- **Delivery reports:** plain SMTP relays, Gmail / Workspace, FCM and web push report no delivery; bounces from before M4
  can't be linked to campaigns; a provider without a saved webhook key is still accepted on its secret URL alone.
- **AMP:** the app's preview is an approximation (real behaviour shows in Gmail or the AMP playground); Gmail shows AMP
  only to senders Google has registered; Postmark sends no AMP part; Pulse variables don't work inside amp-mustache
  templates; no AMP in internal notifications.
- **On-site overlap** counts segment audiences only (page, device and contact-field rules aren't counted).
- **Dashboards:** DAU history keeps counting deleted contacts; new vs returning isn't split by platform or source;
  Campaign performance totals aren't one list of people.
- **SDKs:** the Android module was compiled and linted but never run on a device; the iOS UIKit / UserNotifications code
  has never been compiled (no Xcode here); neither native SDK was tested against a running server. The Flutter SDK doesn't
  report push taps. iOS push goes through FCM tokens (no direct APNs).
- **Triggered and automation campaigns (journeys):** contacts don't leave a journey when they leave its trigger segment (use a condition, a goal or "Change
  campaigns"); a message decision can't watch a "Send marketing message" step; per-step counts are best effort.
- **Segments:** no "ever" window (an absolute range from 2000-01-01 comes close); ranges starting more than 365 days back
  are evaluated by the sweep only (every `PULSE_SWEEP_INTERVAL`), not in real time, and journey `check` conditions refuse
  them; property paths go through one list at most (`items[].code`, not `items[].tags[]`); no "currently in a journey"
  rule, point groups or regexp operator; a backfill racing live events for the same segment can be overwritten until
  the next sweep.
- **Event schemas:** counts are best effort (a retried batch can count twice); enforce refuses the whole
  `POST /v1/events` request when one event doesn't fit; Zapier / Make hook events and integration events are never
  refused; the property list comes from the first 500 events ClickHouse reads, not a random sample.
- **Tracking:** WhatsApp button links aren't click-tracked; RCS replies are matched to the latest RCS message of the last
  7 days.
- **Integrations:** no published Zapier / Make app; no OAuth app installs (HubSpot private app tokens, Salesforce client
  credentials, Shopify webhooks added by hand); HubSpot companies, deals and deletions aren't synced; Shopify consent,
  refunds and fulfilments aren't used; Google Ads EEA consent signals aren't sent and partial failures aren't read back.
- **Bounce mailbox:** one per workspace, password sign-in only (no XOAUTH2), non-standard plain-text bounces and VERP
  addresses aren't read.
- **Forms:** uploaded files aren't virus-scanned and show only in the form's submissions, not on the contact.
- **Platform:** Postgres Row-Level Security isn't enabled (tenant isolation is enforced in every query and tested at the
  API level).
- **Scale:** Redis is single-threaded and the per-poll barrier waits for the slowest partition; bulk imports still go
  through the real-time pipeline; analytics reports and filtered contact views weren't load-tested and will want rollups
  at billions of events ([load test](03-load-test.md), [architecture](02-architecture.md#at-scale-needs-pre-aggregation)).
- **One-time campaigns:** sending at each contact's local time works for campaigns sent once, not repeating ones.
- **Settings:** the maintenance pass interval is global (`PULSE_MAINTENANCE_INTERVAL`), not per workspace.
