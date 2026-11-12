# Pulse tutorials

Step-by-step guides for using Pulse, written against the app as it is. Each one says where to click
(menu → page → button) and what you should see after each step.

| # | Tutorial | You will |
|---|---|---|
| 1 | [Getting started](01-getting-started.md) | Create a workspace, add teammates and roles, find your way around (one Campaigns menu, the New campaign chooser), connect your website, see your first contacts |
| 2 | [Contacts](02-contacts.md) | Import a CSV, contact fields (including lists and choices), filters like `is:anonymous`, the full contact page (reachability, messages, events), save a view as a segment or create one from a list, why a contact is in a segment |
| 3 | [Segments](03-segments.md) | Dynamic and static segments, the rule builder, Exclude groups, date ranges, daily refresh, sizes that open their contacts, who each channel reaches, copy groups, duplicate |
| 4 | [Channels](04-channels.md) | Connect email (SMTP, Gmail, Google Workspace), SMS, WhatsApp, RCS (including Gupshup) and push; sending limits and daily quotas; saved test contacts; preview, send test, the test inbox; delivery webhooks with signatures; the web push prompt and install check |
| 5 | [Templates and content](05-templates-and-content.md) | The editor, variables, SMS DLT IDs, an AMP version, landing pages, forms (multi-page, uploads), assets |
| 6 | [Triggered and automation campaigns](06-triggered-and-automation-campaigns.md) | Build a welcome series in the step builder (start on an event, a form or joining a segment), with a decision, a wait and a condition; go live, watch a test contact, open the people behind each number, pause, archive or delete; send deadlines, copy and paste steps, duplicate |
| 7 | [One-time campaigns: all settings and results](07-one-time-campaigns.md) | Send to a segment with an A/B test, pick the winner, read the funnel and why messages didn't go out (every number opens its contacts), send again; local-time sending, a send deadline, a conversion goal, the exact audience; schedule and cancel a weekly campaign |
| 8 | [Website & app messages](08-website-and-app-messages.md) | A website slide-in and the order pop-ups show in, dynamic web content with variants by segment, page, device and contact field (with never-shown warnings and the overlap check), a mobile in-app message |
| 9 | [Integrations](09-integrations.md) | HubSpot or Salesforce (pull, push, push from a campaign), Shopify, Zapier, ad audiences, outbound webhooks |
| 10 | [Analytics](10-analytics.md) | The live Overview, reports (with CSV export), funnels, cohorts, revenue by campaign and UTM campaign |
| 11 | [Admin and privacy](11-admin-and-privacy.md) | Consent, tracking, Automation & limits (retention with a preview of what's deleted, retries, schedules, default provider limits), a person's data (download, delete), audit log export, single sign-on, roles |
| 12 | [Events and their properties](12-events-and-properties.md) | The Events page, event schemas (types, allowed values, dependent properties), sending properties from a website or server, problems and Enforce, property filters in segments and triggered campaigns |
| 13 | [Personalising messages](13-personalising-messages.md) | Variables (`{{contact.first_name}}`, `{{event.product_name}}`), defaults and formats, the variable picker, missing values, the event that started a triggered campaign, preview and test with a real event, choosing the channel by rule (Indian number → SMS, otherwise WhatsApp) |
| 14 | [The Campaigns page, New campaign and the wizard](14-campaigns-hub-and-one-time-campaigns.md) | Every campaign in one list by type (One-time, Triggered, Automation), status and tag, with linked numbers; New campaign by what you want to do; a one-time SMS written in the step-by-step wizard, with the exact audience and a send deadline; the two editors; old addresses; duplicate, archive, delete |
| 15 | [Delivery and dashboards](15-delivery-and-dashboards.md) | Read a campaign's funnel and why messages didn't go out, open the contacts behind any number, compare campaigns in Campaign performance, use the Users, Products and Live events dashboards |
| 16 | [Connecting any provider](16-connecting-any-provider.md) | Connect a provider Pulse has no ready-made connection for: describe its request, log in (API key, OAuth2 or a login request), check it with Test connection and Preview request, and read its delivery reports |
| 17 | [Interactive AMP emails](17-interactive-amp-emails.md) | Add a carousel, poll and form inside an email with blocks or code, check and preview it, which providers send AMP, Gmail registration, poll results and answers as events for segments and campaigns |
| 18 | [Finding the contacts behind a number](18-contacts-behind-a-number.md) | Open the people behind any count (segment sizes, campaign funnels, estimates, dashboards), read and remove the filter chips, keep a list as a static segment, and see which numbers aren't links and why |

The examples use a local Pulse at `http://localhost:7080`. On your own server, use its address instead.

The screenshots come from a real Pulse and are retaken with `make docs-shots` (see
[docs/screenshots](../screenshots/README.md)). People who install or integrate Pulse: see the
[developer docs](../developer/README.md).
