# Phase 2: web app, sign-in, channels, on-site

Scope as agreed: user accounts plus SSO (Keycloak or any OIDC provider), a React + TypeScript + Vite app (segment builder,
journey canvas, template editors, contacts, dashboard), and the WebEngage/Mautic channels: SMS, WhatsApp with SMS fallback,
RCS with SMS fallback, FCM push, focus items and Dynamic Web Content (DWC). The JS SDK comes in the next slice.

## Build order (each layer works end to end before the next)

1. **Sign-in.** `users` and `sessions` tables, workspace signup, password login (argon2id), HttpOnly session cookie with a CSRF header check.
   API keys keep working for servers. Every `/v1` route accepts a session or an API key.
2. **SSO.** OIDC authorization code + PKCE, configured per workspace (issuer, client id/secret, auto-provisioning). Keycloak runs in the dev
   compose stack with a seeded realm, so the whole flow is tested locally. It works with any OIDC provider: Keycloak, Azure AD, Google, Okta.
3. **UI APIs.** List/update/delete for segments, journeys and templates. Contact search and an event timeline (from ClickHouse).
   Live segment size preview, dashboard stats, users, API key rotation, SSO settings.
4. **Channels.** Templates get a `channel` and channel-specific content. Each tenant configures providers, with secrets
   encrypted with AES-256-GCM. One dispatcher per channel topic (`messages.sms|whatsapp|rcs|push`).
   - **Fallback:** if WhatsApp or RCS can't reach the contact (provider says unreachable, the number isn't capable, or a
     failed delivery receipt arrives), the SMS fallback template is sent instead, exactly once.
   - **Journey nodes:** `send_sms`, `send_whatsapp`, `send_rcs` and `send_push`, the last two with optional `fallback_sms_template_id`.
   - **Sandbox provider:** records every message in a test inbox shown in the UI, so every channel and fallback path can be exercised
     without real accounts.
5. **Provider adapters:** Twilio and MSG91 (SMS), Meta WhatsApp Cloud API, Google RCS Business Messaging, FCM HTTP v1. Each is tested
   with `httptest` against the documented request and response shapes. They have **not** been checked against live accounts.
6. **On-site.** Focus items (modal, bar, slide-in; targeted by segment and URL; triggered on load, exit intent, scroll or delay;
   frequency capped) and DWC slots (default content plus per-segment variants). The public decision API `POST /v1/web/decide`
   uses a public site key. The JS SDK will call it to render content on customer websites.
7. **Web app.** Login (password + "Sign in with SSO"), app shell, dashboard, contacts, segment builder, journey canvas
   (React Flow), email builder (GrapesJS) plus SMS/WhatsApp/RCS/push template forms, focus item and DWC editors, channel
   provider settings, test inbox, team and SSO settings. It's built with Vite and embedded in the Go binary, so it's still one deployable.

## Not in this slice (Mautic/WebEngage features for later)

Forms and landing pages, points and stages, A/B split nodes, CRM/Zapier integrations, roles beyond admin/editor,
the JS/mobile SDKs (they will render focus items and DWC, register push tokens and track page views).

## How it's verified

Go unit tests for auth, crypto, fallback and every adapter. The e2e suite is extended with signup/login, SSO against
Keycloak, and WhatsApp→SMS and RCS→SMS fallback through the sandbox. Then a browser walkthrough of the app.
