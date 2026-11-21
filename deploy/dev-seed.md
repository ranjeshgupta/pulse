# Local development accounts

Test values for the local Docker stack only. Never reuse them anywhere real.

| What | Where | Credentials |
|---|---|---|
| Demo workspace (created by `make seed`) | http://localhost:7080/login | workspace ID printed by the seed (`demo-retail`, or `demo-retail-xxxxxx` on later runs), `demo.admin@pulse.test` / `pulse-demo-pass-1` |
| Docs workspace "Acme Retail" (created by `make docs-seed`, used by `make docs-shots`) | http://localhost:7080/login | `acme-retail`, `docs.admin@pulse.test` / `pulse-docs-pass-1`. Teammates Priya Shah (editor), Dev Malhotra (viewer), Nina Kapoor (custom role "Analyst") have no password |
| Workspaces made by the tutorial 1 screenshot script (`make docs-shots SHOT=01`; one per run, named "Acme Retail") | | `acme-retail-xxxxxx`, `asha.rao+<run>@acme-retail.test` with a random throwaway password; not meant to be signed into again |
| Keycloak SSO test user (realm `pulse`) | http://localhost:8180 | `sso.tester` / `Pulse-SSO-test-1` |
| Keycloak admin console | http://localhost:8180/admin | `admin` / `admin` |
| Platform admin token (`POST /v1/tenants`) | `PULSE_ADMIN_TOKEN` in `deploy/docker-compose.yml` | `dev-admin-token` (refused unless `PULSE_PUBLIC_URL` is a local address) |

None of these accounts has two-step sign-in turned on, and the seeded workspaces' two-step sign-in policy is
**Optional**; keep it that way, since the screenshot scripts sign in with a password only.

To try SSO against the dev Keycloak, use these values in Settings → Sign-in & keys → Single sign-on (OIDC) in the demo workspace:

- Issuer URL: `http://localhost:8180/realms/pulse`
- Discovery URL: `http://keycloak:8180/realms/pulse`
- Client ID: `pulse-app`
- Client secret: `pulse-dev-client-secret`

## What the seed creates

`make seed` ("Demo Retail") and `make docs-seed` ("Acme Retail") run the same `cmd/seed`: sandbox
providers for SMS, WhatsApp, RCS and push; email, SMS, WhatsApp, RCS, push and app inbox templates;
dynamic segments (New sign-ups, Abandoned cart, Repeat buyers in Pune, Pro plan, VIP tag) and static
segments (VIP customers, Webinar attendees); triggered and automation campaigns (journeys) Welcome and Cart
recovery (active) and Win back (draft); one-time campaigns (broadcasts: one sent, one scheduled next week, one draft); a form, a landing page
(`/p/<workspace>/weekend-sale`), a focus item, web content, an in-app message; stages, companies,
three teammates and a custom role; a funnel, a report and a cohort; and 300 contacts with activity
spread over the last 30 days (`-contacts`, `-days`); the suggested event schemas with 60 days of shop
events; the personalised **Cart rescue** email, SMS and WhatsApp templates with an active **Cart
rescue** triggered campaign (cart_abandoned → SMS for Indian numbers, otherwise WhatsApp, then an email);
campaign tags, two one-time campaigns written in the wizard, saved test contacts, sending limits on the
SMS sandbox and a few unsubscribes and bounces; and a "Spring catalogue" email and a WhatsApp campaign
with delivery reports, opens, clicks, bounces, a complaint and failures, plus product and app screen views
for the dashboards.

`-if-exists <workspace ID>` makes it stop when that workspace already exists and the admin can sign in,
after adding (once each) the parts that later versions of the seed added: the event data, the Cart
rescue content, the campaign extras and the delivery data.
