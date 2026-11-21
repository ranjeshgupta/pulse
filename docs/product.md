# Pulse product overview

Pulse is a real-time, multi-tenant marketing automation platform that a company runs on its own
servers. It keeps every contact and what they do on websites and apps, groups them into segments
the moment they qualify, and reaches them with one-time, triggered and automation campaigns over
email, SMS, WhatsApp, RCS, mobile and web push, an in-app inbox, in-app messages and website
content.

This document is for product leaders: who Pulse is for, the problems it solves, every feature
area with its honest limits, the workflows a marketer follows, and what sets it apart. How it is
built is in the [architecture document](architecture.md); how to use each screen is in the
[tutorials](tutorials/README.md).

---

## Who it's for

- **Growth and CRM marketing teams** at consumer businesses (e-commerce, fintech, edtech, media,
  travel, subscription apps) that message millions of customers across several channels and need
  campaigns to react to behaviour within seconds.
- **Businesses in India and similar markets** where SMS needs DLT registration, WhatsApp and RCS
  are primary channels, phone numbers matter as much as emails, and data protection law (the DPDP
  Act) applies.
- **Agencies and platform teams** that serve many brands from one installation: every brand is a
  separate workspace with its own team, roles, channels and data.
- **Companies that must keep customer data in-house** (regulated industries, data residency, or a
  preference not to send customer data to another vendor's cloud).

## The problems it solves

| Problem | What Pulse does |
|---|---|
| Segments and campaigns that react minutes or hours late because they are recomputed on a schedule | Each event updates the contact's segments and starts campaigns within a fraction of a second at normal load |
| Tools that slow down as the contact base grows | Work per event stays constant; built and load-tested for millions of contacts per workspace |
| One tool per channel, and no single view of consent and reach | One audience, one consent model and one campaign model across email, SMS, WhatsApp, RCS, push, inbox, in-app and website |
| "Why didn't this person get the message?" | Every message that didn't go out has a reason (unsubscribed, bounced, capped, quiet hours, deadline, invalid number…), shown in reports and on the contact |
| Report numbers nobody can act on | Every count of people opens the list of exactly those people, ready to export or save as a segment |
| Data that has to leave the company | Self-hosted: data stays in the company's own databases |
| Being locked into one SMS or email vendor | Any SMTP service, several SMS / WhatsApp / RCS providers, and a no-code connector for any provider with an HTTP API |

---

## Features

Each feature is described in plain words, followed by its main limit where there is one. "Limit"
means what Pulse does **not** do today.

### Audience and data

- **Contacts with any custom fields.** Fields are added without database changes; text, number,
  date, yes/no, list and choice fields have labels and a personal-data flag.
- **Anonymous visitors that become known.** Website and app visitors are tracked anonymously (a
  per-workspace switch) and merged into the known contact, with their history, when they identify.
- **Identity keys and duplicate merge.** Admins choose which fields identify a person (customer ID,
  email, phone, any unique field) and their priority; matching contacts merge automatically or are
  listed under Duplicates for a person to merge. *Limit: merges can't be undone; only server-side
  data triggers automatic merges (never what a visitor types on a website).*
- **Event tracking from servers, websites and apps,** with optional event schemas: property types,
  required fields, allowed values and dependent properties, checked as events arrive (warn or
  enforce), with problem samples. *Limit: browser events are never refused, only flagged.*
- **Tags, lead scoring, lifecycle stages and companies.** Points per event, stages that move
  forward at point thresholds, B2B companies whose fields are copied onto their contacts.
- **Contact list with filters as plain questions** ("did an event between two dates", "got a
  campaign and opened it", "reachable by WhatsApp"), chosen columns, CSV export, save as segment.
- **Contact page**: reach on each channel and why not, devices, campaigns and their current step,
  every message with its outcome and reason, purchases, products, and the full event history.
- **Imports** of CSV files up to 10 GB in the background, with column mapping, a target static
  segment, progress and history.
- **Location from IP** (country, region, city, time zone) with an optional GeoIP database.

### Segmentation

- **Real-time segments** on contact fields, event counts in a time window (with property filters
  such as "purchase where `items[].category` is lamps"), tags, and other segments, in nested
  ALL / ANY / NONE groups, built in a searchable filter picker.
- **Static segments** (manual lists) filled by imports, forms, journey steps or by hand.
- **Daily-refresh segments** recomputed once a day at a chosen time, for heavy rules that don't
  need to be live.
- **Exact reach per channel**: how many members can actually be reached by email, SMS, WhatsApp,
  RCS or push (address present, not unsubscribed, not bounced).
- **"Why is this contact in this segment?"**, rule by rule, with the contact's values.
- **Segment from a sentence** with AI help (see below).
- *Limits: behaviour windows older than 365 days are recomputed every few minutes rather than
  instantly; no "currently in a journey" rule, regular-expression operator or point groups;
  property paths go through one list at most.*

### Campaigns and journeys

- **One Campaigns list** with three types named by when they send: **one-time** (to segments,
  once or repeating), **triggered** (when someone does something or submits a form) and
  **automation** (when someone joins a segment), with tags, search, status and channel filters,
  and a **marketing calendar**.
- **One-time campaign wizard**: audience (segments in and out) → content → schedule → review,
  with an exact estimate after exclusions, consent, reachability and frequency caps at every step.
- **Sending options**: each contact's local time, each contact's best hour, a sending speed
  (messages per hour), a send deadline, a choice of provider, A/B tests of 2–5 variants with an
  automatic winner, and control groups that measure uplift. *Limit: local-time and best-time
  sending are for one-time sends, not repeating ones.*
- **Visual journey builder** for triggered and automation campaigns: actions (send on any
  channel, send on the first channel the contact can receive, update fields, tags, points, stage,
  owner, company, static segments, Do Not Contact, push to a CRM, webhook, notify a teammate,
  show website content), decisions with time limits (opened, clicked, replied, read, delivered,
  visited a page, submitted a form, downloaded an asset), conditions (any segment rule, other
  journeys, valid email, form answers), waits, A/B splits with automatic winners, goals, re-entry
  rules, jumps, undo / redo, copy and paste of steps between journeys, and live per-step counts.
  *Limit: contacts don't leave a journey automatically when they leave its trigger segment; a
  condition or goal handles that.*
- **Approvals (maker / checker)**: people who can't publish ask for approval, and a publisher
  approves exactly the version requested; a workspace can require four-eyes launches for everyone.
  *Limit: no email notice to approvers yet (the list and banner update in the app).*
- **Frequency caps and quiet hours** across all campaigns per channel, in each contact's time
  zone; transactional messages (OTPs, receipts) are exempt.

### Channels

- **Email** over any SMTP service, with one-click settings for common sending services and
  Microsoft 365, Gmail and Google Workspace (app password or OAuth), pooled connections, open and
  click tracking, one-click unsubscribe, plain-text part, attachments, reply tracking and
  **interactive AMP emails** (carousels, polls, forms whose answers come back to Pulse). *Limits:
  plain SMTP relays and Gmail give no delivery reports; Gmail shows AMP only to registered senders.*
- **SMS** through Twilio, MSG91 and Gupshup, with **DLT entity and template IDs**, link
  shortening and click tracking, STOP / START keywords and replies.
- **WhatsApp** through the Meta Cloud API or Gupshup (approved templates and session messages),
  with **automatic SMS fallback** when the number isn't on WhatsApp or the message is
  undeliverable. *Limit: buttons in approved templates can't be click-tracked.*
- **RCS** through Google RBM or Gupshup (rich cards and suggestions, replies and taps), with SMS
  fallback. *Limit: Gupshup's RCS formats are inferred from Google's and need confirming with a
  real account.*
- **Mobile push** through Firebase (Android and iOS) and **web push** with Pulse's own keys (no
  Firebase needed), with click and dismiss tracking and uninstall detection. *Limit: iOS push goes
  through Firebase tokens, not directly to Apple.*
- **App inbox** and **in-app messages** (modal, banner, full screen) in the mobile SDKs.
- **Any other provider** through the custom HTTP gateway: a four-step wizard (sign in, send, was
  it accepted, delivery reports by webhook or by asking the provider) that fills itself from the
  provider's pasted curl or Postman examples, with a "Try it" at each step.
- **Several providers per channel**, chosen per campaign, step or test send; per-provider rate and
  daily quota.
- **Delivery truth**: delivered, bounced (hard / soft) and spam complaints linked to the exact
  campaign, step and template; bounce mailboxes read over IMAP; configurable bounce rules.
- **Sandbox providers** that send nothing and show every message in a test inbox, for trying
  every channel and fallback without real accounts.
- *Overall limit: the built-in provider adapters were built from public documentation and tested
  against simulated providers, not live accounts.*

### Content

- **Message templates** for every channel: a drag-and-drop email builder, a rich text editor and
  HTML, with previews as a real contact and test sends to saved test contacts.
- **Reusable content blocks** (header, footer, offer) used in any template.
- **Multi-language templates**: a version per language, chosen by each contact's language.
- **Spam check and sender domain check**: a content score with a fix for each issue, and SPF,
  DKIM, DMARC, BIMI and MX checks against the large mailbox providers' bulk-sender rules. *Limit:
  content rules only; no seed-inbox testing.*
- **Forms** (hosted or embedded; multi-page, file uploads, consent, double opt-in), **landing
  pages** (drag-and-drop, rich text or HTML) and **gated downloads**.
- **Surveys and NPS** with a personal link in any message, results and NPS trend. *Limit: no
  branching and no in-app survey widget yet.*
- **Product catalog** with CSV import, used for recommendations.
- **RSS-to-email** and a **public archive** of email campaigns. *Limit: items dated in the future
  wait for a later send, and items without a date go only into the first send.*

### Personalisation

- **Variables** from the contact (`{{contact.first_name}}`), from the event that started the
  campaign (`{{event.product_name}}`), from the campaign and from links, with defaults and formats
  (dates, money, numbers, truncation), and a picker on every field.
- **Choose the channel by condition** (for example Indian numbers by SMS, others by WhatsApp).
- **Product recommendations** per contact at send time: bought together with what they bought or
  viewed, then their views, trending and popular items, in stock and not just bought. *Limit:
  co-purchase and popularity lists, not a machine-learned model.*
- **Missing-value policy** per template: send with blanks or skip the contact.

### Analytics

- **Overview** of what needs attention (provider near its daily quota, failing channel, bounce
  spike, failing campaign), audience growth, revenue and campaigns in flight.
- **Campaign reports**: a funnel from audience to clicks, failure reasons, results per run,
  variant, step and channel, charts over time, revenue of recipients and control-group uplift.
- **Template reports** across every campaign that used a template, with the busiest hours.
- **Dashboards** for users (daily, weekly and monthly actives, new vs returning), campaign
  performance and products (views, carts, purchases, abandoned carts), and a live event feed.
- **Funnels** with breakdowns, **cohorts and retention**, **revenue attribution** (first touch,
  last touch, linear, over messages and UTM campaigns), a **custom report builder** and **path
  analysis** (what people do before or after an event).
- **Customer value (RFM)**: buyers grouped from champions to lost, with churn risk, ready-made
  segments and a trigger when a buyer moves group or leaves the two-year window. *Limit: rule-based,
  no predictive model; only the last two years of purchases count.*
- **Every number of people opens those people.** *Limit: a few totals (cohort and funnel cells,
  canvas path counters, audience at send time) are counts without a list.*

### Website and app engagement

- **Web SDK**: page views, sessions and UTM capture, identify and events, verified identity,
  forms, pop-ups, bars and slide-ins with triggers and frequency limits, personalised content
  slots with audience rules and priority, web push opt-in prompt, app inbox.
- **Mobile SDKs** for Flutter, Android and iOS: events, push, inbox, in-app messages and **app
  content slots** (personalised cards or HTML inside the app). *Limit: the Android and iOS UI code is
  compiled on every push but hasn't been run on a device; the Flutter SDK doesn't report push
  taps.*
- **Overlap and "never shown" checks** for website content variants, and a "see what a contact
  would see" preview.
- **Web push install check** from the server and a self-check link that runs in the visitor's
  browser.

### Governance and security

- **Workspaces** with their own team, roles, channels, settings and data on one installation.
- **Roles**: admin, editor, viewer and custom roles with none / view / edit / publish per area.
- **Single sign-on** with any OpenID Connect provider (verified emails only; an existing password
  account is linked only after its owner signs in with the password) and **two-step sign-in** with
  an authenticator app, optional or required for everyone or chosen roles and people.
- **Sessions**: change your password (other devices are signed out), sign out everywhere, and
  automatic sign-out after 7 days without use (14 days at most).
- **Audit log** of every change and sign-in, exportable, with secrets redacted.
- **Encrypted credentials**, shown masked; changing a provider's address requires its secrets
  again; a webhook's signing secret is shown in full only when created or rotated.
- **Privacy tools for DPDP Act and GDPR requests**: consent history in words, download a contact's
  data, correct, withdraw consent, delete; per-channel and per-topic consent; a preference centre;
  double opt-in; retention settings per kind of data. *Limits: deleting a contact doesn't recall
  them from ad audiences; there is no separate log of data-subject requests (the audit log records
  exports and deletions).*
- **Settings for everything automatic**: data retention, retries, schedules, default sending
  limits, bounce rules, unsubscribe behaviour, identity keys, control groups, approvals.
- **Nothing lost by accident**: every editor guards unsaved changes, and every delete asks first
  (typing the name for big ones). *Limit: deletes have no undo.*

### Integrations

- **HubSpot and Salesforce**: scheduled pulls with field mapping, pushes on demand and from
  journeys.
- **Shopify**: orders, customers and checkouts as events.
- **Zapier and Make** through REST hooks (triggers and actions).
- **Facebook Custom Audiences and Google Ads Customer Match** kept in step with segments.
- **Outbound webhooks** for any event or segment change, signed and retried, with a delivery log.
- **REST API** for everything the app does.
- *Limits: no published Zapier / Make app; no one-click OAuth installs (CRM tokens and Shopify
  webhooks are set up by hand); HubSpot companies and deals aren't synced.*

### AI help

- **Optional, off by default.** With an Anthropic API key on the server, marketers get subject
  line and push title suggestions, rewrites (shorter, friendlier, simpler, more urgent, more
  formal, fix grammar), translation into a template's other languages, and a segment built from a
  sentence ("bought twice in 90 days, live in Pune").
- **Safe by construction**: nothing changes without a click; suggestions that drop a
  personalisation variable or produce an invalid segment are refused; only the text being edited
  (and field and event names) is sent, never contacts' data.
- *Limits: 20 requests a minute and 200 a day per workspace (the server's key pays for them); a
  text over 60 KB to rewrite, or a template over 120 KB to translate, is refused rather than cut.*
- *Limit: no image generation or send-time prediction beyond the best-hour feature.*

---

## Key workflows

### 1. Connect data

1. Create the workspace, invite the team, set roles, SSO and two-step sign-in.
2. Add the web SDK snippet and the mobile SDKs; send server events with the API key.
3. Describe key events (purchase, signup) with schemas; set the revenue event.
4. Import existing contacts from CSV, or connect HubSpot, Salesforce or Shopify.
5. Choose identity keys (customer ID, then email, then phone).

Tutorials: [getting started](tutorials/01-getting-started.md), [contacts](tutorials/02-contacts.md),
[events and properties](tutorials/12-events-and-properties.md), [integrations](tutorials/09-integrations.md).

### 2. Set up channels

1. Add an email provider (one-click settings), check the sending domain.
2. Add SMS with DLT IDs, WhatsApp and RCS (with SMS fallback), push keys.
3. Or connect any provider through the gateway wizard by pasting its API examples.
4. Set frequency caps, quiet hours, bounce rules and unsubscribe topics.
5. Send tests through the sandbox and to saved test contacts.

Tutorials: [channels](tutorials/04-channels.md), [connecting any provider](tutorials/16-connecting-any-provider.md),
[delivery and dashboards](tutorials/15-delivery-and-dashboards.md).

### 3. Build an audience

1. Start from the contact list's filters or the segment builder.
2. Check the live size and reach per channel; ask "why is this contact here?".
3. Save as a dynamic segment, or turn any list into a static segment.

Tutorials: [segments](tutorials/03-segments.md), [contacts behind a number](tutorials/18-contacts-behind-a-number.md).

### 4. Send a one-time campaign

1. Campaigns → New campaign → "Send a message to a segment".
2. Pick segments in and out; see the exact estimate.
3. Write the content (or reuse a template), personalise it, check spam score, preview as a contact.
4. Schedule: now, later, repeating, local time, best time, sending speed, deadline, A/B test,
   control group.
5. Launch, or ask for approval.
6. Watch the funnel and reasons; open any number to see the people.

Tutorials: [one-time campaigns](tutorials/07-one-time-campaigns.md),
[campaigns hub](tutorials/14-campaigns-hub-and-one-time-campaigns.md),
[templates and content](tutorials/05-templates-and-content.md),
[personalising messages](tutorials/13-personalising-messages.md).

### 5. Automate a lifecycle journey

1. Campaigns → New campaign → "React when someone does something" (for example cart abandoned) or
   "Follow up when someone joins a segment".
2. Add steps with "+": wait, send WhatsApp with SMS fallback, decide on clicked within a day,
   branch, update fields, set a goal.
3. Activate; the next matching event enters the journey.
4. Watch per-step counts and the report over time.

Tutorial: [triggered and automation campaigns](tutorials/06-triggered-and-automation-campaigns.md).

### 6. Engage on the website and in the app

1. Create pop-ups, bars or slide-ins with audiences, triggers and frequency.
2. Create content slots with variants per audience, on the web or in the app.
3. Launch in-app messages and inbox messages; set up the web push prompt.

Tutorials: [website and app messages](tutorials/08-website-and-app-messages.md),
[interactive AMP emails](tutorials/17-interactive-amp-emails.md).

### 7. Measure and govern

1. Read the Overview each morning; follow "Needs attention".
2. Use dashboards, funnels, cohorts, attribution, paths and customer value.
3. Handle privacy requests from the contact page; review the audit log.

Tutorials: [analytics](tutorials/10-analytics.md), [admin and privacy](tutorials/11-admin-and-privacy.md).

---

## What makes it different

- **Real time by design.** Segments are maintained as events arrive, not recomputed on a
  schedule. In load tests on a single machine, an event changed a segment in about 0.1 s at
  10,000 events per second, and the end-to-end tests require an email within 5 seconds of the
  triggering event.
- **Built for scale.** Designed for 10–100 million contacts per workspace; each stage of
  processing scales on its own, and segment and audience counts are exact set operations rather
  than database scans.
- **Self-hosted and multi-tenant.** One installation serves many brands or business units; data
  stays on the operator's infrastructure.
- **India-ready channels.** SMS with DLT template registration, WhatsApp and RCS with automatic SMS
  fallback, Indian providers (MSG91, Gupshup) built in, phone-first identity, quiet hours and time
  zones per contact, multi-language templates, and DPDP Act tools.
- **Any provider, no code.** The gateway wizard connects any SMS, WhatsApp, RCS, email or push API
  from its documented examples, including login steps and delivery reports.
- **Numbers you can trust and act on.** Every message has an outcome and a reason; every count of
  people opens those people.
- **Guardrails for big teams.** Roles per area, approvals, audit log, two-step sign-in, SSO, typed
  confirmations for destructive actions.

---

## How it compares

See [comparison.md](comparison.md).

---

## Honest limits at a glance

The most important limits for planning, collected from the sections above:

- Provider adapters and CRM / ad connectors are tested against simulated providers, not live
  accounts; confirm each provider before going live.
- Some providers (plain SMTP, Gmail, push) give no "delivered" status.
- Native mobile SDK user interfaces have not been verified on devices; iOS push goes through
  Firebase.
- Contacts don't leave a journey automatically when they leave its trigger segment.
- Recommendations, churn risk and best send time are rule-based, not machine-learned.
- AI help needs an Anthropic API key and is off without one; each workspace gets 200 requests a day.
- Heavy reports and exports are limited per workspace (for example 30 report runs and 5 contact
  exports a minute), and each report query stops after 60 seconds by default.
- Public sign-up is off by default; the operator creates workspaces.
- Running Pulse needs four data services (Kafka, Postgres, ClickHouse, Redis); very large
  installations need Redis sharding, which is not built yet (see the
  [architecture document](architecture.md#21-known-limits-and-trade-offs)).
