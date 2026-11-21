// Screenshots for docs/tutorials/16-connecting-any-provider.md: the Custom HTTP gateway set to
// "Connect to a gateway's existing API", step by step (where to send, a login request, the body,
// how Pulse knows it was sent, delivery reports), then Test connection and Preview request. The form
// is filled in and cancelled, never saved. Test connection logs in to a pretend SMS company's API
// that runs in a container on the compose network (docs/screenshots/mock-provider.py; start it
// first, see there). Without it the Test connection picture shows the error instead, and the script
// says so.
import { clearHighlights, go, highlight, session, settle, shot } from "../lib.mjs";

const AREA = "tutorials";
const API = "http://sms-api:8080"; // the mock's address on the compose network, as Pulse reaches it

const BODY = `{
  "to": "{{message.to}}",
  "text": "{{message.text}}",
  "sender": "ACMEIN"
}`;

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

  // Choose the gateway and "Connect to a gateway's existing API".
  await dialog.getByRole("combobox").first().selectOption("http_sms");
  await dialog.getByLabel("Provider name").fill("SMSCo");
  const existing = dialog.getByRole("radio", { name: /Connect to a gateway's existing API/ });
  await existing.click();
  await settle(page);
  await highlight(existing);
  await shot(page, AREA, "16-mode", { clip: dialog.locator(".panel-body > .grid-2, .panel-body > .choice-cards"), pad: 12 });
  await clearHighlights(page);
  // The paste box is open at the top; this walk-through fills the steps by hand, so fold it.
  await dialog.locator("details.pf-paste > summary").click();

  // 1 Where to send.
  const where = step("Where to send");
  await where.getByLabel(/^Address \(URL\)/).fill(`${API}/v1/messages`);
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "16-where", { clip: where, pad: 8 });

  // 2 How Pulse signs in: a login request.
  const auth = step("How Pulse signs in");
  await auth.getByLabel(/^Type/).selectOption("login");
  await auth.getByLabel(/^Login URL/).fill(`${API}/v1/login`);
  await auth.getByLabel(/^Username/).fill("acme-docs");
  await auth.getByLabel(/^Password/).fill("test-password-123");
  await auth.locator("details.pf-more > summary", { hasText: "Login details" }).click();
  await auth.getByLabel(/^JSON\ path/).fill("data.access_token");
  await auth.getByLabel(/^Expiry JSON path/).fill("data.expires_in");
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "16-auth", { clip: auth, pad: 8 });

  // 3 What to send.
  const what = step("What to send");
  await what.getByLabel(/^Body/).fill(BODY);
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "16-request", { clip: what, pad: 8 });

  // 4 How Pulse knows it was sent, with "Change this" open.
  const result = step("How Pulse knows it was sent");
  await result.getByText("Change this").click();
  await result.getByLabel(/^And this field in the answer/).fill("status");
  await result.getByLabel(/^Equals/).fill("queued");
  await result.getByLabel(/^The gateway's message ID is at/).fill("message_id");
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "16-result", { clip: result, pad: 8 });

  // 5 Delivery reports, and the receipt token that appears with them.
  const reports = step("Delivery reports (optional)");
  await reports.getByLabel("The gateway sends delivery reports").check();
  await reports.getByLabel(/^Message ID at/).fill("message_id");
  await reports.getByLabel(/^Status at/).fill("state");
  await reports.getByLabel(/^Status values/).fill("DELIVRD = delivered\nUNDELIV = undeliverable\nFAILED = failed");
  await reports.getByText("More fields (optional)").click();
  await reports.getByLabel(/^Failure reason at/).fill("error");
  await reports.getByLabel(/^Receipt token/).fill("a-long-random-receipt-token");
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "16-receipts", { clip: reports, pad: 8 });

  // 6 Check it. Test connection logs in now and shows the token, masked.
  const tools = step("Check it").locator(".provider-tools");
  await tools.getByRole("button", { name: "Test connection" }).click();
  await tools.locator(".alert").first().waitFor();
  if (await tools.locator(".alert.error").count()) console.log(`  Test connection failed (is the mock running? see mock-provider.py): ${await tools.locator(".alert.error").innerText()}`);
  await settle(page);
  await highlight(tools.getByRole("button", { name: "Test connection" }));
  await shot(page, AREA, "16-test-connection", { clip: tools, pad: 8 });
  await clearHighlights(page);

  // Preview request: the exact request for Pulse's test message, secrets masked.
  await tools.getByRole("button", { name: "Preview request" }).click();
  await tools.locator("pre").waitFor();
  await settle(page);
  await shot(page, AREA, "16-preview", { clip: tools.locator("pre"), pad: 8 });

  await dialog.getByRole("button", { name: "Cancel" }).click();
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
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "16-paste", { clip: paste, pad: 8 });
  const auth = dialog.getByRole("region", { name: "How Pulse signs in", exact: true });
  await shot(page, AREA, "16-paste-signin", { clip: auth, pad: 8 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
}
