// Screenshots for docs/tutorials/16-connecting-any-provider.md: the Custom HTTP gateway set to
// "Connect to a gateway's existing API", walked through its four-step wizard (1 Sign in with a login
// request and Test connection, 2 Send with Preview request, 3 Accepted or failed?, 4 Delivery
// reports), and the paste box filling the steps from a gateway's curl/Postman examples. The forms
// are filled in and cancelled, never saved. Test connection logs in to a pretend SMS company's API
// that runs in a container on the compose network (docs/screenshots/mock-provider.py; start it
// first, see there). Without it the Test connection picture shows the error instead, and the script
// says so.
import { clearHighlights, go, highlight, session, settle, shot, confirmIfAsked } from "../lib.mjs";

const AREA = "tutorials";
const API = "http://sms-api:8080"; // the mock's address on the compose network, as Pulse reaches it

const BODY = `{
  "to": "{{message.to}}",
  "text": "{{message.text}}",
  "sender": "ACMEIN",
  "client_ref": "{{message.id}}"
}`;

// A sample delivery report with SMSCo's three words, for step 4's "Fill in common words".
const REPORT = `[
  {"message_id": "a1b2c3", "state": "DELIVRD"},
  {"message_id": "d4e5f6", "state": "UNDELIV", "error": "absent subscriber"},
  {"message_id": "g7h8i9", "state": "FAILED", "error": "network error"}
]`;

// No text cursor, focus ring or hover state in the picture.
async function calm(page) {
  await page.evaluate(() => document.activeElement?.blur());
  await page.mouse.move(0, 0);
}

// The box around everything a locator matches, `pad` pixels wider and `bottom` pixels taller (for a
// picture whose next element sits close below it).
async function around(locator, pad, bottom) {
  const boxes = [];
  for (const el of await locator.all()) boxes.push(await el.boundingBox());
  const x = Math.min(...boxes.map((b) => b.x)) - pad, y = Math.min(...boxes.map((b) => b.y)) - pad;
  const right = Math.max(...boxes.map((b) => b.x + b.width)) + pad, end = Math.max(...boxes.map((b) => b.y + b.height)) + bottom;
  return { x, y, width: right - x, height: end - y };
}

