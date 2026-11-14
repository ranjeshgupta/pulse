# Pulse

Real-time, multi-tenant marketing automation in Go on Kafka, ClickHouse, Postgres and Redis.
It aims for Mautic/WebEngage features at Listmonk speed: events move contacts into segments
and campaigns within seconds, not on a 15-minute cron.

## Documentation

- **[Tutorials](docs/tutorials/README.md)**: eighteen step-by-step guides for marketers, with screenshots
  (start with [Getting started](docs/tutorials/01-getting-started.md)). Campaigns:
  [triggered and automation campaigns](docs/tutorials/06-triggered-and-automation-campaigns.md),
  [one-time campaigns](docs/tutorials/07-one-time-campaigns.md),
  [the Campaigns hub](docs/tutorials/14-campaigns-hub-and-one-time-campaigns.md); on your site and app:
  [website & app messages](docs/tutorials/08-website-and-app-messages.md); and since M4:
  [delivery and dashboards](docs/tutorials/15-delivery-and-dashboards.md),
  [connecting any provider](docs/tutorials/16-connecting-any-provider.md),
  [interactive AMP emails](docs/tutorials/17-interactive-amp-emails.md),
  [the contacts behind a number](docs/tutorials/18-contacts-behind-a-number.md).
- **[Developer docs](docs/developer/README.md)**: eight guides: [installation](docs/developer/01-installation.md)
  (local and production), [configuration](docs/developer/02-configuration.md), [API](docs/developer/03-api.md),
  web and mobile SDKs, webhooks and integrations, architecture, contributing and testing.
- Screenshots in the docs are taken from the running app by scripts: `make docs-shots`
  ([how](docs/screenshots/README.md)).

Design notes:

- [docs/01-review.md](docs/01-review.md): what Mautic and Listmonk do, and why Mautic is slow
- [docs/02-architecture.md](docs/02-architecture.md): pipeline, topics, segment and journey design
- [docs/03-load-test.md](docs/03-load-test.md): measured throughput and latency, bottlenecks fixed
- [docs/04-phase2-plan.md](docs/04-phase2-plan.md): web app, sign-in, channels, on-site
- [docs/05-feature-parity.md](docs/05-feature-parity.md): Mautic + WebEngage feature check, and what's left
- [docs/06-marketing-team-plan.md](docs/06-marketing-team-plan.md): the marketing team's requirements, milestones M1–M7
  and how each was built

## What's built

- **Core (Phase 1):** event ingestion, identity, real-time segments (streaming, backfill, sweep), journeys with durable
  waits, and the ClickHouse event log.
- **Phase 2:**
  - Web app (React + TypeScript, embedded in the Go binary).
  - Sign-in with passwords and workspace SSO (OIDC; tested against Keycloak), with optional or required two-step sign-in through an authenticator app (Google Authenticator, Microsoft Authenticator, Authy…) for password sign-ins.
  - Channels: email, SMS, WhatsApp and RCS with SMS fallback, and FCM push. Each has a preview with a real contact,
    test sends, and provider connection tests.
  - Scheduled journeys, broadcasts and on-site content: start required, end optional (empty means it never ends),
    broadcasts repeating daily, weekly or monthly.
  - Focus items and Dynamic Web Content (Website & app messages → Website), through a public decision API.
- **P0 (done):** per-channel consent honored by every send; email open and click tracking; unsubscribe links and RFC 8058
  one-click; CSV import and export; the web SDK (`/sdk/pulse.js`: track, identify, page views, focus items, content slots,
  push tokens). Install snippet: Settings → Sign-in & keys → Your website. Test page: `/sdk/demo.html?key=<site key>`.
- **P1 batches 1-3 (done):** journey power steps, frequency caps and quiet hours; static segments, tags, scoring, identity merge;
  forms (hosted `/f/{key}` and `data-pulse-form`), preference center, STOP/START replies, broadcast A/B tests,
  broadcast and journey reports, funnels.
- **P1 batch 4 (done):** custom roles, audit log, outbound webhooks (`webhooks` role), background imports, bounce and
  complaint webhooks, standard web push, a custom HTTP gateway for any channel, and the Flutter SDK
  ([sdks/flutter/pulse_sdk](sdks/flutter/pulse_sdk)); native Android and iOS SDKs came with batch 8b.
- **App inbox (done):** an `inbox` channel kept by Pulse itself; journeys (`send_inbox`) and broadcasts send to it, the
  SDKs list and update it (`GET/POST /v1/web/inbox`, `Pulse.inbox`), reads and clicks count in reports.
  A signed-in user's inbox always needs their user hash (identity verification); anonymous visitors' doesn't.
- **In-app campaigns (done):** `/v1/inapp` to manage them (with `GET /v1/inapp/check` and `POST /v1/inapp/{id}/test`),
  `POST /v1/web/inapp` for the app SDKs; the Flutter, Android and iOS SDKs render modal, banner and full-screen messages.
  Segment targeting counts for signed-in users only with their user hash. (On-site focus items follow the workspace's
  identity-verification setting instead.)
- **Batch 5 (done):** GeoIP and the anonymous-tracking switch, retention cleanup (since M5 under Settings → Automation &
  limits), journeys
  grouped into Actions/Conditions/Decisions/Waits with message-engagement decisions, and segment presets with date rules.
