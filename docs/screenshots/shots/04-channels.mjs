// Screenshots for docs/tutorials/04-channels.md.
// Uses the docs workspace, which the seed gives sandbox providers for SMS, WhatsApp, RCS and push
// (email goes through the server's SMTP relay, Mailpit in development). Provider forms are filled
// with placeholder values and cancelled, never saved; delivery rules are changed on screen only.
// The one thing it sends is a provider test through the SMS sandbox, which lands in the test inbox.
import { clearHighlights, go, highlight, session, settle, shot } from "../lib.mjs";

const AREA = "tutorials";

/** The bounce and reply webhook URLs end in a workspace token: hide it like a secret. */
async function maskWebhookTokens(page) {
  await page.evaluate(() => {
    for (const el of document.querySelectorAll("code")) {
      el.textContent = el.textContent.replace(/\/v1\/(bounces|replies)\/[0-9a-f]{16,}/g, "/v1/$1/••••••••••••••••");
    }
  });
}

const panel = (page, heading) => page.locator("section.panel", { has: page.getByRole("heading", { name: heading, exact: true }) });

export default async function (browser) {
  const { context, page } = await session(browser);

  // 1. The Channels page: one panel per channel.
  await go(page, "/settings/channels");
  const emailPanel = page.locator("section.panel").first();
  await highlight(emailPanel.getByRole("button", { name: "Add provider" }));
  await shot(page, AREA, "04-channels", { clip: { x: 0, y: 0, width: 1440, height: 760 } });
  await clearHighlights(page);

  // 2. Email: the SMTP provider form, filled with placeholders (not saved).
  await emailPanel.getByRole("button", { name: "Add provider" }).click();
  let dialog = page.getByRole("dialog", { name: "Add a Email provider" });
  await dialog.waitFor();
  await dialog.getByRole("combobox").first().selectOption("smtp");
  await dialog.getByLabel("Name in Pulse").fill("Amazon SES (Mumbai)");
  await dialog.getByLabel("Host").fill("email-smtp.ap-south-1.amazonaws.com");
  await dialog.getByLabel("Port").fill("587");
  await dialog.getByLabel("From address").fill("Acme Retail <hello@acme-retail.example.com>");
  await settle(page);
  await shot(page, AREA, "04-add-smtp", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();

  // 3. SMS: MSG91, and WhatsApp: Meta Cloud API (placeholders, not saved).
  const smsPanel = page.locator("section.panel").nth(1);
  await smsPanel.getByRole("button", { name: "Add provider" }).click();
  dialog = page.getByRole("dialog", { name: "Add a SMS provider" });
  await dialog.getByRole("combobox").first().selectOption("msg91");
  await dialog.getByLabel("Name in Pulse").fill("MSG91 (India)");
  await dialog.getByLabel(/^Flow template ID/).fill("64f1c2e5d6a7b8c9d0e1f2a3");
  await dialog.getByLabel(/^Sender ID/).fill("ACMEIN");
  await dialog.getByLabel("Use for all SMS messages").uncheck();
  await settle(page);
  await shot(page, AREA, "04-add-msg91", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();

  const waPanel = page.locator("section.panel").nth(2);
  await waPanel.getByRole("button", { name: "Add provider" }).click();
  dialog = page.getByRole("dialog", { name: "Add a WhatsApp provider" });
  await dialog.getByRole("combobox").first().selectOption("meta_whatsapp");
  await dialog.getByLabel("Name in Pulse").fill("WhatsApp (Acme Retail)");
  await dialog.getByLabel(/^Phone number ID/).fill("106540352242922");
  await dialog.getByLabel(/^WhatsApp Business account ID/).fill("102290129340398");
  await page.setViewportSize({ width: 1440, height: 1300 });
  await settle(page);
  await shot(page, AREA, "04-add-whatsapp", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await page.setViewportSize({ width: 1440, height: 900 });

  // 4. Push: Firebase Cloud Messaging; and the custom HTTP gateway (placeholders, not saved).
  const pushPanel = page.locator("section.panel").nth(4);
  await pushPanel.getByRole("button", { name: "Add provider" }).click();
  dialog = page.getByRole("dialog", { name: "Add a Push provider" });
  await dialog.getByRole("combobox").first().selectOption("fcm");
  await dialog.getByLabel("Name in Pulse").fill("Firebase (Acme app)");
  await dialog.getByLabel(/^Firebase project ID/).fill("acme-retail-app");
  await page.setViewportSize({ width: 1440, height: 1100 });
  await settle(page);
  await shot(page, AREA, "04-add-fcm", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();

  await smsPanel.getByRole("button", { name: "Add provider" }).click();
  dialog = page.getByRole("dialog", { name: "Add a SMS provider" });
  await dialog.getByRole("combobox").first().selectOption("http_sms");
  await dialog.getByLabel("Name in Pulse").fill("Our SMS gateway");
  await dialog.getByLabel(/^Endpoint URL/).fill("https://gateway.example.com/pulse");
  await settle(page);
  await shot(page, AREA, "04-add-gateway", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await page.setViewportSize({ width: 1440, height: 900 });

  // 5. A provider test through the SMS sandbox.
  await highlight(smsPanel.getByRole("button", { name: "Send test" }));
  await shot(page, AREA, "04-sms-sandbox", { clip: smsPanel, pad: 8 });
  await clearHighlights(page);
  await smsPanel.getByRole("button", { name: "Send test" }).click();
  dialog = page.getByRole("dialog", { name: /^Send a test through/ });
  await dialog.getByLabel("Send to phone number").fill("+919812345678");
  await dialog.getByRole("button", { name: "Send test" }).click();
  await dialog.getByText(/Accepted by the provider/).waitFor();
  await settle(page);
  await shot(page, AREA, "04-provider-test", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Close" }).last().click();

  // 6. Delivery rules, changed on screen only (other guides' journeys use this workspace).
  const rules = panel(page, "Delivery rules");
  await rules.getByRole("row", { name: /Email/ }).getByRole("checkbox").check();
  await rules.getByRole("spinbutton", { name: "Messages" }).first().fill("2");
  await rules.getByRole("spinbutton", { name: "Hours" }).first().fill("24");
  await rules.getByLabel(/Hold marketing messages overnight/).check();
  await page.setViewportSize({ width: 1440, height: 1500 });
  await settle(page);
  await highlight(rules.getByRole("button", { name: "Save delivery rules" }));
  await shot(page, AREA, "04-delivery-rules", { clip: rules, pad: 8 });
  await clearHighlights(page);
  await go(page, "/settings/channels"); // drop the unsaved changes

  // 7. Bounces and complaints (token hidden), and the bounce mailbox.
  await page.setViewportSize({ width: 1440, height: 1500 });
  const bounces = panel(page, "Bounces and complaints");
  await bounces.scrollIntoViewIfNeeded();
  await maskWebhookTokens(page);
  await shot(page, AREA, "04-bounces", { clip: bounces, pad: 8 });
  const mailbox = panel(page, "Bounce mailbox (IMAP)");
  await mailbox.getByLabel("IMAP server").fill("imap.example.com");
  await mailbox.getByLabel("Username").fill("bounces@acme-retail.example.com");
  await settle(page);
  await shot(page, AREA, "04-bounce-mailbox", { clip: mailbox, pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });

  // 8. App inbox panel.
  await go(page, "/settings/channels");
  const inbox = page.locator("section.panel", { hasText: "Pulse keeps inbox messages itself" });
  await shot(page, AREA, "04-app-inbox", { clip: inbox, pad: 8 });

  // 9. The test inbox, with the SMS from step 5, and the simulators (filled in, not sent).
  await go(page, "/settings/test-inbox");
  await page.getByRole("tab", { name: "SMS" }).click();
  await page.getByRole("cell", { name: "+919812345678" }).first().waitFor();
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "04-test-inbox", { clip: { x: 0, y: 0, width: 1440, height: 520 } });
  await page.getByRole("tab", { name: "All" }).click();
  const reply = panel(page, "Simulate a reply");
  await reply.getByLabel("From").fill("+919812345678");
  const bounce = panel(page, "Simulate a bounce");
  await bounce.getByLabel("Email").fill("someone@example.com");
  await settle(page);
  await shot(page, AREA, "04-simulators", { clip: page.locator(".grid-2").last(), pad: 8 });

  await context.close();
}