export default async function (browser) {
  const { context, page } = await session(browser);
  // Tall enough that the dialog's body (70% of the window) holds every step without scrolling.
  await page.setViewportSize({ width: 1440, height: 4000 });
  await go(page, "/settings/channels");
  const sms = page.locator("section.panel").nth(1);
  await sms.getByRole("button", { name: "Add provider" }).click();
  const dialog = page.getByRole("dialog", { name: "Add SMS provider" });
  await dialog.waitFor();
  const step = (name) => dialog.getByRole("region", { name, exact: true });
  const bar = dialog.locator(".gw-bar");
  const next = (name) => dialog.getByRole("button", { name: `Next: ${name} →`, exact: true }).click();

  // Choose the gateway and "Connect to a gateway's existing API".
  await dialog.getByRole("combobox").first().selectOption("http_sms");
  await dialog.getByLabel("Provider name").fill("SMSCo");
  const existing = dialog.getByRole("radio", { name: /Connect to a gateway's existing API/ });
  await existing.click();
  await settle(page);
  await calm(page);
  await highlight(existing);
  await shot(page, AREA, "16-mode", { clip: dialog.locator(".panel-body > .grid-2, .panel-body > .choice-cards"), pad: 12 });
  await clearHighlights(page);
  // The paste box is open at the top; this walk-through fills the steps by hand, so fold it.
  await dialog.locator("details.pf-paste > summary").click();

  // Step 1, Sign in: a login request, with Login details open.
  const signIn = step("Sign in");
  await signIn.getByLabel(/^Type/).selectOption("login");
  await signIn.getByLabel(/^Login URL/).fill(`${API}/v1/login`);
  await signIn.locator("details.pf-more > summary", { hasText: "Login details" }).click();
  await signIn.getByLabel(/^JSON path/).fill("data.access_token");
  await signIn.getByLabel(/^Expiry JSON path/).fill("data.expires_in");
  await signIn.getByLabel(/^Username/).fill("acme-docs");
  await signIn.getByLabel(/^Password/).fill("test-password-123");
  await settle(page);
  await calm(page);
  await shot(page, AREA, "16-auth", { clip: dialog.locator(".gw-bar, .gw-step .fieldset"), pad: 8 });

  // Step 2, Send: where the request goes, then what it carries.
  await next("Send");
  const send = step("Send");
  await send.getByLabel(/^Address \(URL\)/).fill(`${API}/v1/messages`);
  await send.getByLabel(/^Body/).fill(BODY);
  await settle(page);
  await calm(page);
  await shot(page, AREA, "16-where", { clip: await around(dialog.locator(".gw-bar, .gw-step .pf-target"), 8, 4) });
  // "Further down": from Format to the folded How placeholders work and Extra headers.
  const format = send.locator("label.field").filter({ has: page.locator("span", { hasText: /^Format$/ }) });
  await shot(page, AREA, "16-request", { clip: format.or(send.locator("details.pf-more")), pad: 8 });

  // Step 2's Try it: Preview request, the exact request for a sample message, secrets masked.
  const trySend = send.locator(".gw-try");
  await trySend.getByRole("button", { name: "Preview request" }).click();
  await trySend.locator("pre").waitFor();
  await settle(page);
  await shot(page, AREA, "16-preview", { clip: trySend.locator("pre"), pad: 8 });

  // Back to step 1's Try it: Test connection logs in now and shows the token, masked. (Pulse
  // checks the whole draft, so it needs step 2's address, filled in above.)
  await bar.getByRole("button", { name: /Sign in/ }).click();
  const tryAuth = step("Sign in").locator(".gw-try");
  await tryAuth.getByRole("button", { name: "Test connection" }).click();
  await tryAuth.locator(".alert").first().waitFor();
  if (await tryAuth.locator(".alert.error").count()) console.log(`  Test connection failed (is the mock running? see mock-provider.py): ${await tryAuth.locator(".alert.error").innerText()}`);
  await settle(page);
  await calm(page);
  await highlight(tryAuth.getByRole("button", { name: "Test connection" }));
  await shot(page, AREA, "16-test-connection", { clip: tryAuth, pad: 8 });
  await clearHighlights(page);

  // Step 3, Accepted or failed?, with Change this open, and the Failed part.
  await bar.getByRole("button", { name: /Accepted or failed\?/ }).click(); // jump there in the bar
  const answer = step("Accepted or failed?");
  await answer.locator("details.pf-more > summary", { hasText: "Change this" }).click();
  await answer.getByLabel(/^And this field in the answer/).fill("status");
  await answer.getByLabel(/^Equals/).fill("queued");
  await answer.getByLabel(/^The gateway's message ID is at/).fill("message_id");
  await answer.getByLabel(/^Failed when this field/).fill("status");
  await answer.getByLabel(/^Is one of/).fill("rejected, failed");
  await settle(page);
  await calm(page);
  await shot(page, AREA, "16-result", { clip: bar.or(answer.locator("label.check")), pad: 8 });

  // Step 4, Delivery reports: They call us, the report's fields, and the words filled in from a sample.
  await next("Delivery reports");
  const reports = step("Delivery reports");
  await reports.getByRole("radio", { name: /They call us/ }).click();
  await reports.getByLabel(/^Receipt token/).fill("a-long-random-receipt-token");
  await reports.getByLabel(/^Message id at/).fill("message_id");
  await reports.getByLabel(/^Status at/).fill("state");
  await reports.getByLabel(/^Failure reason at/).fill("error");
  await reports.getByLabel(/^A report/).fill(REPORT);
  await reports.getByRole("button", { name: /Fill in common words/ }).click();
  await settle(page);
  await calm(page);
  await shot(page, AREA, "16-receipts", { clip: bar.or(reports.locator(".gw-try")), pad: 8 });

  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await confirmIfAsked(page, "Discard changes");
  await pasteExamples(page);
  await context.close();
}

// An RCS gateway API as its documentation and Postman show it (placeholder values): a login
// that returns {"JWTAUTH": …}, then a template message sent with that token.
const GW_SEND = `curl --location 'https://api.sms-gateway.example/rcs/v1/message' \\
--header 'Authorization: Bearer xxxxxtokenxxxx' \\
--header 'Content-Type: application/json' \\
--data '{
    "type": "template",
    "phone_no": "919812345678",
    "bot_name": "acme_promo",
    "template": {
        "name": "weekend_sale",
        "variables": { "name": "Asha" }
    }
}'`;
const GW_LOGIN = `postman request POST 'https://api.sms-gateway.example/auth/v1/login/' \\
  --header 'Content-Type: application/json' \\
  --body '{
    "username": "acme-docs",
    "password": "test-password-123"
}'`;
const GW_ANSWER = `{"JWTAUTH": "eyJhbGciOi.example.token", "user_data": {"email": "ops@acme-retail.test", "is_active": true}}`;

// Pasting a gateway's examples: an RCS provider filled in from them (cancelled, not saved).
async function pasteExamples(page) {
  await go(page, "/settings/channels");
  const rcs = page.locator("section.panel").nth(3); // Email, SMS, WhatsApp, RCS, Push
  await rcs.getByRole("button", { name: "Add provider" }).click();
  const dialog = page.getByRole("dialog", { name: "Add RCS provider" });
  await dialog.waitFor();
  await dialog.getByRole("combobox").first().selectOption("http_rcs");
  await dialog.getByLabel("Provider name").fill("Acme RCS gateway");
  await dialog.getByRole("radio", { name: /Connect to a gateway's existing API/ }).click();
  const paste = dialog.locator("details.pf-paste");
  await paste.getByLabel(/^Send-message request/).fill(GW_SEND);
  await paste.getByLabel(/^Login request/).fill(GW_LOGIN);
  await paste.getByLabel(/^Login answer/).fill(GW_ANSWER);
  await paste.getByRole("button", { name: "Fill in the form" }).click();
  await paste.locator(".alert").waitFor();
  await settle(page);
  await paste.locator("textarea").evaluateAll((els) => els.forEach((el) => { el.scrollTop = 0; })); // show each command's start
  await calm(page);
  await shot(page, AREA, "16-paste", { clip: paste, pad: 8 });
  // Step 1, Sign in, as the paste filled it in (Login details stay folded).
  await shot(page, AREA, "16-paste-signin", { clip: dialog.locator(".gw-bar, .gw-step .fieldset"), pad: 8 });
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await confirmIfAsked(page, "Discard changes");
}
