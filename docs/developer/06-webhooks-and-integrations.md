# 6. Webhooks and integrations

How data leaves Pulse for your systems, and how other systems talk back to it:

- [Outbound webhooks](#outbound-webhooks): Pulse POSTs events to your URLs, signed.
- [Inbound provider webhooks](#inbound-provider-webhooks): delivery receipts, replies, bounces,
  complaints and deliveries from email, SMS, WhatsApp and RCS services, and the
  [reason codes](#reason-codes) Pulse gives every failure.
- [Zapier and Make (REST hooks)](#zapier-and-make-rest-hooks): triggers and actions with an
  integration key.
- [The custom HTTP gateway](#the-custom-http-gateway): send any channel through your own service,
  or call any provider's API with [your own request](#custom-request-mode) and
  [authentication](#custom-authentication).
- [Google and Gupshup providers](#google-and-gupshup-providers): Gmail, Google Workspace, the
  Workspace relay, and Gupshup SMS, WhatsApp and RCS.
- [CRM, Shopify and ad audiences](#crm-shopify-and-ad-audiences): what the built-in connectors do.

Outbound calls (webhooks, the gateway, connectors) go through an HTTP client that refuses private
and loopback addresses unless `PULSE_ALLOW_PRIVATE_WEBHOOKS=true` (development only; see
[Configuration](02-configuration.md#secrets-and-outbound-calls)). Requests that carry a
provider's credentials (custom authentication logins and token requests, the gateway, Gupshup)
never follow a redirect to another site. Signing secrets are stored encrypted with
`PULSE_SECRET_KEY`, which webhooks need.

---

## Outbound webhooks

An outbound webhook posts every event it subscribes to, as JSON, to one URL. Manage them in
**Settings → Webhooks** or with `/v1/settings/webhooks` ([API guide](03-api.md#webhooks)). Only
people with every admin permission can see and change them, since a webhook can copy every contact
out of the workspace.

![Settings, Webhooks, New webhook: name Purchases to CRM, URL https://crm.example.com/hooks/pulse, events purchase and segment_entered with suggestions below, Active ticked, and a Verify the signature panel with Node.js code](img/dev-06-webhook-new.png)

### Events

`events` is a list of event names, or `["*"]` for everything. Any event works, including ones you
haven't sent yet:

- events you send (`purchase`, `signup`, …) and the SDKs record (`page_view`, `identify`,
  `session_start`, `form_submitted`, `focus_clicked`, …);
- events Pulse records: messages (`email_sent`, `email_opened`, `email_clicked`, `sms_delivered`,
  `push_clicked`, …), consent (`unsubscribed`, `resubscribed`), `email_bounced`, `tag_added`,
  `points_changed`, `stage_changed`, `journey_entered`, `journey_completed` and others;
- `segment_entered` and `segment_exited`, from segment membership changes;
- `contact_created`, sent when a contact becomes known (a new identified contact, or an anonymous
  visitor who identifies). `*` doesn't include it: list it by name.

A new or changed webhook starts receiving within about 10 seconds (the `webhooks` role reloads its
list every 10 s). Untick **Active** (`"active": false`) to pause one without deleting it.

### Request

```http
POST /hooks/pulse HTTP/1.1
Host: crm.example.com
Content-Type: application/json
User-Agent: Pulse-Webhooks/1
X-Pulse-Event: purchase
X-Pulse-Delivery: 6f1c0d2a9b8e4f7a8c3d2e1f0a9b8c7d
X-Pulse-Signature: t=1794375162,v1=<64 hex characters>

{"id":"6f1c0d2a9b8e4f7a8c3d2e1f0a9b8c7d","event":"purchase","ts":"2026-11-11T10:00:00Z","workspace_id":7,
 "contact":{"id":42,"external_id":"cust-1001","attributes":{"email":"asha@example.com","city":"Pune","plan":"pro"}},
 "props":{"amount":499,"currency":"INR"}}
```

| Field | Meaning |
|---|---|
| `id` | The event's ID, also in `X-Pulse-Delivery`. The same on every retry: use it to skip duplicates |
| `event` | The event name, also in `X-Pulse-Event` |
| `ts` | When the event happened (UTC) |
| `workspace_id` | Pulse's numeric workspace ID |
| `contact.id`, `contact.external_id` | The contact (Pulse's ID and yours) |
| `contact.attributes` | The whole profile **after** this event |
| `props` | The event's properties (omitted when empty) |
| `segment_id` | `segment_entered` / `segment_exited` only |

Segment events carry only `contact.id` (no external ID or attributes) and an ID of the form
`seg:<segment>:<contact>:<time>:<entered>`; look the contact up with the API if you need more.
`contact_created` has the ID `created:<event id>`. The **Send test** button (or
`POST /v1/settings/webhooks/{id}/test`) sends a `webhook_test` event with a sample contact, signed
like a real one, and shows your server's answer. The attempt is also listed under **Deliveries**
(`"test": true` in the API), marked **test**, and is never retried.

### Verifying the signature

Each webhook has a signing secret (`whsec_…`), shown on its page (behind **Reveal**) and in
`GET /v1/settings/webhooks/{id}`. Every request carries:

```
X-Pulse-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(secret, "<t>." + raw body)>
```

The HMAC key is the secret string exactly as shown (including `whsec_`), and the message is the
timestamp, a dot, and the raw request body bytes. Check it on the **raw** body before parsing the
JSON (re-serialising changes the bytes), compare in constant time, and reject timestamps more than
5 minutes away from now. Each retry is signed again with its own `t`.

![A saved webhook: the signing secret masked with Reveal, Copy and Rotate secret buttons, and the Send a test panel](img/dev-06-webhook-secret.png)

**Node.js** (with Express, keep the raw body):

```js
const crypto = require("crypto");

// Returns true when the request really comes from Pulse.
// rawBody: the body exactly as received (a Buffer or string), before any JSON parsing.
// header: the X-Pulse-Signature header, "t=<unix seconds>,v1=<hex>".
function verifyPulseSignature(rawBody, header, secret, toleranceSeconds = 300) {
  const parts = {};
  for (const item of String(header || "").split(",")) {
    const [k, v] = item.trim().split("=", 2);
    parts[k] = v;
  }
  const t = Number(parts.t);
  if (!parts.v1 || !Number.isInteger(t)) return false;
  if (Math.abs(Date.now() / 1000 - t) > toleranceSeconds) return false;   // replayed or stale
  const expected = crypto.createHmac("sha256", secret)
    .update(`${parts.t}.`)
    .update(rawBody)
    .digest("hex");
  const a = Buffer.from(expected), b = Buffer.from(parts.v1);
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}

// app.post("/hooks/pulse", express.raw({ type: "application/json" }), (req, res) => {
//   if (!verifyPulseSignature(req.body, req.get("X-Pulse-Signature"), process.env.PULSE_WEBHOOK_SECRET)) {
//     return res.sendStatus(401);
//   }
//   const event = JSON.parse(req.body);
//   ...
//   res.sendStatus(204);
// });
```

**Python** (with Flask, `request.get_data()` is the raw body):

```python
import hashlib
import hmac
import time


def verify_pulse_signature(raw_body: bytes, header: str, secret: str, tolerance: int = 300) -> bool:
    """True when the request really comes from Pulse.

    raw_body: the request body bytes exactly as received.
    header: the X-Pulse-Signature header, "t=<unix seconds>,v1=<hex>".
    """
    parts = dict(item.strip().split("=", 1) for item in (header or "").split(",") if "=" in item)
    t, given = parts.get("t", ""), parts.get("v1", "")
    if not t.isdigit() or not given:
        return False
    if abs(time.time() - int(t)) > tolerance:  # replayed or stale
        return False
    expected = hmac.new(secret.encode(), t.encode() + b"." + raw_body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, given)

# @app.post("/hooks/pulse")
# def pulse_hook():
#     if not verify_pulse_signature(request.get_data(), request.headers.get("X-Pulse-Signature", ""), SECRET):
#         abort(401)
#     event = request.get_json()
#     ...
#     return "", 204
```

**Go**:

```go
import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// VerifyPulseSignature reports whether body (exactly as received) was signed by Pulse with secret.
// header is the X-Pulse-Signature value, "t=<unix seconds>,v1=<hex>".
func VerifyPulseSignature(body []byte, header, secret string, tolerance time.Duration) bool {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" || time.Since(time.Unix(t, 0)).Abs() > tolerance {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(sig))
}

// In a handler: body, _ := io.ReadAll(r.Body)
// if !VerifyPulseSignature(body, r.Header.Get("X-Pulse-Signature"), secret, 5*time.Minute) { 401 }
```

These three were checked against a signature computed the way `webhooks.Sign`
(`internal/webhooks/webhooks.go`) computes it: each accepts it, and rejects the body with one extra
byte. The same functions verify the [custom gateway](#the-custom-http-gateway)'s requests.

**Rotating the secret**: **Rotate secret** on the webhook's page (or `PUT` with
`"rotate_secret": true`) replaces it at once; deliveries from then on use the new one, including
retries of earlier events. Update your receiver right after.

### Retries and ordering

A delivery succeeds when your server answers **2xx within 10 seconds**. Anything else (a network
error, a timeout, any other status) is retried after **30 s, 2 min, 10 min, 1 h and 6 h**: six
attempts over about seven hours, then it's given up. Every attempt is recorded in the webhook's
**Deliveries** list (the latest 100, with status, error and time; `GET /v1/settings/webhooks/{id}`
→ `deliveries`, where `final` marks the last attempt of a delivery). Retries are kept in Redis and
continue across restarts; deleting or pausing the webhook stops them.

Deliveries happen in parallel (up to 32 at a time per worker) and in no guaranteed order across
events, and an event can arrive more than once (a retried batch, or a timeout after your server
already processed it). So:

- answer quickly (do the work in a queue) and with 2xx once you've stored the event;
- deduplicate by `X-Pulse-Delivery`;
- order by `ts`, not by arrival.

---

## Inbound provider webhooks

Pulse receives callbacks from the services it sends through. Each kind has its own URL; none needs
an API key: they are authenticated by the provider's signature or by a secret in the URL.

### Delivery receipts and replies (SMS, WhatsApp, RCS, gateway)

Each provider you add in **Settings → Channels** gets a webhook URL,
`{PULSE_PUBLIC_URL}/v1/webhooks/providers/{provider id}`, shown on the Channels page as "Delivery
reports" and in `GET /v1/channels/providers` (`webhook_url`). Enter it in the provider's console:

| Provider | What to configure | How Pulse checks it |
|---|---|---|
| Twilio (SMS) | The number's "A message comes in" webhook and the status callback (Pulse sends its URL with each message) | `X-Twilio-Signature`: HMAC-SHA1 with the auth token over the URL Twilio called plus the form fields. Behind a proxy, keep `Host` and set `X-Forwarded-Proto` so the URL matches |
| Meta WhatsApp Cloud API | The app's webhook URL, with the provider's **verify token**; subscribe to `messages` | `GET` verification with `hub.verify_token`; `X-Hub-Signature-256` (HMAC-SHA256 with the app secret) on every post |
| Google RCS Business Messaging | The agent's webhook URL, with the provider's **webhook client token** | The client-token handshake; HMAC-SHA512 signature on every post |
| Gupshup SMS, WhatsApp, RCS | The delivery report / callback URL, with `?token=<the provider's webhook token>` appended (see [Gupshup](#gupshup)) | The webhook token (`?token=` or an `X-Pulse-Token` header); reports without it are refused |
| Custom HTTP gateway | Your service posts receipts back (see [the gateway contract](#the-custom-http-gateway)) | Standard mode: `X-Pulse-Signature`, as above, with the gateway's signing secret. Custom request mode: the [receipt token](#receipts-in-custom-request-mode) |

What Pulse does with them:

- **Delivery receipts** (`delivered`, `read`, `failed`) become `<channel>_delivered`, `_read` and
  `_failed` events on the message's contact, for reports and journey message decisions. A WhatsApp
  or RCS message reported undeliverable falls back to its SMS template, if it has one. Failures
  carry a [reason code](#reason-codes) (from the provider's error code where it has one: Meta
  131026 is `blocked`, 131048/131049 and Twilio 30007 `spam_policy`, Gupshup 1002 `not_on_channel`…).
- **Inbound messages** from a known phone number become `<channel>_received` (`text`, `from`,
  `keyword`) on every contact with that number. Messages from unknown numbers are dropped.
- **Keywords**: a reply matching the workspace's opt-out words (STOP and the like) unsubscribes
  those contacts from that channel (`unsubscribed`, `source: "sms_keyword"`); an opt-in word (START…)
  resubscribes them. The lists are editable in the delivery rules (Settings → Channels,
  `PUT /v1/settings/delivery`).
- **RCS replies** (typed text or a tapped suggestion) are also recorded as `rcs_replied` on the
  latest RCS message the contact got in the last 7 days, so a journey can wait for "replied".

To try this without a provider, the sandbox simulates both: `POST /v1/sandbox/inbound`
(`{"channel":"sms","from":"+919800000001","text":"STOP"}`) and `POST /v1/sandbox/receipts`
(`{"provider_message_id": "…", "status": "delivered"}`; `status` is `delivered`, `failed`, `read`,
`complained`, or `bounced` with `"kind": "hard"` or `"soft"`; `reason` and `code` optional).

### Reason codes

Every `<channel>_failed`, `_suppressed` (skipped for consent) and `_capped` event, and every
`email_bounced` and `email_complained`, carries `props.reason_code` next to the raw
`props.reason` (the provider's or dispatcher's own words). Reports group by these codes, and the
Contacts list filters by them (`reason:<code>`).

| Code | Means |
|---|---|
| `invalid_number`, `invalid_email` | The phone number or address is invalid or the mail server rejected it |
| `no_address` | The contact has no email, valid phone or push device |
| `not_on_channel` | Not a WhatsApp user, RCS not supported, handset unreachable |
| `blocked` | WhatsApp: undeliverable (possibly blocked by the person) |
| `invalid_token` | Push: the device token or browser subscription is gone |
| `hard_bounce`, `soft_bounce`, `complaint` | Email bounced permanently, temporarily, or was marked as spam |
| `consent`, `bounced_before`, `capped`, `deadline`, `missing_value` | Skipped: opted out; bounced earlier; frequency cap; past the send deadline; a variable had no value |
| `template`, `no_provider` | Template missing, wrong channel, not approved or failed to render; no provider for the channel |
| `provider_auth`, `provider_rejected`, `spam_policy` | The provider refused Pulse's credentials; refused the message; held it back by spam or frequency policy |
| `rate_limited`, `timeout`, `provider_error` | The provider kept answering 429, didn't answer, or failed (5xx, network), after every retry |
| `unknown` | Recorded before reason codes existed, or not classifiable ("Not recorded") |

Codes are derived in `internal/reasons` from the adapters' error texts and the providers' own
codes; a receipt may carry one directly (`code` in a sandbox receipt, `reason_code` in a gateway
receipt).

### Bounces, complaints and deliveries (email)

**Settings → Channels → Bounces and complaints** (or `GET /v1/settings/bounces`) shows the
workspace's secret bounce URL, `{PULSE_PUBLIC_URL}/v1/bounces/{token}`. Point your email service's
event webhook at it. The body format is detected:

| Service | Configure | Verified with (once saved) |
|---|---|---|
| SendGrid | Event Webhook: delivered, bounce, dropped, spam report; turn on the signed event webhook | The verification key SendGrid shows (ECDSA over timestamp + body, ±24 h) |
| Postmark | Bounce, spam complaint and delivery webhooks; put a user and password in the URL | HTTP basic auth |
| Mailgun | Webhooks for delivered, permanent and temporary failures and complaints (v3 `event-data`) | The HTTP webhook signing key (HMAC of timestamp + token, ±24 h; each token accepted once) |
| Amazon SES | Notifications (bounce, complaint, delivery) through an SNS topic with an HTTPS subscription to the URL; Pulse confirms the subscription itself | Always: the SNS message signature (v1 or v2), with the certificate from an `sns.<region>.amazonaws.com` host (SES events posted without SNS: basic auth) |
| SparkPost | Event webhook: delivery, bounce, out-of-band bounce, spam complaint; basic auth credentials on the webhook | HTTP basic auth |
| Anything else | Generic JSON: `{"email": "…", "type": "hard", "reason": "…", "ref": "…"}` (`type`: `hard`, `soft`, `complaint` or `delivered`), or a list of them | HTTP basic auth |

```bash
curl -s -X POST "$PULSE/v1/bounces/$BOUNCE_TOKEN" -u pulse:$BASIC_PASSWORD -H "Content-Type: application/json" \
  -d '[{"email":"asha@example.com","type":"hard","reason":"550 mailbox unavailable"}]'
```

**Verification.** The secret URL picks the workspace. Save keys with
`PUT /v1/settings/bounces/signing` (or on the settings page): the SendGrid verification key, the
Mailgun signing key, and a basic auth user and password (at least 12 characters) for the formats
without a signature of their own. Once a format's key is saved, its bodies that don't pass are
refused; and **once any key is saved**, every body must pass a saved check, so a format with no
check of its own needs basic auth or is refused (the settings page shows it as "Refused"). With no
key saved, the URL alone is accepted ("URL secret only"). Refused bodies get 401.

**Message references.** Every email with tracking on carries a signed reference to its message
(workspace, contact, template, campaign, run, variant: ids only, plus an HMAC), as the
`Message-ID`'s local part, in `X-Pulse-Ref`, and in the sending service's own metadata header,
which its webhooks echo back. The SMTP provider's **Sending service** setting chooses that header
(auto-detected from the host: `smtp.sendgrid.net`, `*.mailgun.org`, `*.postmarkapp.com`,
`email-smtp.*.amazonaws.com`, `*.sparkpostmail.com`):

| Service | Header |
|---|---|
| SendGrid | `X-SMTPAPI: {"unique_args":{"pulse_ref":"…"}}` |
| Mailgun | `X-Mailgun-Variables: {"pulse_ref":"…"}` |
| Postmark | `X-PM-Metadata-pulse_ref` (split into `pulse_ref2` past 80 characters) |
| Amazon SES | `X-SES-MESSAGE-TAGS: pulse_ref=…` |
| SparkPost | `X-MSYS-API: {"metadata":{"pulse_ref":"…"}}` |

A report whose reference was signed by this server for this workspace is linked to the message,
its campaign and template (so it shows in that campaign's report); otherwise it's recorded on the
contact with that address, unlinked. Linked events take their IDs from the message, so a repeated
webhook records once. Deliveries without a reference are dropped (they would count nowhere).
Bounces from before references existed stay unlinked.

What Pulse records:

- A **hard** bounce records `email_bounced` and sets `email_bounced: true` (only while the contact
  still has the bounced address), which stops email to the address until your server clears it.
- A **soft** bounce is only recorded.
- A **complaint** records `email_complained` and unsubscribes the contact from email
  (`source: "complaint"`).
- A **delivery** records `email_delivered`. Plain SMTP relays (and Gmail, the Workspace relay, FCM
  and web push) never report deliveries: reports show "—" ("not reported by this provider") for
  them.

Addresses that match no contact are ignored. Keep the URL private.

If your mail goes through a server that sends delivery status notifications (RFC 3464) and ARF
complaints (RFC 5965) to a mailbox instead, add it under **Settings → Channels → Bounce mailbox**
(IMAP; the `integrations` role reads it every 5 minutes, and needs `PULSE_SECRET_KEY`). They are
linked from the quoted original's `X-Pulse-Ref` or `Message-ID`.

### Email replies

To let journeys react to replies ("Replies to email"), set a reply tracking address in the delivery
rules (`reply_to`, e.g. `replies@inbound.example.com`). Marketing emails then get
`Reply-To: replies+r<code>@inbound.example.com`. Route that domain's mail to your email service's
inbound parse (SendGrid Inbound Parse, Mailgun routes, Postmark inbound) and point its webhook at
`{PULSE_PUBLIC_URL}/v1/replies/{token}` (the same token as the bounce URL; shown as the reply URL).
Pulse finds the code among the recipients and records `email_replied` on the contact who got that
message. Generic JSON is also accepted: `{"to": "…", "from": "…", "subject": "…", "text": "…"}`.

---

## Zapier and Make (REST hooks)

Pulse speaks the REST hooks pattern that Zapier and Make use, with its own key per connector so
you never hand out the workspace API key. There is no published Zapier or Make app yet: use their
"Webhooks" / "HTTP" modules with these endpoints.

1. In **Settings → Integrations**, add **Zapier / Make** (or `POST /v1/integrations` with
   `{"kind":"rest_hooks","name":"Zapier"}`). Its API key (`pzk_…`) is shown once; replace it with
   `POST /v1/integrations/{id}/key`.
2. Call `/v1/hooks/*` with `Authorization: Bearer pzk_…` (or `X-Pulse-Key: pzk_…`).

| Call | Does |
|---|---|
| `GET /v1/hooks/me` | Connection test: `{"workspace","integration","integration_id"}` |
| `GET /v1/hooks/segments` | Segments, for a dropdown |
| `GET /v1/hooks/samples?event=purchase` | A sample payload, for mapping fields |
| `POST /v1/hooks/subscriptions` | Subscribe: `{"target_url" (or Zapier's "hookUrl"), "event", "segment_id"?, "form_id"?}` → 201 `{"id","event","target_url","secret"}` |
| `DELETE /v1/hooks/subscriptions/{id}` | Unsubscribe (204) |
| `POST /v1/hooks/contacts` | Create or update a contact: `{"external_id"? or "email", "phone"?, "attrs"?}` → 202. Reserved attributes are left out and listed in `ignored_attrs` |
| `POST /v1/hooks/events` | Track an event: `{"external_id"? or "email", "name", "props"?}` → 202 |
| `POST /v1/hooks/tags` | Add tags, or remove them with `"remove": true`: `{"external_id"? or "email", "tags": [...]}` → 202 |

```bash
ZAP=pzk_…   # the connector's key
curl -s -X POST $PULSE/v1/hooks/subscriptions -H "Authorization: Bearer $ZAP" -H "Content-Type: application/json" \
  -d '{"hookUrl":"https://hooks.zapier.com/hooks/catch/123/abc/","event":"segment_entered","segment_id":14}'
curl -s -X POST $PULSE/v1/hooks/contacts -H "Authorization: Bearer $ZAP" -H "Content-Type: application/json" \
  -d '{"email":"lead@example.com","attrs":{"plan":"trial","source":"typeform"}}'
# 202 {"event_id":"…","external_id":"lead@example.com","ignored_attrs":[]}
```

Triggers: `contact_created`, `segment_entered` / `segment_exited` (optionally one segment),
`form_submitted` (optionally one form), or any event name. Deliveries are outbound webhooks owned by
the connector: the same payload, signature (with the subscription's `secret`), retries and delivery
log (on the connector's page). A **410 Gone** answer removes the subscription, as REST hooks expect.
At most 100 subscriptions per connector. Actions find the contact by `external_id`, else by email,
else create one named by the email.

---

## The custom HTTP gateway

When Pulse has no adapter for your provider (a regional SMS aggregator, an in-house notification
service), add a **Custom HTTP gateway** provider to any channel (email, SMS, WhatsApp, RCS, push).
It works in two modes, chosen by **How Pulse calls your gateway** (`config.mode`):

- **Pulse's standard request (signed JSON)** (`standard`, the default): Pulse POSTs each rendered
  message to your endpoint in the format below, signed, and your service delivers it.
- **My own request (method, address, headers, body)** (`custom`): you design the request, so Pulse
  can call a provider's existing API directly ([custom request mode](#custom-request-mode)).

Both modes can add [custom authentication](#custom-authentication) (basic, bearer, API key, OAuth2
or a login flow).

![Settings, Channels, Add SMS provider: Custom HTTP gateway selected, Name in Pulse, How Pulse calls your gateway set to Pulse's standard request (signed JSON), Endpoint URL https://sms-gateway.example.com/pulse, Authentication type None, a masked signing secret, the Test connection and Preview request buttons, and Use for all SMS messages](img/dev-06-gateway.png)

```bash
curl -s -X POST $PULSE/v1/channels/providers -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d '{
  "channel": "sms", "provider": "http_sms", "name": "Our SMS gateway", "is_default": true,
  "config": {"url": "https://sms-gateway.example.com/pulse"},
  "secrets": {"signing_secret": "<a long random string you also give your gateway>"}}'
```

The provider IDs are `http_email`, `http_sms`, `http_whatsapp`, `http_rcs` and `http_push`. The
signing secret is yours to choose (`openssl rand -hex 32`); Pulse stores it encrypted and never shows
it again.

### What Pulse sends

```http
POST /pulse HTTP/1.1
Host: sms-gateway.example.com
Content-Type: application/json
X-Pulse-Signature: t=1794375162,v1=<hex HMAC-SHA256(signing secret, "<t>." + body)>
Idempotency-Key: 1f6c…-welcome

{"message_id":"1f6c…-welcome","channel":"sms","to":"+919811122233",
 "content":{"text":"Hi Ravi, your cart is waiting: https://pulse.example.com/t/s/Xy12ab"}}
```

| Field | Channels | Meaning |
|---|---|---|
| `message_id` | all | Pulse's message ID, also in `Idempotency-Key`. A retry sends the same ID: deliver it once |
| `channel` | all | `email`, `sms`, `whatsapp`, `rcs` or `push` |
| `to` | all | Email address, E.164 phone number, or push token |
| `platform` | push | `android`, `ios` or `web` |
| `subject`, `html`, `text` | email | The rendered email; `text` is the plain-text part |
| `ref` | email | The message reference; pass it on as an `X-Pulse-Ref` header (and the sending service's metadata) so bounces and deliveries can be [linked](#bounces-complaints-and-deliveries-email) |
| `headers` | email | Extra headers to set, e.g. `List-Unsubscribe` and `List-Unsubscribe-Post` |
| `attachments` | email | `[{"filename","content_type","size","url"}]`: signed download links valid 7 days (needs `PULSE_SECRET_KEY`) |
| `content` | all | The rendered channel content (`channel.Content`): `text`, `sender_id`, `dlt_template_id`, `dlt_entity_id` (SMS); `template_name`, `language`, `body_params` (WhatsApp); `text`, `card`, `suggestions` (RCS); `title`, `body`, `image_url`, `url`, `data` (push; `data.pulse_track` is the click tracking URL); `amp` (email: the AMP part, personalised, when the template has one; your service must add it to the email as `text/x-amp-html`) |

Verify `X-Pulse-Signature` with the functions [above](#verifying-the-signature) and the gateway's
signing secret.

### What your gateway answers

Pulse waits up to 15 seconds.

| Your answer | Pulse records |
|---|---|
| `2xx`, optionally `{"id": "your-message-id"}` | Sent (`<channel>_sent`). Keep `id` to match the receipts you post later |
| `404` or `410` | The recipient can't be reached on this channel: WhatsApp and RCS fall back to SMS when the message has an SMS template; an SMS number is marked `sms_bounced`; a push token is deleted |
| other `4xx` | Permanent failure (`<channel>_failed`), not retried |
| `429` or `5xx`, a timeout or a network error | Temporary failure: the dispatcher tries again, see the note below |

**Note on temporary failures.** The `dispatch` role tries a message that failed temporarily again
after 30 s, 1 min, 2 min, 5 min, 10 min, 30 min and 1 h (8 tries, about 1 h 50 min in all), without
holding up other messages. Each workspace can change the number of tries (1-20) and the longest
wait (1 minute to a day) in **Settings → Automation & limits**: fewer tries cut the schedule, a
shorter longest wait caps it, more tries keep doubling the last wait. Send `Retry-After` (seconds or
an HTTP date) with a `429` or `503` to ask for a longer wait (up to that longest wait). If every try
fails, Pulse records `<channel>_failed` with `reason: "gave up after 8 tries: …"`. Every try
carries the same `message_id`: deliver it once.

### Receipts and replies back to Pulse

Post delivery receipts and inbound messages to the provider's webhook URL
(`{PULSE_PUBLIC_URL}/v1/webhooks/providers/{provider id}`), signed the same way with the same secret
(Pulse rejects a signature older than 5 minutes):

```json
{"id": "your-message-id", "status": "delivered"}
{"id": "your-message-id", "status": "failed", "unreachable": true, "reason": "not on WhatsApp", "reason_code": "not_on_channel"}
{"id": "your-message-id", "status": "bounced", "kind": "hard", "reason": "550 mailbox unavailable"}
{"id": "your-message-id", "status": "complained"}
{"status": "inbound", "id": "your-inbound-id", "from": "+919811122233", "text": "STOP"}
```

`status` is `delivered`, `failed`, `read`, `bounced` (email; `kind` `hard` or `soft`, `hard` when
missing), `complained` (email) or `inbound`; `unreachable: true` on a failure triggers the
SMS fallback for WhatsApp and RCS (and marks an SMS number bounced). `reason_code` is optional
([reason codes](#reason-codes)); without it Pulse derives one from `reason`. Receipts are matched to messages
sent in the last 3 days; later or unknown IDs are ignored. Inbound messages are handled like any provider's
([above](#delivery-receipts-and-replies-sms-whatsapp-rcs-gateway)). Signing in Node.js:

```js
const body = JSON.stringify({ id: "your-message-id", status: "delivered" });
const t = Math.floor(Date.now() / 1000);
const sig = crypto.createHmac("sha256", secret).update(`${t}.${body}`).digest("hex");
await fetch(webhookUrl, {
  method: "POST",
  headers: { "Content-Type": "application/json", "X-Pulse-Signature": `t=${t},v1=${sig}` },
  body,
});
```

Pulse answers 200 when accepted, 401 for a bad signature, 400 for a body it can't read.

### Custom request mode

In custom mode the provider's `config.request` (JSON) describes the request; the app's form writes
it for you.

```json
{
  "method": "POST",
  "url": "https://api.sms.example/v2/messages?route=promo",
  "headers": [{"name": "X-Account", "value": "acme"}],
  "content_type": "application/json",
  "body": "{\"to\": \"{{message.to}}\", \"text\": \"{{message.text}}\", \"customer\": \"{{contact.external_id}}\"}",
  "success": {"status_min": 200, "status_max": 299, "json_path": "status", "json_value": "queued"},
  "id_path": "data.message_id",
  "receipt": {"items_path": "events", "id_path": "message_id", "status_path": "state",
              "statuses": {"DELIVRD": "delivered", "READ": "read", "UNDELIV": "undeliverable", "FAILED": "failed"},
              "reason_path": "error.text", "code_path": "error.code",
              "verify": "header", "verify_name": "X-Receipt-Token"}
}
```

- **Method** `GET`, `POST`, `PUT` or `PATCH`; **content type** JSON (the default), a form
  (`application/x-www-form-urlencoded`) or anything else as plain text.
- **Variables**: the standard ones (`{{contact.x}}`, `{{event.x}}`, `{{campaign.x}}`) and
  `{{message.x}}`, the rendered message: `id`, `channel`, `to`, `from`, `platform`, `subject`,
  `html`, `text`, `title`, `body`, `image_url`, `url`, `sender_id`, `dlt_template_id`,
  `dlt_entity_id`, `template_name`, `language`, `params`, `header_image_url`, `suggestions`, `card`,
  `data`, `ref`, `amp`. (`message.x` is empty in message templates.)
- **Escaping**: every value is escaped for where it lands: JSON-escaped in a JSON body
  (`| json` inserts a whole value, e.g. `{{message.params | json}}`), percent-encoded in a form
  body and in query parameters. A header value containing a line break refuses the send.
- **Fixed destination**: the URL's scheme and host must be written out
  (`https://api.sms.example/…`); variables are allowed only in its path and query, so a contact's
  data can never choose where the request goes. Private and loopback addresses are refused unless
  `PULSE_ALLOW_PRIVATE_WEBHOOKS` is set.
- **Success rule**: the status within `status_min`-`status_max` (default 200-299) and, with
  `json_path`, the answer's value there equal to `json_value` (or, with no value, present and not
  `false` or empty). `id_path` reads the provider's message ID from a JSON answer, to match receipts.
- **Answers**: 404 and 410 mean unreachable (fallback to SMS), 429 and 5xx are retried, any other
  4xx and a 2xx that fails the success rule are permanent failures.

Use **Preview request** in the form (`POST /v1/channels/providers/preview`) to see the exact
request, with secrets masked, for the test message or a template rendered for a contact.

### Receipts in custom request mode

The provider's delivery webhook posts to the same provider webhook URL. `receipt` maps it: an
object, or a list of them at `items_path` (or the body itself when it's an array); `id_path` and
`status_path` find the message ID and status; `statuses` maps the provider's values (any case) to
`delivered`, `read`, `failed`, `undeliverable` (failed, and the SMS fallback runs) or `bounced`;
values not listed (such as "queued") are ignored. `reason_path` and `code_path` add the reason.

Receipts **fail closed**: they must carry the provider's **receipt token** (a secret you choose),
or every receipt is refused. `verify` says where:

| `verify` | The token is |
|---|---|
| `header` | The value of the header `verify_name` |
| `query` | The value of the query parameter `verify_name` (register the URL with `?<name>=<token>`) |
| `hmac` | The key of an HMAC-SHA256 of the raw body, sent in the header `verify_name` as hex or base64 (a `sha256=` prefix is allowed) |

### Custom authentication

The gateway (both modes) takes authentication in `config.auth` (JSON), with its secrets among the
provider's sealed secrets:

| `type` | Settings | Secrets |
|---|---|---|
| `none` | | |
| `basic` | | `auth_username`, `auth_password` |
| `bearer` | `inject` (optional) | `auth_token` |
| `api_key` | `key_in` (`header` or `query`), `key_name` | `auth_api_key` |
| `oauth2` | `token_url`, `client_id`, `scope`, `audience`, `grant` (`client_credentials`, or `refresh_token`), `client_auth` (`body` or `basic`), `inject` | `auth_client_secret` (and `auth_refresh_token`) |
| `login` | `login`: the login request and how to read its answer; `inject` | any `{{secrets.x}}` the login names |

A **login flow** is for APIs that hand out a token from a login call:

```json
{"type": "login",
 "login": {"method": "POST", "url": "https://api.sms.example/auth/login",
           "content_type": "application/json",
           "body": "{\"user\": \"{{secrets.username}}\", \"password\": \"{{secrets.password}}\"}",
           "token_from": "json", "token_path": "data.token",
           "expiry_from": "json", "expiry_path": "data.expires_in"},
 "inject": {"in": "header", "name": "Authorization", "template": "Bearer {{token}}"}}
```

- `{{secrets.x}}` names a secret (each gets its own masked field in the form). In a JSON body it's
  JSON-escaped; in a form body or the URL, percent-encoded. A secret is never allowed in the login
  or token URL's scheme, user info, host or port: only its path and query.
- The token is read with `token_from`: `json` (a dotted path: `data.token`, `items[0].t` or
  `items.0.t`), `header` (a response header; a leading `Bearer ` is dropped) or `regex` (an
  expression on the body: its first group, or the whole match).
- Its lifetime: `expiry_from: "json"` with `expiry_path` (seconds left, a Unix time in seconds or
  milliseconds, or an RFC 3339 time), `fixed` with `expiry_seconds` (10 seconds to 30 days), or
  empty (an hour, renewed sooner on a 401). Login and token URLs must be `https` (and public)
  unless `PULSE_ALLOW_PRIVATE_WEBHOOKS` is set.
- `inject` puts the token into each request: a header (`name`, default `Authorization`;
  `template`, default `Bearer {{token}}`), a query parameter, or a top-level field of a JSON object
  or form body (`in: "body"`).

Tokens are cached per provider in Redis (sealed with the server key), keyed by a hash of the
settings and secrets, so a changed password never reuses an old token. A token is renewed shortly
before it expires (a tenth of its lifetime, at most a minute early) by one instance at a time
(the others wait for its result); a `401` on a send drops the token and retries once with a new
one. A login or token endpoint answering 4xx (other than 429), or an answer without a token where
the settings say, is a permanent failure (`provider_auth`); 5xx and network errors are retried
like any send. **Test connection** (`POST /v1/channels/providers/check-auth`) runs the login now and
shows the token masked with its expiry; it never uses or fills the cache. For an SMTP provider
(Gmail with an app password, Google's relay, any SMTP server) it opens a new connection (never a
pooled one), says EHLO, starts TLS, logs in and quits: `result.server` names the server and
`result.logged_in` says whether it logged in; a `530`/`534`/`535` reply means the credentials were refused.

Changing any of these settings (or the URLs, the mode, the request template, the receipt mapping)
needs every secret entered again, and pointing the provider at another host clears the saved
secrets ([API guide](03-api.md#channels-and-providers)).

---

## Google and Gupshup providers

### Gmail, Google Workspace and the Workspace relay

| Provider | Host | Sign-in | Limits a new provider starts with |
|---|---|---|---|
| Gmail (SMTP) `gmail_smtp` | `smtp.gmail.com`, port 587 (STARTTLS) or 465 (TLS) | An app password, or a Google OAuth client | 500 a day, 1 a second |
| Google Workspace (SMTP) `gworkspace_smtp` | the same | the same | 2,000 a day, 2 a second |
| Google Workspace SMTP relay `gworkspace_relay` | `smtp-relay.gmail.com`, port 587, 465 or 25 | Your server's IP address, or SMTP auth | 10,000 a day |

These accounts are for low volume: send bulk mail through the relay or a sending service. None of
them reports deliveries.

- **App password**: 16 characters from the Google account's security page (it needs 2-step
  verification); spaces are removed.
- **OAuth client (XOAUTH2)**: in Google Cloud, create an OAuth client, authorise it once for the
  scope `https://mail.google.com/` with the sending account, and keep the **refresh token**. Enter
  the client ID, client secret and refresh token; Pulse exchanges the refresh token for access
  tokens at Google's token endpoint (cached per provider, like custom authentication) and signs in
  with XOAUTH2, only over TLS.
- **Relay**: in the Google Admin console (Apps → Google Workspace → Gmail → Routing → SMTP relay
  service), allow your server's IP addresses (or require SMTP authentication), and allow sending
  from your domain's addresses. Enter **Your domain (sent in EHLO)**: the relay identifies you by it.

### Gupshup

`gupshup_sms` (Enterprise SMS gateway), `gupshup_whatsapp` and `gupshup_rcs`. They were built
from Gupshup's public documentation and tested against mock servers only: every API URL is a
setting, and the endpoints should be confirmed with a real Gupshup account.

- **SMS** goes through the Enterprise gateway (`GatewayAPI/rest`, `method=SendMessage`) with your
  user ID and password, the sender ID (DLT header) as `mask`, and India's DLT IDs: the template's
  `dlt_template_id` / `dlt_entity_id`, or the provider's defaults. Unicode text is detected.
- **WhatsApp** sends approved templates (the template's name is Gupshup's template ID, with body
  parameters and a header image) or session text, with the app's API key; delivery events (v2:
  delivered, read, failed) are matched on Gupshup's message ID, and replies feed STOP keywords.
- **RCS** sends text, a rich card and suggestions; "RCS not supported" answers fall back to SMS.
  Its delivery report and inbound formats are inferred, not documented by Gupshup.
- **Webhooks**: every Gupshup provider needs a **Webhook token** (any long random string). Register
  the provider's Pulse webhook URL at Gupshup with `?token=<the token>` appended (or send it as an
  `X-Pulse-Token` header). SMS delivery reports may come by GET or POST. Reports and replies
  without the token are refused.
- Error codes become reason codes (unknown subscriber → `invalid_number` and the number marked
  bounced, DND → `consent`, template mismatch → `template`, WhatsApp 1002 → `not_on_channel` and
  the SMS fallback…).

### AMP email by provider

An email template's AMP part goes out as `text/x-amp-html` between the plain-text and HTML parts
with every SMTP relay (SES, SendGrid, SparkPost, Mailgun, Gmail and Workspace, plain relays)
**except Postmark**, which doesn't support AMP: there the AMP part is left out and the HTML is sent.
The custom gateway gets it as `content.amp` (standard mode) or `{{message.amp}}` (custom mode) and
must add it itself. The editor shows this per provider (`POST /v1/templates/amp/check` →
`providers`). Gmail shows AMP only from senders Google has registered.

---

## CRM, Shopify and ad audiences

Built-in connectors live in **Settings → Integrations** (`/v1/integrations`, settings edit access).
Their credentials are sealed with `PULSE_SECRET_KEY` and only shown masked; every remote base URL is
a setting, and every call goes through the guarded HTTP client.

| Connector | Direction | How |
|---|---|---|
| HubSpot (private app token) | Both | Contacts pulled on a schedule (15 minutes to a day) through a field mapping, recorded as `crm_synced` events with `hubspot_id`; a contact is pushed on demand (`POST /v1/integrations/{id}/push`) or by the journey step **Push contact to integration** |
| Salesforce (connected app, client credentials; Contact or Lead) | Both | The same, with `salesforce_id` |
| Shopify | In | `POST /v1/webhooks/shopify/{id}` takes `orders/create`, `customers/create`, `checkouts/create` and `checkouts/update`, verified with Shopify's HMAC (`X-Shopify-Hmac-Sha256`) and the shop domain, as `order_completed`, `customer_created` and `cart_updated` |
| Facebook Custom Audiences, Google Ads Customer Match | Out | A segment linked to an audience uploads its members as SHA-256 hashes of the normalised email and phone, streams entries and exits, and reconciles every `PULSE_AUDIENCE_RECONCILE_INTERVAL` |

These connectors have been tested against mock servers only, not live accounts
([known gaps](../05-feature-parity.md#known-gaps)). Field-by-field behaviour is in the
[feature parity notes](../05-feature-parity.md) and the [architecture](../02-architecture.md#integrations-batch-8a).

Next: [7. Architecture and extending](07-architecture-and-extending.md).
