# Pulse tutorials

Step-by-step guides for using Pulse, written against the app as it is. Each one says where to click
(menu → page → button) and what you should see after each step.

| # | Tutorial | You will |
|---|---|---|
| 1 | [Getting started](01-getting-started.md) | Create a workspace, add teammates and roles, connect your website, see your first contacts |
| 2 | [Contacts](02-contacts.md) | Import a CSV, contact fields (including lists and choices), filters like `is:anonymous`, save a view as a segment, why a contact is in a segment |
| 3 | [Segments](03-segments.md) | Dynamic and static segments, the rule builder, Exclude groups, date ranges, daily refresh, View contacts |
| 4 | [Channels](04-channels.md) | Connect email, SMS, WhatsApp, RCS and push; preview, send test, the test inbox |
| 5 | [Templates and content](05-templates-and-content.md) | The editor, variables, landing pages, forms (multi-page, uploads), assets |
| 6 | [Your first journey](06-journeys.md) | A welcome series with a decision, a wait and a condition; delete or archive journeys |
| 7 | [Broadcasts](07-broadcasts.md) | Schedule a one-off or repeating campaign, with A/B testing, left-out segments and local-time sending |
| 8 | [On-site & in-app](08-on-site-and-in-app.md) | A website pop-up, dynamic web content, a mobile in-app message |
| 9 | [Integrations](09-integrations.md) | HubSpot or Salesforce, Shopify, Zapier, ad audiences |
| 10 | [Analytics](10-analytics.md) | Reports (with CSV export), funnels, cohorts, revenue |
| 11 | [Admin and privacy](11-admin-and-privacy.md) | Consent, retention, a person's data (download, delete), audit log export, single sign-on |
| 12 | [Events and their properties](12-events-and-properties.md) | The Events page, event schemas (types, allowed values, dependent properties), sending properties from a website or server, problems and Enforce, property filters in segments and journeys |
| 13 | [Personalising messages](13-personalising-messages.md) | Variables (`{{contact.first_name}}`, `{{event.product_name}}`), defaults and formats, the variable picker, missing values, the event that started a journey, preview and test with a real event, choosing the channel by rule (Indian number → SMS, otherwise WhatsApp) |

The examples use a local Pulse at `http://localhost:7080`. On your own server, use its address instead.

The screenshots come from a real Pulse and are retaken with `make docs-shots` (see
[docs/screenshots](../screenshots/README.md)). People who install or integrate Pulse: see the
[developer docs](../developer/README.md).
