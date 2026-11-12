// Screenshots for docs/tutorials/16-connecting-any-provider.md: the Custom HTTP gateway in "my own
// request" mode with a login request, Test connection and Preview request. The form is filled in
// and cancelled, never saved. Test connection logs in to a pretend SMS company's API that runs in a
// container on the compose network (docs/screenshots/mock-provider.py; start it first, see there).
// Without it the Test connection picture shows the error instead, and the script says so.
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
  await page.setViewportSize({ width: 1440, height: 4000 });
  await go(page, "/settings/channels");
  const sms = page.locator("section.panel").nth(1);
  await sms.getByRole("button", { name: "Add provider" }).click();
  const dialog = page.getByRole("dialog", { name: "Add SMS provider" });
  await dialog.waitFor();

  // Choose the gateway and "My own request".
  await dialog.getByRole("combobox").first().selectOption("http_sms");
  await dialog.getByLabel("Name in Pulse").fill("SMSCo");
  const mode = dialog.getByLabel(/^How Pulse calls your gateway/);
  await mode.selectOption("custom");
  await settle(page);
  await highlight(mode);
  await shot(page, AREA, "16-mode", { clip: dialog.locator(".panel-body > label.field:nth-of-type(-n+3), .panel-body > p.small"), pad: 12 });
  await clearHighlights(page);

  // The request.
  const request = dialog.locator("fieldset", { has: page.locator("legend", { hasText: "Request" }) });
  await request.getByLabel(/^URL/).fill(`${API}/v1/messages`);
  await request.getByLabel(/^Body/).fill(BODY);
  await request.getByLabel("And the answer's JSON path (optional)").fill("status");
  await request.getByLabel("Equals (optional)").fill("queued");
  await request.getByLabel("Provider message ID at (JSON path)").fill("message_id");
  await settle(page);
  await shot(page, AREA, "16-request", { clip: request, pad: 8 });

  // Delivery receipts.
  await request.getByLabel(/The provider posts delivery receipts/).check();
  await request.getByLabel(/^Message\ ID\ at/).fill("message_id");
  await request.getByLabel(/^Status\ at/).fill("state");
  await request.getByLabel("Failure reason at (optional)").fill("error");
  await request.getByLabel("Status values").fill("DELIVRD = delivered\nUNDELIV = undeliverable\nFAILED = failed");
  await settle(page);
  await shot(page, AREA, "16-receipts", { clip: request.locator("label.check").or(request.locator(".grid-2").last()), pad: 8 });

  // Authentication: a login request.
  const auth = dialog.locator("fieldset", { has: page.locator("legend", { hasText: "Authentication" }) });
  await auth.getByLabel(/^Type/).selectOption("login");
  await auth.getByLabel(/^Login URL/).fill(`${API}/v1/login`);
  await auth.getByLabel(/^JSON\ path/).fill("data.access_token");
  await auth.getByLabel(/^Expiry JSON path/).fill("data.expires_in");
  await auth.getByLabel('Secret "username"').fill("acme-docs");
  await auth.getByLabel('Secret "password"').fill("test-password-123");
  await settle(page);
  await shot(page, AREA, "16-auth", { clip: auth, pad: 8 });

  // Test connection: logs in now and shows the token, masked.
  const tools = dialog.locator(".provider-tools");
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

  // The secrets at the end of the form, and the default checkbox.
  await dialog.getByLabel("Receipt token (optional)").fill("a-long-random-receipt-token");
  await settle(page);
  await shot(page, AREA, "16-receipt-token", {
    clip: dialog.locator(".panel-body > label.field").filter({ hasText: "Receipt token" }),
    pad: 8,
  });

  await dialog.getByRole("button", { name: "Cancel" }).click();
  await context.close();
}
