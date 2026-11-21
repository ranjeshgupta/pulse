# How Pulse compares

Pulse is a self-hosted, open-source, real-time marketing automation and customer engagement
platform. This page sets it next to other open-source projects and SaaS platforms that people
evaluate for the same job, so you can see where it fits and where another product may suit you
better.

Everything said about other products comes from their own public websites, documentation or source
repositories, **checked in November 2026 (as of 2026-11)**, with a link for each claim under
[Sources](#sources). Where we could not find a public statement, the table says so rather than
guessing. Products change quickly, and many SaaS features depend on the plan or a paid add-on, so
check the vendor's current documentation before you decide.

What Pulse itself does is described in the [product overview](product.md), with its known limits
under [Honest limits at a glance](product.md#honest-limits-at-a-glance).

**Contents:** [Products compared](#products-compared) · [Capability table](#capability-table) ·
[Where others are ahead](#where-others-are-ahead) · [Product notes](#product-notes) ·
[Sources](#sources) · [Disclaimer](#disclaimer)

## Products compared

| Product | Kind | License / delivery | Release checked |
|---|---|---|---|
| **Pulse** | Open source, self-hosted | Go, one binary on Kafka, ClickHouse, Postgres and Redis | this repository |
| Mautic | Open source, self-hosted | GPL v3, PHP [M1] | 7.2.2, 2026-10-26 [M2] |
| Listmonk | Open source, self-hosted | AGPL-3.0, Go, Postgres [L1] | 6.2.0 stable; 7.0.0-beta.2 (2026-11-10) [L2] |
| Dittofeed | Open source, self-hosted | MIT, TypeScript; some features in a separate enterprise edition [D1] | 0.23.0 stable (2025-12-01) [D2] |
| Plunk | Open source, self-hosted or hosted | AGPL-3.0, TypeScript [P1] | 0.18.0, 2026-11-01 [P2] |
| WebEngage | SaaS | Hosted service | as of 2026-11 |
| MoEngage | SaaS | Hosted service | as of 2026-11 |
| CleverTap | SaaS | Hosted service | as of 2026-11 |
| Braze | SaaS | Hosted service | as of 2026-11 |

The open-source projects were chosen because they are actively maintained and cover email or
journeys; the SaaS platforms because they are widely used for customer engagement in India and
globally. Other open-source projects we looked at but left out: Laudspeaker (last release
2024-06-06) and Keila (a newsletter tool; its site lists automations as upcoming) [X1].

## Capability table

✅ documented by the vendor · 🟡 partial, through a plugin, partner or webhook, or limited to an
edition (see the note) · — not found in the vendor's public documentation (this does **not** mean
the product can't do it). Numbers in brackets point to [Sources](#sources).

| Capability | Pulse | Mautic | Listmonk | Dittofeed | Plunk | WebEngage | MoEngage | CleverTap | Braze |
|---|---|---|---|---|---|---|---|---|---|
| Run it on your own servers | ✅ | ✅ | ✅ | ✅ | ✅ | — | — | — ¹ | — |
| Hosted service by the vendor | — | 🟡 partner [M3] | 🟡 third parties [L1] | — closed 2026-10-31 [D3] | ✅ [P3] | ✅ | ✅ | ✅ | ✅ |
| Many workspaces / tenants in one account or install | ✅ | — ² | — | 🟡 enterprise edition [D4] | 🟡 projects [P3] | 🟡 ³ | ✅ [E2] | ✅ [C2] | ✅ [B2] |
| Segments update in real time as events arrive | ✅ | 🟡 rebuilt by cron [M4] | — | — | 🟡 background cadence [P4] | ✅ [W2] | 🟡 ⁴ [E3] | ✅ [C3] | ✅ [B3] |
| Event tracking API | ✅ | 🟡 tracking script, REST API [M5] | — | ✅ [D5] | ✅ [P5] | ✅ [W3] | ✅ [E4] | ✅ [C4] | ✅ [B4] |
| SDKs: web, Android, iOS, Flutter | 🟡 ⁵ | 🟡 web script [M5] | — | 🟡 web, Node, React Native [D5] | — REST only [P6] | ✅ [W3] | ✅ [E4] | ✅ [C4] | ✅ [B4] |
| Visual journey builder (waits, conditions, A/B split) | ✅ | 🟡 no split documented [M6] | — | 🟡 no A/B node [D6] | 🟡 ⁶ [P7] | ✅ [W4] | ✅ [E5] | ✅ [C5] | ✅ [B5] |
| Drag-and-drop email editor | ✅ | ✅ [M7] | ✅ [L1] | 🟡 low-code editor, MJML [D1] | ✅ [P8] | ✅ [W5] | ✅ [E6] | ✅ [C6] | ✅ [B6] |
| SMS | ✅ ⁷ | ✅ via a transport such as Twilio [M8] | 🟡 messenger webhook [L3] | ✅ Twilio [D7] | — | ✅ [W5] | ✅ [E7] | ✅ [C7] | ✅ [B7] |
| WhatsApp | ✅ ⁷ | — | 🟡 messenger webhook [L1] | 🟡 ⁸ | — | ✅ [W5] | ✅ [E7] | ✅ [C8] | ✅ [B8] |
| RCS | ✅ ⁷ | — | — | — | — | ✅ [W6] | ✅ [E7] | ✅ via Infobip [C9] | ✅ ⁹ [B7] |
| Web push | ✅ | ✅ via OneSignal [M9] | — | — | — | ✅ [W5] | ✅ [E7] | ✅ [C10] | ✅ [B9] |
| Mobile push | ✅ ⁷ | ✅ via OneSignal [M10] | 🟡 FCM via messenger webhook [L3] | 🟡 ⁸ | — | ✅ [W5] | ✅ [E7] | ✅ [C10] | ✅ [B9] |
| In-app messages and app inbox | ✅ | — | — | — listed as future [D7] | — | ✅ [W7] | ✅ [E7] | ✅ [C11] | ✅ [B10] |
| Landing pages and forms | ✅ | ✅ [M11] | 🟡 subscription form [L4] | — | — | 🟡 on-site surveys [W5] | ✅ [E8] | 🟡 pop-ups; pages via partners [C12] | ✅ [B11] |
| Lead scoring / points | ✅ | ✅ [M12] | — | — | — | — | — | — | 🟡 as a Canvas recipe [B12] |
| A/B testing | ✅ | ✅ email [M13] | — | 🟡 random-bucket segment [D5] | — | ✅ [W4] | ✅ [E9] | ✅ [C13] | ✅ [B5] |
| Funnels and cohort / retention analysis | ✅ | — [M14] | — | — | — | ✅ [W8] | ✅ [E10] | ✅ [C14] | ✅ per campaign [B13] |
| CRM sync (Salesforce, HubSpot) | ✅ ⁷ | ✅ plugins [M15] | — | 🟡 HubSpot [D5] | — | 🟡 ¹⁰ | ✅ Salesforce; HubSpot by webhook [E11] | 🟡 listed partners [C15] | 🟡 Salesforce recipe [B14] |
| Single sign-on | ✅ OIDC | ✅ SAML [M16] | ✅ OIDC [L5] | 🟡 OIDC, multi-tenant mode [D8] | — | ✅ SAML [W9] | ✅ SAML [E12] | ✅ SAML [C16] | ✅ SAML, some editions [B15] |
| Roles and permissions | ✅ custom roles | ✅ [M17] | ✅ [L6] | — | — | ✅ [W10] | ✅ [E13] | ✅ [C17] | ✅ [B2] |
| Facebook / Google ad audience sync | ✅ ⁷ | — | — | — | — | ✅ [W5] | ✅ [E14] | ✅ paid add-on [C18] | ✅ [B16] |

Notes:

1. CleverTap states it does not keep on-premise data centres; with its PII tokenization feature a
   dispatcher component can run in your own infrastructure [C1].
2. Mautic's "Projects" group items like folders; they are not separate tenants [M18].
3. WebEngage describes access control per project; we found no page that describes several projects
   per account [W10].
4. MoEngage evaluates segments over the last 30 days in real time for event-triggered campaigns;
   other segments are precomputed [E3].
5. Pulse has web, Flutter, Android (Kotlin) and iOS (Swift) SDKs. The Android SDK has been compiled
   but not run on a device, and the iOS SDK has not been compiled; iOS push goes through FCM.
6. Plunk documents delay, condition, wait-for-event and webhook workflow steps; whether the editor
   is a visual canvas, and an A/B split, are not documented [P7].
7. Pulse's adapters for SMS (Twilio, MSG91, Gupshup), WhatsApp (Meta, Gupshup), RCS (Google RBM,
   Gupshup), FCM push, HubSpot, Salesforce, Facebook and Google Ads are tested against mock APIs,
   not yet against live accounts. HubSpot companies and deals aren't synced.
8. Dittofeed's README lists WhatsApp and mobile push; its channel documentation does not list
   WhatsApp and describes mobile push as future [D1, D7].
9. Braze documents RCS as needing a specific contract, SKU and supported country [B7].
10. WebEngage's segmentation page lists Salesforce and HubSpot among data sources; a WebEngage support
    article says there is no out-of-the-box Salesforce integration, only the REST API and webhooks
    [W2, W11].

## Where others are ahead

Pulse is new. Being fair about what that means:

- **Proven in production.** Mautic is a long-running project with a community and plugin ecosystem;
  the SaaS platforms are run for you by their vendors. Pulse's
  provider adapters and CRM connectors have not yet been run against live accounts (note 7), and its
  native mobile SDKs have not been run on devices (note 5).
- **Lighter to run.** Listmonk needs only its binary and Postgres [L1]. Pulse needs Kafka, ClickHouse, Postgres and Redis, which pays off at 10 million
  contacts and up but is more to operate for a small list.
- **Managed hosting and data regions.** The SaaS platforms run the service for you and document data
  regions: WebEngage (US, India, Saudi Arabia) [W1], MoEngage (US, EU, India, Indonesia and others)
  [E1], CleverTap (EU, India, Singapore, US, Indonesia, UAE) [C1] and Braze (US, EU, Australia,
  Indonesia, Japan, South Korea) [B1]. Pulse runs wherever you run it, and nobody runs it for you.
- **Enterprise sign-in.** Mautic and the SaaS platforms document SAML; CleverTap and MoEngage also
  document SCIM or just-in-time provisioning [C16, E12]. Pulse supports OIDC only.
- **AI.** The SaaS platforms document broader AI features: MoEngage's Merlin AI [E15], CleverTap's send-time and channel optimisation and Scribe
  writer [C19], Braze's Intelligent Timing and BrazeAI [B17], WebEngage's send-time optimisation
  [W12]. Pulse has per-contact best send time and optional AI suggestions for content and audiences
  (with your own Anthropic API key).
- **Testing.** CleverTap and MoEngage document bandit or AI-driven variant distribution [C13, E9];
  Pulse picks a winner after a test window.

Where Pulse stands out: among the products compared here, it is the only open-source one whose
documented features cover email, SMS, WhatsApp, RCS, web and mobile push, in-app messages and an app
inbox, with segments that update in real time, in one installation that hosts many workspaces. As
self-hosted software it has no per-contact or per-message fee; you pay for your own servers and
messaging providers.

## Product notes

### Mautic

Open-source marketing automation in PHP, licensed GPL v3 [M1]. Strong for B2B: landing pages,
forms, points and point groups, Salesforce and HubSpot plugins, SAML sign-in and granular roles
[M11, M12, M15, M16, M17]. Segments are rebuilt by scheduled cron jobs [M4]; push goes through
OneSignal [M9, M10]. Managed hosting is offered through an official partner, and the project has
announced a process to choose its next hosting partner [M3, M19].

### Listmonk

A fast, self-hosted newsletter and mailing-list manager in Go with only Postgres as a dependency
[L1]. Segments are SQL expressions [L7]; it has a drag-and-drop email builder, OIDC sign-in and
per-list roles [L1, L5, L6]. SMS, WhatsApp and push are possible through external "messenger"
webhooks [L1, L3]. Journeys, event tracking and behavioural analytics are not part of its documented
scope.

### Dittofeed

An MIT-licensed customer engagement and journey tool in TypeScript [D1]. It has an identify / track
API, web, Node and React Native SDKs, journeys with delay, wait-for and segment-split nodes, and
email and SMS channels [D5, D6, D7]. Multi-tenant mode and OIDC sign-in are in its enterprise
edition [D4, D8]. Its website says Dittofeed Cloud closed on 31 October 2026 and the open-source
code remains available without ongoing support [D3].

### Plunk

An AGPL-licensed email platform in TypeScript, self-hosted with Docker or used as a hosted service
[P1, P3]. It has an event API, a drag-and-drop email editor, segments recomputed in the background
and event-triggered workflows [P4, P5, P7, P8]. It describes itself as an email platform; other
channels are not documented [P6].

### WebEngage

A customer engagement SaaS widely used in India. It documents real-time segments, journeys with
split, wait and condition blocks, email, SMS, WhatsApp, RCS, web and mobile push, in-app messages and
a notification inbox, funnels and cohorts, SAML SSO, custom roles and ad-audience sync [W2–W10].
Data is hosted in the US, India or Saudi Arabia [W1]. Prices are not published [W13].

### MoEngage

A customer engagement SaaS. It documents several workspaces per login, Flows with A/B splits of up to
five paths, all channels in the table, landing pages, Merlin AI, SAML with just-in-time provisioning,
and data centres in the US, EU, India, Indonesia and more [E1–E15]. Plan names are published without
prices [E16].

### CleverTap

A customer engagement SaaS. It documents projects, live segments, journeys, all channels in the
table (RCS through Infobip, with more providers in private beta), app inbox, cohorts and funnels,
SAML SSO with SCIM, and ad remarketing as a paid add-on [C1–C19]. Its entry plan is priced publicly
in India; higher plans are by quote [C20].

### Braze

A customer engagement SaaS. It documents workspaces, dynamic segments, Canvas journeys with
decision splits and experiment paths, all channels in the table (RCS under specific contracts),
landing pages, audience sync, SAML SSO on some editions, and AI features [B1–B17]. Its documented
data centres do not include India [B1]. Editions are listed without prices [B18].

## Sources

All pages accessed between 2026-11-20 and 2026-11-21 (as of 2026-11).

**Mautic**
- [M1] License: https://github.com/mautic/mautic/blob/7.x/LICENSE.txt
- [M2] Releases: https://github.com/mautic/mautic/releases
- [M3] Hosting: https://mautic.org/pricing
- [M4] Segments rebuilt by cron: https://docs.mautic.org/en/latest/segments/manage_segments.html,
  https://docs.mautic.org/en/latest/configuration/cron_jobs.html
- [M5] Tracking script: https://docs.mautic.org/en/latest/configuration/tracking_script.html
- [M6] Campaign builder: https://docs.mautic.org/en/latest/campaigns/campaign_builder.html
- [M7] Email and landing page builder: https://docs.mautic.org/en/latest/builders/email_landing_page.html
- [M8] SMS: https://docs.mautic.org/en/latest/channels/sms.html
- [M9] Web notifications: https://docs.mautic.org/en/latest/channels/web_notifications.html
- [M10] Mobile push: https://docs.mautic.org/en/latest/channels/push_notifications.html
- [M11] Landing pages and forms: https://docs.mautic.org/en/latest/components/landing_pages.html,
  https://docs.mautic.org/en/latest/components/forms.html
- [M12] Points: https://docs.mautic.org/en/latest/points/points.html
- [M13] Email A/B tests: https://docs.mautic.org/en/latest/channels/emails.html
- [M14] Reports: https://docs.mautic.org/en/latest/reports/reports.html
- [M15] Plugins: https://docs.mautic.org/en/latest/plugins/salesforce.html,
  https://docs.mautic.org/en/latest/plugins/hubspot.html
- [M16] Authentication (SAML): https://docs.mautic.org/en/latest/authentication/authentication.html
- [M17] Roles: https://docs.mautic.org/en/latest/users_roles/managing_roles.html
- [M18] Projects: https://docs.mautic.org/en/latest/projects/projects-overview.html
- [M19] Hosting partner RFP: https://mautic.org/blog/announcing-the-rfp-for-the-official-mautic-trial-managed-hosting-provider

**Listmonk**
- [L1] Website: https://listmonk.app/
- [L2] Releases: https://github.com/knadh/listmonk/releases
- [L3] Messengers: https://listmonk.app/docs/messengers/
- [L4] Lists API and subscription forms: https://listmonk.app/docs/apis/lists/
- [L5] OIDC: https://listmonk.app/docs/oidc/
- [L6] Roles and permissions: https://listmonk.app/docs/roles-and-permissions/
- [L7] Querying and segmentation: https://listmonk.app/docs/querying-and-segmentation/

**Dittofeed**
- [D1] Repository and README: https://github.com/dittofeed/dittofeed
- [D2] Releases: https://github.com/dittofeed/dittofeed/releases
- [D3] Website (cloud shutdown notice): https://www.dittofeed.com/
- [D4] Multi-tenant mode: https://docs.dittofeed.com/deployment/self-hosted/auth-modes/multi-tenant
- [D5] Documentation index (API, SDKs, destinations): https://docs.dittofeed.com/llms.txt
- [D6] Journeys: https://docs.dittofeed.com/resources/journeys
- [D7] Channels: https://docs.dittofeed.com/integrations/channels
- [D8] Auth modes: https://docs.dittofeed.com/deployment/self-hosted/auth-modes

**Plunk**
- [P1] Repository: https://github.com/useplunk/plunk
- [P2] Releases: https://github.com/useplunk/plunk/releases
- [P3] Billing and projects (hosted version): https://docs.useplunk.com/concepts/billing
- [P4] Segments: https://docs.useplunk.com/concepts/segments
- [P5] Track event API: https://docs.useplunk.com/api-reference/public-api/trackEvent
- [P6] Website: https://www.useplunk.com/
- [P7] Workflows: https://docs.useplunk.com/concepts/workflows
- [P8] Templates: https://docs.useplunk.com/concepts/templates

**WebEngage**
- [W1] Data centres (in an integration guide): https://docs.webengage.com/docs/branch
- [W2] Segmentation: https://webengage.com/segmentation/
- [W3] REST API and SDKs: https://docs.webengage.com/docs/rest-api-getting-started,
  https://docs.webengage.com/docs/flutter-push-notifications
- [W4] Journeys: https://knowledgebase.webengage.com/docs/journey-creation-concepts
- [W5] Channels: https://webengage.com/channels/
- [W6] RCS: https://docs.webengage.com/docs/rcs
- [W7] Notification inbox: https://docs.webengage.com/docs/copy-to-notification-inbox-flutter
- [W8] Analytics and cohorts: https://webengage.com/analytics/,
  https://knowledgebase.webengage.com/docs/analysing-cohorts
- [W9] Single sign-on: https://docs.webengage.com/docs/single-sign-on-integration
- [W10] Roles: https://knowledgebase.webengage.com/docs/role-based-access-control
- [W11] Salesforce support article: https://help.webengage.com/kb/does-webengage-offer-integration-with-salesforce/
- [W12] Send-time optimisation: https://knowledgebase.webengage.com/docs/send-intelligently
- [W13] Pricing: https://webengage.com/pricing/

**MoEngage**
- [E1] Data centres: https://www.moengage.com/docs/user-guide/data/key-concepts/data-centers-in-moengage
- [E2] Workspaces: https://help.moengage.com/hc/en-us/articles/210860483
- [E3] Segment types: https://www.moengage.com/docs/user-guide/segment/getting-started/types-of-segments
- [E4] Developer guide: https://www.moengage.com/docs/developer-guide/introduction
- [E5] Flows: https://www.moengage.com/docs/user-guide/flows-cross-channel-messaging/create/create-flows/create-a-flow
- [E6] Email campaigns: https://www.moengage.com/docs/user-guide/campaigns-and-channels/email/create/create-an-email-campaign
- [E7] Channels: https://www.moengage.com/docs/user-guide/campaigns-and-channels/getting-started/introduction/moengage-channels
- [E8] Landing pages: https://www.moengage.com/docs/user-guide/content/landing-pages/create/landing-page-templates
- [E9] A/B and multivariate testing: https://www.moengage.com/docs/user-guide/ai-and-intelligence/ab-testing/create-multivariate-testing/perform-user-distribution-dynamically-using-merlin-ai
- [E10] Dashboard overview (cohorts, funnels): https://www.moengage.com/docs/user-guide/getting-started/initial-setup/dashboard-overview
- [E11] CRM: https://www.moengage.com/docs/partner-guide/data-and-analytics/crm/salesforce-crm-native/setup-moengage-app-on-salesforce,
  https://www.moengage.com/docs/partner-guide/data-and-analytics/crm/hubspot
- [E12] SSO and just-in-time provisioning: https://www.moengage.com/docs/user-guide/settings/account/security/just-in-time-user-provisioning-jit
- [E13] Roles: https://www.moengage.com/docs/user-guide/settings/account/team-management/access-roles
- [E14] Facebook audiences: https://www.moengage.com/docs/user-guide/campaigns-and-channels/facebook-audience/create/create-a-facebook-audience-campaign
- [E15] Merlin AI: https://help.moengage.com/hc/en-us/articles/4411520913556
- [E16] Pricing: https://www.moengage.com/pricing/

**CleverTap**
- [C1] Regions, hosting and PII tokenization: https://developer.clevertap.com/docs/idc,
  https://trust.clevertap.com/controls, https://docs.clevertap.com/docs/pii-tokenization
- [C2] Projects: https://docs.clevertap.com/docs/project-setup
- [C3] Campaigns on live behaviour: https://docs.clevertap.com/docs/intro-to-campaigns
- [C4] API and Flutter SDK: https://developer.clevertap.com/docs/api-overview,
  https://developer.clevertap.com/docs/flutter-quick-start-guide
- [C5] Journeys: https://docs.clevertap.com/docs/construct-a-journey,
  https://docs.clevertap.com/docs/conditional-split-by-user-property
- [C6] Email editor: https://docs.clevertap.com/docs/email-editor-templates
- [C7] SMS: https://docs.clevertap.com/docs/liquid-tags
- [C8] WhatsApp: https://docs.clevertap.com/docs/features-and-add-ons
- [C9] RCS: https://docs.clevertap.com/docs/rcs-setup
- [C10] Web push: https://docs.clevertap.com/docs/create-message-web-push
- [C11] App inbox: https://developer.clevertap.com/docs/flutter-app-inbox
- [C12] Web pop-ups and landing-page partners: https://docs.clevertap.com/docs/web-pop-up-editor,
  https://docs.clevertap.com/docs/swipe-pages
- [C13] A/B and multivariate testing: https://docs.clevertap.com/docs/ab-multivariate-testing
- [C14] Cohorts and funnels: https://docs.clevertap.com/docs/cohorts-v2,
  https://docs.clevertap.com/docs/funnels-vs-flows
- [C15] CRM partners: https://docs.clevertap.com/docs/crm
- [C16] Single sign-on: https://docs.clevertap.com/docs/single-sign-on-sso
- [C17] Roles: https://docs.clevertap.com/docs/role-based-access-control
- [C18] Remarketing: https://docs.clevertap.com/docs/remarketing,
  https://docs.clevertap.com/docs/google-adwords-remarketing
- [C19] Best time and Scribe: https://docs.clevertap.com/v4.0/docs/best-time,
  https://docs.clevertap.com/docs/scribe
- [C20] Pricing: https://clevertap.com/pricing/

**Braze**
- [B1] Data centres: https://www.braze.com/docs/user_guide/data/infrastructure/data_centers/
- [B2] Users, workspaces and permissions: https://www.braze.com/docs/user_guide/administrative/manage_your_braze_users/user_permissions
- [B3] Segmentation filters: https://www.braze.com/docs/user_guide/engagement_tools/segments/segmentation_filters/
- [B4] SDK integration: https://www.braze.com/docs/developer_guide/sdk_integration
- [B5] Canvas components: https://www.braze.com/docs/user_guide/engagement_tools/canvas/canvas_components
- [B6] Drag-and-drop email: https://www.braze.com/docs/user_guide/message_building_by_channel/email/drag_and_drop/overview/
- [B7] SMS, MMS and RCS: https://www.braze.com/docs/user_guide/message_building_by_channel/sms_mms_rcs/rcs/rcs_setup/
- [B8] WhatsApp: https://www.braze.com/docs/user_guide/channels/whatsapp
- [B9] Push: https://www.braze.com/docs/developer_guide/push_notifications
- [B10] Messaging overview and Content Cards (inbox): https://www.braze.com/docs/user_guide/messaging,
  https://www.braze.com/docs/user_guide/message_building_by_channel/content_cards/
- [B11] Landing pages: https://www.braze.com/docs/user_guide/messaging/landing_pages/create_landing_pages
- [B12] Lead scoring use case: https://www.braze.com/docs/user_guide/get_started/b2b_use_cases/lead_scoring
- [B13] Retention reports: https://www.braze.com/docs/user_guide/data_and_analytics/reporting/retention_reports/
- [B14] Salesforce Sales Cloud recipe: https://www.braze.com/docs/user_guide/get_started/b2b_use_cases/b2b_salesforce_sales_cloud
- [B15] SAML SSO: https://www.braze.com/docs/user_guide/administer/global/saml_single_sign_on
- [B16] Audience Sync: https://www.braze.com/docs/partners/canvas_audience_sync/overview
- [B17] Intelligent Timing and BrazeAI: https://www.braze.com/docs/user_guide/brazeai/intelligence_suite/intelligent_timing,
  https://www.braze.com/docs/releases/2026/9_17_26
- [B18] Pricing: https://www.braze.com/pricing

**Other projects considered**
- [X1] https://github.com/laudspeaker/laudspeaker/releases, https://www.keila.io/

## Disclaimer

Mautic, Listmonk, Dittofeed, Plunk, WebEngage, MoEngage, CleverTap, Braze and all other product
names are trademarks of their respective owners. They are used here only to identify those products;
no affiliation with or endorsement by any of them is implied. The information about other products is
taken from their public websites, documentation and repositories as of November 2026, may be
incomplete or out of date, and is not a statement about what any product cannot do. If you spot an
error, please open an issue or pull request and we will correct it.