- **Batch 6, contacts (done):** lifecycle stages (Settings → Stages; by hand, API, the `change_stage` journey step, or
  points thresholds that only move contacts forward), companies (Audience → Companies; attributes copied to contacts as
  `company_<key>`, linked by hand or by a server identify with `attrs.company`), and duplicate merge
  (`POST /v1/contacts/merge`, `GET /v1/contacts/duplicates`, Contacts → Duplicates and "Merge into…").
- **Batch 6, content (done):** every email carries a plain-text part (written in the template's Plain text tab, or
  generated from the HTML) and may attach up to 10 files (10 MB together); Content → Assets holds uploaded files
  (25 MB each, kept in Postgres) downloadable at `/a/{key}`, openly or behind a form (`asset_downloaded` for the
  submitter); Content → Landing pages publishes pages at `/p/{workspace}/{page}` with embedded forms, a schedule, a
  script-free preview and views/visitors/submissions from the web SDK's `landing_page_viewed`.
- **Rich text editor (done):** a CKEditor-style editor (TipTap) on every HTML field: email templates and landing pages
  (a Rich text tab beside drag and drop and HTML), focus items and web content, with variables and a Source view.
- **Batch 7, analytics (done):** cohorts / retention (`POST /v1/cohorts/run`, saved under `/v1/cohorts`), revenue
  attribution (`/v1/revenue/settings`, `POST /v1/revenue/report`: last touch, first touch or linear over broadcasts,
  journeys and UTM campaigns; recipients' revenue in broadcast and journey reports), UTM capture in the web SDK
  (`first_utm_*` / `last_utm_*` profile attributes, the session's campaign on `page_view`), funnel breakdowns
  (`breakdown: {attr}`), a custom report builder (`POST /v1/reports/run`, saved under `/v1/reports`) and automatic A/B
  winners on journey split steps (`winner: {metric, after_seconds, min_per_variant}`).
- **Batch 7b, segment builder (done):** one searchable, grouped "Add filter" picker (Contact, Company, Location, Device,
  Consent & deliverability, Behaviour, Points & stage, Segments & tags; every custom attribute listed with its type;
  recently used first; keyboard accessible), typed filter rows with suggested values, behaviour filters as sentences, and
  explicit nested "Match ALL / ANY of the following" groups. New data for Mautic's filters: last-seen device and locale
  from both SDKs, browsing sessions (`session_start`), `email_domain`, `date_added` / `date_identified` /
  `date_modified`, contact owners (`PUT /v1/contacts/{id}/owner`), SMS bounces (`sms_bounced`), manual vs self-service
  unsubscribes (`method` on `unsubscribed`), and SMS / RCS link click tracking (`/t/s/{code}`, `sms_clicked`). New rule
  operators: doesn't contain, starts / ends with, is not one of, is not set, between, on a date. Mautic filter mapping:
  [docs/05-feature-parity.md](docs/05-feature-parity.md#segment-builder-batch-7b).
- **Batch 7b, campaign builder (done):** "+" on every journey path asks Action, Decision, Condition, Wait or Split, then
  a searchable list with descriptions (disabled items say why); every Mautic campaign decision, action and condition is
  a step: form / asset / page / device / dynamic content decisions, opens / clicks (any or one link) / replies to email
  (inbound reply tracking: `reply_to` delivery setting, `POST /v1/replies/{token}`), marketing messages with channel
  fallback, email to users, show focus item and push dynamic content per contact, owner / company / company score,
  change campaigns, jump to step (25 jumps per enrollment at most), Do Not Contact, delete contact (typed confirmation),
  where deleting, removing DNC and owner / company steps need contacts edit access, and conditions on campaigns, form fields, notifications, valid email (MX), DNC, a date and page
  history. The canvas has Yes/No colours, step numbers, Tidy up, zoom to fit, minimap, undo/redo and per-step counts.
  Mapping: [docs/05-feature-parity.md](docs/05-feature-parity.md#campaign-builder-batch-7b).
- **Batch 8a, integrations and audience sync (done):** Settings → Integrations, a connector registry per workspace
  (credentials sealed with `PULSE_SECRET_KEY` and only ever shown masked, a status, last sync, a sync log, Test connection
  and previews):
  - **HubSpot** (private app token) and **Salesforce** (connected app, client credentials; Contact or Lead): contacts
    pulled on a schedule (15 minutes to a day) through a field mapping (remote field ↔ attribute, both ways, into Pulse or
    out to the CRM; reserved attributes can only be pushed), as `crm_synced` events keeping `hubspot_id` /
    `salesforce_id`; a contact is pushed on demand (`POST /v1/integrations/{id}/push`) or by the journey step
    **Push contact to integration** (`push_integration`; a failed push records `integration_push_failed` and the journey
    carries on).
  - **Shopify**: `POST /v1/webhooks/shopify/{id}` takes orders/create, customers/create and checkouts/create|update,
    verified with Shopify's HMAC, as `order_completed` (amount, revenue, currency, items: set Analytics → Revenue to
    `order_completed` / `amount`), `customer_created` and `cart_updated` (abandoned carts) on the contact with that email
    or phone.
  - **Zapier / Make** (REST hooks): the connector has its own API key (`pzk_…`); `/v1/hooks/subscriptions` subscribes a
    URL to `contact_created`, `segment_entered` / `segment_exited` (optionally one segment), `form_submitted` (optionally
    one form) or any event, delivered like outbound webhooks (signed, retried, a 410 unsubscribes); actions
    `/v1/hooks/contacts`, `/v1/hooks/events`, `/v1/hooks/tags`.
  - **Audience sync**: link a segment to a Facebook Custom Audience or a Google Ads Customer Match list (existing, or
    created by Pulse). Members go up as SHA-256 hashes of the normalised email and phone (E.164), in batches; entries and
    exits stream from `segment.membership`, consent and address changes from `events.enriched`, with a full reconcile
    every `PULSE_AUDIENCE_RECONCILE_INTERVAL`. Contacts unsubscribed from everything are never uploaded. Linking needs
    settings and segments edit access.
  Every remote base URL is a setting (the real one by default, only the vendor's own hosts outside local
  development; changing it asks for the credentials again), and every call goes through the SSRF-guarded client.
- **Batch 8b, reach (done):**
  - **Native SDKs:** Kotlin for Android ([sdks/android](sdks/android): `pulse-core`, plain Kotlin/JVM, and
    `pulse-android`) and Swift for iOS ([sdks/ios](sdks/ios): Swift package, `PulseCore` + `Pulse`), with the Flutter
    SDK's features plus push click / dismiss tracking, a local opt-out and storage namespaced by site key. The cores are
    unit-tested in Docker (`gradle:8-jdk17`, `swift:5.10`); the Android module compiled with lint in an Android SDK
    image, the iOS UIKit / UserNotifications code has not been compiled (no Xcode here). See each README.
  - **RCS replies:** Google RBM webhooks (client token check, signed with HMAC-SHA512) bring typed replies and tapped
    suggestions as `rcs_received`, delivered / read receipts, STOP / START keywords, and `rcs_replied` on the latest
    RCS message the contact got in 7 days, so a journey's message decision can wait for "replied" on RCS.
  - **Bounce mailbox:** Settings → Channels → Bounce mailbox (IMAP, TLS or STARTTLS, password sealed and shown masked) is read
    every 5 minutes for delivery status notifications (RFC 3464) and feedback-loop complaints (RFC 5965 / ARF), handled
    like bounce webhooks; read messages are flagged `\Seen`.
  - **Push click tracking:** every push carries `data.pulse_track`; the web SDK's service worker and the Android and iOS SDKs
    post clicks and dismissals there (`POST /t/p/{token}?a=clicked|dismissed`), recorded as `push_clicked` /
    `push_dismissed`; reports count clicks and a message decision can wait for "clicked" on push.
  - **Forms:** file upload fields (types checked against the content, size limits, kept as private assets, downloaded
    only by signed-in users with contacts access) and multi-page forms (page breaks, each page checked before the next,
    a progress bar) in the builder, the hosted page, the web SDK embed and the landing page preview.
  - **Audit log export:** CSV (formula-safe) or JSON, filtered by person, object type and dates, streamed
    (`GET /v1/audit/export`, same access as the log).
- **Batch 9, contact views and navigation (done):**
  - **Contact views:** the Contacts search box takes filters as removable chips with suggestions as you type:
    `is:anonymous`, `is:identified`, `is:unsubscribed`, `is:bounced`, `is:dnc`, `segment:<name or id>` (`list:` too),
    `tag:`, `stage:`, `company:`, `owner:` (`me`, `none`, or a teammate for users with settings view access), `points:>N` /
    `<N` / `=N`, `email:`, `name:`, and free text, ANDed. Filters compile to a segment rule (`internal/api/contactview.go`) and run in ClickHouse with
    the segment compiler, keyset-paged, with a count that is exact up to 10,000 and approximate above. "Save as segment"
    stores that rule as a dynamic segment; "View contacts" on every segment opens `segment:<id>` (`in:<id>` since M7, so the list has exactly the segment's size); a column chooser; and
    "Export CSV" of the view with its columns (`GET /v1/contacts/export?q=&columns=`, contacts edit access).
  - **One navigation:** Audience (Contacts, Companies, Segments, and Events since M1), Campaigns (since M7 one
    **Campaigns** entry and **Website & app messages**),
    Content (Message templates, Landing pages, Forms, Assets), Analytics (Reports, Funnels, Cohorts, Revenue), and one
    Settings entry with its own menu (Workspace, Contact data, Delivery, Connections). Segments and lists are one
    Segments page with Dynamic and Static segments; focus items, web content and in-app messages are one "Website &
    app messages" list (Website or Mobile app first); imports are one flow under Contacts (upload, map columns, static
    segment, review, background run, one history). Old addresses redirect. Decisions:
    [docs/05-feature-parity.md](docs/05-feature-parity.md#navigation-batch-9).
- **Docs review fixes (done):** found while writing the docs:
  - **Privacy:** **Delete contact** and **Download data** (profile, consent, segments and events as JSON) on the
    contact profile and as `DELETE /v1/contacts/{id}` / `GET /v1/contacts/{id}/data` (contacts edit, audited).
  - **Campaigns:** broadcasts leave out members of excluded segments and can send at the chosen clock time in each
    contact's own time zone; journeys start on **submits a form** (any form or one); journeys that never ran can be
    deleted from the list and those that ran archived (hidden, restorable, history kept); activation takes effect at
    once (the API tells the journey workers through Redis).
  - **Reports** export to CSV; **SSO** can be removed; a webhook's **Send test** is listed with its deliveries.
  - **Delivery:** a provider outage (429, 5xx, timeouts) no longer drops messages: retries back off from 30 seconds
    to an hour (honouring `Retry-After`) without holding up other messages, and giving up records `<channel>_failed`
    with the reason; `pulse replay-dlq` replays dead-letter topics.
  - **Robustness:** settings that don't parse stop start-up; forms without the time they were shown count as bots;
    a fresh clone builds and tests without building the web app first.
- **M1, events and segments (done):** the first milestone of the [marketing team plan](docs/06-marketing-team-plan.md):
  - **Event schemas** (Audience → Events): every event's volume, its properties as seen and as described, a schema
    editor (types, required, enums with labels, dependent enums such as sign-up method social → provider, personal
    data, "add to list field"), six suggested e-commerce schemas, and problems counted with samples. Warn by
    default; enforce (typed confirmation) makes `POST /v1/events` refuse events that don't fit; browser and SDK
    events are never refused.
  - **Segments:** property paths (`product.code`, `items[].code` for any item) typed by the schema, absolute date
    ranges for behaviour (ranges over a year back are left to the sweep), "is between" two dates, NONE groups
    (exclusions), list fields (has / doesn't have) and choice fields, `phone_country`, daily-refresh segments
    (Live or Daily at HH:MM in a time zone), and "why is this contact in this segment" on the contact page
    (`GET /v1/contacts/{id}/segments/{id}/explain`). Both compilers agree on all of it (`make it`).
  - **Journeys:** event triggers with property conditions ("only when payment_method is upi").
  - **Data:** `events.product_code` / `product_category` columns in ClickHouse; tutorial
    [12, events and properties](docs/tutorials/12-events-and-properties.md).
- **M2, personalisation (done):** standard variables in every templated field: `{{contact.first_name}}` (any contact
  field), `{{event.product_name}}` (the event that started the journey, carried to every message it sends, at most
  16 KB and without personal properties), `{{campaign.name}}`, `{{links.unsubscribe}}`, with pipes for defaults and
  formats (`default`, `upper`, `lower`, `title`, `date`, `money`, `number`, `truncate`, `urlencode`, `json`); the older
  `{{.Attrs.x}}` keeps working. A `{ } Variable` picker on every field, warnings for unknown variables, a per-template
  missing-value policy (leave empty, or skip the contact), preview and test send with a real contact and event
  (`GET /v1/templates/sample-event`), and "Send marketing message" rules that choose the channel by condition
  (`contact.phone_country = IN` → SMS, otherwise WhatsApp), with the choice in the step stats. Tutorial
  [13, personalising messages](docs/tutorials/13-personalising-messages.md).
- **M3, campaigns (done):** the **Campaigns** hub: one-time campaigns (broadcasts in the API) and journeys in one
  list, typed automatically (named One-time, Triggered and Automation since M7), with free tags, search and filters by status, channel and tag
  (`GET /v1/campaigns`). A **one-time campaign wizard** per channel (audience with segments in and out → content written
  in the template editor, kept with the campaign unless saved as reusable → schedule → review). An **exact audience
  estimate** after exclusions, consent, reachability and frequency caps, from Redis bitmaps per channel that the
  segments role keeps current (`POST /v1/segments/estimate`, `GET /v1/segments/{id}/reach`; cached 30 s, rate
  limited), also in the one-time campaign editor, the journey Start panel and segments. **Duplicate** broadcasts, journeys,
  templates and segments; **copy and paste** journey steps (between journeys and workspaces, checked by
  `POST /v1/journey-steps/check`) and segment filter groups. **Per-provider rates and daily quotas** shared by every
  dispatcher (over the quota, messages wait for the next day), with usage today on Settings → Channels. **Send
  deadlines** on broadcasts and send steps: late messages are dropped as `<channel>_failed` with reason `deadline`
  and counted in reports. **Pooled SMTP connections** (about 30x faster in the benchmark). **Saved test contacts** in
  every Send a test dialog. Tutorial [14, the Campaigns hub and one-time campaigns](docs/tutorials/14-campaigns-hub-and-one-time-campaigns.md).
- **M4, delivery truth and dashboards (done):**
  - **Linked delivery reports.** Every tracked email carries a signed message reference (the Message-ID's local part,
    `X-Pulse-Ref` and the sending service's own metadata header, chosen by the SMTP provider's **Sending service**
    setting: auto-detected for SendGrid, Mailgun, Postmark, SES and SparkPost), so bounces, complaints and deliveries
    count against the right campaign and contact. New `email_delivered` (sending services only: plain SMTP relays,
    Gmail, FCM and web push report no delivery, shown as "—"), SparkPost bounces and complaints, and bounce emails /
    feedback loops linked from the quoted original. Bounces from before M4 stay unlinked.
  - **Webhook verification.** SNS signatures for SES, SendGrid's signed event webhook, Mailgun's HMAC (each token
    accepted once), and basic auth for Postmark, SparkPost and generic posts, with keys saved on Settings → Channels →
    Bounces (`PUT /v1/settings/bounces/signing`, never shown again). Once any key is saved the workspace's webhook URL
    **fails closed**: a format with no check of its own needs the basic auth password, else it's refused (the page
    shows "Refused"); with no key saved, the secret URL alone is accepted as before ("URL secret only").
  - **Failure reasons.** Every failed, skipped, capped, bounced or complained message carries a reason code
    (invalid number, not on WhatsApp, undeliverable (possibly blocked), spam policy, consent, capped, deadline…), shown
    in reports and on the contact page.
  - **Funnels in reports:** audience at send time → targeted → skipped (by reason group) → sent → delivered →
    opened → clicked (with downloads), plus bounced, complaints, failed and unsubscribed; reasons tables per campaign
    and per journey step.
  - **Dashboards** (Analytics → Users, Campaign performance, Products, Live events): DAU / WAU / MAU by platform and UTM source
    (pre-aggregated in ClickHouse) and new vs returning users; every campaign's funnel and revenue; product views, carts,
    purchases and abandoned carts with "Segment of buyers"; a live event feed polled every 2 seconds. CSV export.
  - **Contact page** (`/contacts/:id`): who can be reached on which channel and why not, devices, triggered and
    automation campaigns with their current step, activity over 1/7/30/365 days, purchases, every message with its status and reason, and the event history.
  - Campaign names on the Campaign performance dashboard and the contact page show only to roles that may view that
    kind of campaign.
- **M5, channels and settings (done):**
  - **Any provider:** the custom HTTP gateway takes **custom authentication** (none, basic, bearer token, API key in a
    header or query, OAuth2 client credentials or refresh token, or a **login request** whose token is read by JSON path,
    response header or regular expression, with its expiry; tokens cached in Redis, shared by every dispatcher and
    refreshed once on a 401) and a **custom request** (method, URL, headers, body with `{{contact.x}}`, `{{event.x}}`,
    `{{campaign.x}}` and `{{message.x}}`, each escaped for where it lands; a success rule; the message id by JSON path;
    a delivery receipt mapping that is refused without the provider's receipt token). **Preview request** shows the
    exact request with secrets masked; **Test connection** runs the login now and shows the token masked.
  - **New providers:** Gmail (SMTP, 500/day), Google Workspace (SMTP, 2,000/day) — with an app password or a Google
    OAuth client (XOAUTH2) — and the Google Workspace SMTP relay (10,000/day, allowed by IP with an EHLO domain, or SMTP
    auth); Gupshup SMS (Enterprise, with DLT template and entity ids on SMS templates), Gupshup WhatsApp and Gupshup RCS
    (endpoints built from Gupshup's public docs and tested against mocks: confirm them with a real account). The full
    list: SMTP (any relay: SES, SendGrid, Mailgun, Postmark, SparkPost…), Gmail, Google Workspace, Workspace relay;
    Twilio, MSG91, Gupshup SMS; Meta WhatsApp Cloud API, Gupshup WhatsApp; Google RBM, Gupshup RCS; Firebase Cloud
    Messaging; web push (no provider: VAPID keys); the custom HTTP gateway (any channel); the sandbox (test inbox). A
    new provider starts with its kind's limits, else the workspace's defaults for the channel.
  - **Provider secrets** are merged key by key on save (an empty field keeps its saved value). Changing any setting that
    could steer a secret (a URL including path and query, the mode, authentication, the request template, receipt
    mapping, a user name) needs every secret entered again (`secrets_cleared`); harmless ones (sender name or number,
    pool sizes, DLT ids) don't. Requests that carry credentials (logins, token requests, the gateway, Gupshup) never
    follow a redirect to another site, a secret can't choose the host that is called, and Test connection with saved
    secrets works only for the saved settings and never shows a secret in its errors.
  - **Settings → Automation & limits** (settings view to read, settings edit to change; audited): every automatic
    behaviour Pulse had hard-coded, with its default (= the old behaviour) and range shown on the page:
    - *Keep data for* (days; 0 = forever): Events (default forever, 30–3650), Message events and Page and screen views
      (default: like Events), By event name (up to 100 names, 7–3650 or forever; always wins over the type-wide
      setting), Anonymous visitors (idle days, off by default, 7–3650), Audit log (365, 30–3650), Import history
      (forever, 7–3650), Webhook deliveries (7, 1–90), Integration log (7, 1–90), Test inbox (forever, 1–3650), Live
      event feed (1,000 events, 100–10,000).
    - *Retries of failed sends*: Tries in all (8, 1–20), Longest wait (1 hour, 1 minute to 1 day; also caps a
      provider's Retry-After).
    - *Schedules*: Re-check time-window segments every (server default `PULSE_SWEEP_INTERVAL`, 1–1440 minutes),
      Recount who can be reached (a day after the last recount, or a fixed UTC hour), New daily segments (06:00, the
      browser's time zone).
    - *Messages*: App inbox expiry (30 days, 1–365). *Default limits of new providers* per channel (rate per second,
      daily quota, time zone). *Fixed by the platform* lists what stays fixed and why.
    Shortening a retention opens **What will be deleted** (counts per log) and asks for confirmation. Retention moved
    here from Data & privacy and now runs **once a day per workspace** (in its own UTC hour), deleting per group of
    event names in ClickHouse and in batches in Postgres.
- **M6, AMP and on-site (done):**
  - **AMP emails:** an interactive AMP part on any email template, built from blocks (heading, text, image, button,
    carousel, gallery, accordion, poll, form; page, text and accent colours and font) or written as code, checked on
    save by Pulse's validator (errors with line numbers stop a save; warnings don't), with a script-free preview and
    send test. Polls and forms post back to Pulse (`POST /v1/amp/{token}`, CORS per the AMP for Email spec, only from
    the workspace's sender domains) as `amp_poll_answered` / `amp_form_submitted` events (one answer per contact per
    poll; form answers may update custom contact fields); poll results on the template. Every SMTP relay sends the AMP
    part **except Postmark**, which doesn't support AMP (the editor says so per provider); the custom gateway gets it as
    `content.amp`. Gmail shows AMP only to senders Google has registered. The sender domains allowed to post back are
    those of the workspace's email providers' From addresses and `PULSE_SMTP_FROM`.
  - **Content variants:** a website content slot shows the first variant whose audience matches (segments in and out,
    page URL contains / doesn't contain, devices, contact-field conditions), else the default; the editor orders them,
    warns about variants that can never show, and counts how many contacts would see each (overlap). **Focus items**
    take the same audience rules and an order: one pop-up, one bar and one slide-in per page, the first allowed.
  - **Web push prompt** (Settings → Web push): a no-code opt-in prompt (bell, bar or pop-up; texts, colours,
    triggers, ask again after N days, pages), an **install check** that opens your site and service worker from the
    server and reports what's missing, and a **browser check** link (`?pulse_check=`) that runs `Pulse.diagnose()` on a
    real page.
- **M7, clarity review (done):**
  - **The contacts behind every number.** Counts across the app (estimate steps, reach by channel, segment sizes,
    campaign funnels and reasons, journey steps, product dashboards, Users dashboard, events, forms, AMP poll answers,
    on-site overlap…) open the Contacts list filtered to exactly those people, with the filters as chips in words
    ("Campaign “Spring catalogue”", "Opened", "Reachable by email"). New `GET /v1/contacts?q=` filters (`in:`,
    `not_in:`, `subscribed:`, `reachable:`, `under_cap:`, `journey:` / `at:` / `journey_status:`, `campaign:` with
    `run:` / `step:` / `outcome:` / `reason:`, `event:` / `no_event:`, `active:`, `amp_poll:`, `form:`, `period:`),
    run on Redis bitmaps or ClickHouse; **Create segment from this list** saves such a list as a static segment.
    Numbers a role can't open stay plain with a tooltip; totals that aren't one list of people say so.
  - **One campaign model.** One **Campaigns** entry in the sidebar; three types named by when they send:
    **One-time** (a message to everyone in chosen segments, once or repeated), **Triggered** (starts for each person
    when they do something) and **Automation** (starts when they join a segment); triggered and automation campaigns
    are built as journeys. **New campaign** asks what you want to do. "Broadcast" no longer appears in the app;
    `/journeys` and `/broadcasts` redirect to the hub. Roles name the areas "One-time campaigns" and "Triggered &
    automation campaigns"; "On-site & in-app" is "Website & app messages".
  - One word per status (Live, Finished, Sending, Draft, Scheduled, Paused, Cancelled, Archived), "Send again" for a
    finished one-time campaign, provider options in words, and plainer settings.
- **Tutorials:** [docs/tutorials](docs/tutorials/README.md), step by step, all 18 written. Remaining gaps:
  [docs/05-feature-parity.md](docs/05-feature-parity.md#known-gaps).

## Run it

Only Docker is required. Go runs inside the `golang` image when it isn't installed.

```bash
make up        # Kafka, ClickHouse, Postgres, Redis, Mailpit, Keycloak + pulse (all roles, web app included)
make seed      # demo workspace with data (credentials: deploy/dev-seed.md)
make docs-seed # the "Acme Retail" workspace for documentation screenshots; make docs-shots retakes them
make test      # Go unit tests (no infrastructure)
make it        # SQL-vs-Go segment agreement test against ClickHouse (needs `make up`)
make e2e       # end-to-end (needs `make up`); webhook tests run in a container on the compose network
make load ARGS="-rate 10000 -duration 45s"   # load test; results in docs/03-load-test.md
cd web && npm install && npm run dev && npm test   # web app on :5173 (proxies the API), and its unit tests
```

| Service | URL |
|---|---|
| Pulse web app and API | http://localhost:7080 |
| Mailpit (sent mail) | http://localhost:18025 |
| Keycloak (dev SSO) | http://localhost:8180 |
| ClickHouse HTTP | http://localhost:18123 (user/password `pulse`) |

## Reserved attribute names

Pulse maintains some profile attributes itself. How far they are protected depends on who sends them:

- **Never settable by anyone** (dropped from every event's `attrs`, including your server's and CSV
  imports): `stage`, `stage_changed_at`, `company_id`, `company_name` and every `company_*` key,
  `date_added`, `date_identified`, `date_modified`, `email_domain`, `owner_id`, `owner_name`,
  `merged_into`, and the last-seen device (`device_type`, `device_os`, `device_os_version`,
  `device_brand`, `device_model`, `browser`, `app_version`: from the SDKs' event properties).
- **Also refused from browsers (site key), form fields and journey "update profile" steps**, but
  accepted from your server (API key `attrs`) and CSV imports: `tags`, `points`, the `optout_*`
  consent flags, `email_bounced` and `sms_bounced`, `first_seen` and `last_seen`. A server value
  replaces what is there (e.g. `"tags": ["vip"]` replaces the tag list, `"optout_email": true`
  unsubscribes without an `unsubscribed` event), so prefer the events below, which keep the
  history. A CSV import's tags column adds tags (as `tag_added` events) rather than replacing them. A
  new phone number clears `sms_bounced`.

`locale` is filled from the SDKs but may be set by your server. `first_utm_*` and `first_referrer`
(first-touch attribution, set by the web SDK) can be set but keep their first value. Change Pulse's
attributes through their own APIs and events: tags (`tag_added` / `tag_removed`,
`POST /v1/contacts/{id}/tags`), points (`points_changed`, `POST /v1/contacts/{id}/points`), stages
(`PUT /v1/contacts/{id}/stage`, journey step `change_stage`), owners (`PUT /v1/contacts/{id}/owner`),
companies (`PUT /v1/contacts/{id}/company`, or an identify with `attrs.company` from your server),
consent (`unsubscribed` / `resubscribed`). Name your own fields differently, e.g. `team_size` rather
than `company_size`.

## Settings

Every setting is a `PULSE_*` environment variable (`internal/config/config.go`). The defaults suit `pulse all` on the
host against the dev compose stack's published ports; `deploy/docker-compose.yml` sets the in-network addresses. A
number, duration or switch that doesn't parse stops start-up with an error naming the variable.

| Variable | Default | Meaning |
|---|---|---|
| `PULSE_HTTP_ADDR` | `:7070` | Address the `api` role listens on |
| `PULSE_PG_DSN` | `postgres://pulse:pulse@localhost:55432/pulse?sslmode=disable` | Postgres connection string |
| `PULSE_CH_ADDR`, `PULSE_CH_DB`, `PULSE_CH_USER`, `PULSE_CH_PASSWORD` | `localhost:19000`, `pulse`, `pulse`, `pulse` | ClickHouse native protocol address, database and credentials |
| `PULSE_REDIS_ADDR` | `localhost:16379` | Redis address |
| `PULSE_KAFKA_BROKERS` | `localhost:19092` | Kafka brokers, comma-separated |
| `PULSE_KAFKA_PARTITIONS` | `12` | Partitions per topic when `migrate` creates the topics (caps each role's parallelism) |
| `PULSE_SMTP_ADDR` | `localhost:11025` | SMTP relay used when a workspace has no email provider of its own |
| `PULSE_SMTP_FROM` | `Pulse <noreply@pulse.local>` | Default email sender; its domain may also post back AMP forms and polls |
| `PULSE_ADMIN_TOKEN` | none | Platform operator token for `POST /v1/tenants`; unset turns that route off |
| `PULSE_ALLOW_SIGNUP` | `true` | Anyone may create a workspace at `/signup`; `false` turns signup off |
| `PULSE_PUBLIC_URL` | `http://localhost:7080` | Where browsers reach Pulse: SSO redirects, Secure cookies, tracking, unsubscribe and webhook URLs |
| `PULSE_SWEEP_INTERVAL` | `5m` | How often segments with event windows or relative dates are re-checked (a workspace may set its own under Settings → Automation & limits) |
| `PULSE_EMAIL_RATE` | `200` | Emails per second per workspace, fractions allowed. A provider's own rate never goes above it |
| `PULSE_SMS_RATE`, `PULSE_WHATSAPP_RATE`, `PULSE_RCS_RATE`, `PULSE_PUSH_RATE`, `PULSE_INBOX_RATE` | `100`, `80`, `50`, `500`, `2000` | The same per-workspace send rate (messages per second) for SMS, WhatsApp, RCS, push and the app inbox |
| `PULSE_SECRET_KEY` | none | 64 hex characters; encrypts stored secrets, SSO state, tracking links, webhook secrets and download links (form-gated assets, attachment links for the custom gateway need it) |
| `PULSE_TRUST_PROXY` | `false` | Take the client IP (for rate limits and the audit log) from the right-most `X-Forwarded-For` entry. Turn on only behind your own reverse proxy |
| `PULSE_ALLOW_PRIVATE_SSO` | `false` | Let SSO discovery reach private addresses (an IdP on your internal network) |
| `PULSE_ALLOW_PRIVATE_WEBHOOKS` | `false` | Let journey webhooks, outbound webhooks, channel providers and the custom gateway (including login and token requests), integrations (CRM, Shopify, ad platform APIs), the IMAP bounce mailbox and the web push install check reach private addresses, and plain http where https is otherwise required (development only) |
| `PULSE_AUDIENCE_RECONCILE_INTERVAL` | `24h` | How often each ad audience sync re-checks its whole segment (entries and exits stream in between) |
| `PULSE_GEOIP_DB` | none | Path to a MaxMind GeoLite2-City or GeoIP2-City `.mmdb` file (download with a free MaxMind licence key, e.g. via `geoipupdate`); unset turns geolocation off |
| `PULSE_MAINTENANCE_INTERVAL` | `1h` | How often the maintenance pass looks for work: each workspace's retention cleanup still runs once a day, in its own UTC hour (Settings → Automation & limits) |
| `PULSE_PAGES_URL` | none | A separate origin for landing pages, asset downloads and hosted forms, e.g. `https://pages.example.com`. **Set it in production**: workspace-authored pages then never share an origin or cookies with the app (no lookalike login pages on the app's domain); those paths redirect there, and that host serves nothing else |

In production set at least `PULSE_SECRET_KEY`, `PULSE_PUBLIC_URL` (https) and `PULSE_PAGES_URL`, and consider
`PULSE_ALLOW_SIGNUP=false` (with `PULSE_ADMIN_TOKEN` to create workspaces through the API). The dev compose file turns the two `ALLOW_PRIVATE` switches on for the local Keycloak
and the e2e webhook receivers.

## API walkthrough

```bash
# 1. Create a tenant (admin token from deploy/docker-compose.yml) and keep its API key
curl -s -X POST localhost:7080/v1/tenants -H 'Authorization: Bearer dev-admin-token' -d '{"name":"Acme"}'
KEY=pk_...

# 2. Template, segment, journey
curl -s -X POST localhost:7080/v1/templates -H "Authorization: Bearer $KEY" \
  -d '{"name":"welcome","subject":"Welcome {{contact.first_name | default:\"friend\"}}","html":"<p>Hi {{contact.first_name}}</p>"}'
curl -s -X POST localhost:7080/v1/segments -H "Authorization: Bearer $KEY" \
  -d '{"name":"Delhi buyers","rule":{"all":[{"attr":"city","op":"eq","value":"Delhi"},{"event":"purchase","op":"gte","count":1,"days":7}]}}'
curl -s -X POST localhost:7080/v1/journeys -H "Authorization: Bearer $KEY" \
  -d '{"name":"welcome","graph":{"trigger":{"type":"segment_entered","segment_id":1},"entry":"send",
       "nodes":{"send":{"type":"send_email","template_id":1,"next":"done"},"done":{"type":"exit"}}}}'
curl -s -X POST localhost:7080/v1/journeys/1/activate -H "Authorization: Bearer $KEY"

# 3. Send events. The email shows up in Mailpit within a few seconds.
curl -s -X POST localhost:7080/v1/events -H "Authorization: Bearer $KEY" -d '{"events":[
  {"external_id":"u1","name":"identify","email":"u1@example.com","attrs":{"city":"Delhi","first_name":"Asha"}},
  {"external_id":"u1","name":"purchase","props":{"amount":499}}]}'
curl -s localhost:7080/v1/contacts/u1 -H "Authorization: Bearer $KEY"   # profile + segment ids
```

## Layout

```
cmd/pulse/          one binary, one role per argument (migrate, api, profile, chsink, segments, journeys, dispatch,
                    webhooks, integrations, all), and `replay-dlq <topic>` to move dead-letter records back
cmd/seed/           demo workspaces for `make seed` and `make docs-seed`; cmd/loadgen/: the load generator for `make load`
internal/segment/   rule AST, Go evaluator, ClickHouse SQL compiler, Redis bitmaps and day counters
internal/segengine/ streaming membership, backfill, sweep
internal/reach/     reachability bitmaps per channel and exact audience estimates
internal/journey/   graph validation, executor, triggers and durable timers
internal/dispatch/  every channel: idempotency, token buckets, provider quotas, deadlines, consent, caps, quiet hours, fallback
internal/profile/   identity resolution, attribute merge
internal/chsink/    events.enriched -> ClickHouse
internal/integrations/ CRM, Shopify, REST hook and ad audience connectors; audience sync
internal/bounces/   bounce and delivery webhooks (verification, linking by message reference), DSN / ARF parsing, the IMAP
                    bounce mailbox poller
internal/reasons/   reason codes for failed, skipped, bounced and complained messages
internal/httpauth/  custom HTTP authentication for providers (basic, bearer, API key, OAuth2, login) and its token cache
internal/automation/ Settings → Automation & limits (retention, retries, schedules, defaults); internal/maintenance/ runs
                    the daily retention cleanup
internal/amp/       AMP for Email: validator, block builder, post-back placeholders, script-free preview
internal/onsite/    focus items and content slots: audiences, decisions, never-shown variants, overlap counts
internal/webcheck/  web push opt-in prompt settings and the install check's safe fetcher
internal/live/      the live event feed (a capped Redis stream per workspace)
sdks/               Flutter, Android (Kotlin) and iOS (Swift) SDKs
internal/api/       HTTP API
internal/store/     Postgres and ClickHouse stores
migrations/         embedded SQL
test/e2e/           end-to-end tests against the running stack
```

## Known Phase 1 limits

The full list of remaining gaps is in [docs/05-feature-parity.md](docs/05-feature-parity.md#known-gaps).

- Automation campaigns (journeys) don't exit contacts when they leave the trigger segment (use a condition, a goal or "Change campaigns").
- A backfill that runs while live events arrive for the same segment can overwrite a fresh streaming
  change. The next sweep fixes it.
- Postgres Row-Level Security is not enabled yet. Tenant isolation is enforced in every query (and tested at the API level).
- Measured on one 12-core machine running everything: 10k events/s at p95 313 ms and 20k events/s sustained.
  Redis (single-threaded) and the per-poll barrier are the next limits. See [docs/03-load-test.md](docs/03-load-test.md).
