// Screenshots for docs/tutorials/04-channels.md.
// Uses the docs workspace, which the seed gives sandbox providers for SMS, WhatsApp, RCS and push
// (email goes through the server's SMTP relay, Mailpit in development). Provider forms are filled
// with placeholder values and cancelled, never saved; delivery rules are changed on screen only.
// The one thing it sends is a provider test through the SMS sandbox, which lands in the test inbox.
import { api, BASE, clearHighlights, DOCS, go, highlight, session, settle, shot, confirmIfAsked } from "../lib.mjs";

const AREA = "tutorials";

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
  let dialog = page.getByRole("dialog", { name: "Add Email provider" });
  await dialog.waitFor();
  await dialog.getByRole("combobox").first().selectOption("smtp");
  await dialog.getByLabel("Provider name").fill("Amazon SES (Mumbai)");
  // "Fill in for:" Amazon SES fills host, port, encryption, login type and sending service.
  await dialog.getByRole("group", { name: "Fill in for a service" }).getByRole("button", { name: "Amazon SES" }).click();
  await dialog.getByLabel(/^From address/).fill("Acme Retail <hello@acme-retail.example.com>");
  await page.setViewportSize({ width: 1440, height: 1700 });
  await settle(page);
  // More settings opens by itself: the preset filled its Sending service.
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "04-add-smtp", { clip: dialog, pad: 0 });
  // Its Sending limits: an SES account allowed 300 a second, above the server's 200 for email.
  const limitsPart = dialog.getByRole("region", { name: "Sending limits" });
  await limitsPart.getByLabel("Messages per second").fill("300");
  await settle(page);
  await shot(page, AREA, "04-add-limits", { clip: limitsPart, pad: 8 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");
  await page.setViewportSize({ width: 1440, height: 900 });

  // Gmail: the three-step app password guide, only the fields of the chosen sign-in method, and the
  // daily quota it starts with (placeholders, not saved).
  await emailPanel.getByRole("button", { name: "Add provider" }).click();
  dialog = page.getByRole("dialog", { name: "Add Email provider" });
  await dialog.getByRole("combobox").first().selectOption("gmail_smtp");
  await dialog.getByLabel("Provider name").fill("Gmail (Asha)");
  await dialog.getByLabel(/^Google account/).fill("asha.acme.retail@gmail.example.com");
  await dialog.getByLabel(/^From address/).fill("Asha at Acme <asha.acme.retail@gmail.example.com>");
  await page.setViewportSize({ width: 1440, height: 2300 });
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "04-add-gmail", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");
  await page.setViewportSize({ width: 1440, height: 900 });

  // 3. SMS: MSG91, and WhatsApp: Meta Cloud API (placeholders, not saved).
  const smsPanel = page.locator("section.panel").nth(1);
  await smsPanel.getByRole("button", { name: "Add provider" }).click();
  dialog = page.getByRole("dialog", { name: "Add SMS provider" });
  await dialog.getByRole("combobox").first().selectOption("msg91");
  await dialog.getByLabel("Provider name").fill("MSG91 (India)");
  await dialog.getByLabel(/^Flow template ID/).fill("64f1c2e5d6a7b8c9d0e1f2a3");
  await dialog.getByLabel(/^Sender ID/).fill("ACMEIN");
  await dialog.getByLabel("Use for all SMS messages").uncheck();
  await page.setViewportSize({ width: 1440, height: 1300 });
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "04-add-msg91", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");
  await page.setViewportSize({ width: 1440, height: 900 });

  const waPanel = page.locator("section.panel").nth(2);
  await waPanel.getByRole("button", { name: "Add provider" }).click();
  dialog = page.getByRole("dialog", { name: "Add WhatsApp provider" });
  await dialog.getByRole("combobox").first().selectOption("meta_whatsapp");
  await dialog.getByLabel("Provider name").fill("WhatsApp (Acme Retail)");
  await dialog.getByLabel(/^Phone number ID/).fill("106540352242922");
  await dialog.getByLabel(/^WhatsApp Business account ID/).fill("102290129340398");
  await page.setViewportSize({ width: 1440, height: 1500 });
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "04-add-whatsapp", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");
  await page.setViewportSize({ width: 1440, height: 900 });

  // 4. Push: Firebase Cloud Messaging (placeholders, not saved). The custom gateway is in 16-*.mjs.
  const pushPanel = page.locator("section.panel").nth(4);
  await pushPanel.getByRole("button", { name: "Add provider" }).click();
  dialog = page.getByRole("dialog", { name: "Add Push provider" });
  await dialog.getByRole("combobox").first().selectOption("fcm");
  await dialog.getByLabel("Provider name").fill("Firebase (Acme app)");
  await dialog.getByLabel(/^Firebase project ID/).fill("acme-retail-app");
  await page.setViewportSize({ width: 1440, height: 1300 });
  await settle(page);
  await page.evaluate(() => document.activeElement?.blur()); // no text cursor in the picture
  await shot(page, AREA, "04-add-fcm", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");

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

  // 7. Bounces, complaints and unsubscribes have their own page: 04-bounces-and-unsubscribes.mjs.

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

  // 10. Sending limits and today's usage (M3): the SMS sandbox has 20/s and 5,000 a day (docs seed).
  // The Limits dialog shows the same fields as the provider form's Sending limits, with the server's rate.
  await go(page, "/settings/channels");
  const sms = page.locator("section.panel").nth(1);
  await shot(page, AREA, "04-limits-usage", { clip: sms, pad: 8 });
  await sms.getByRole("button", { name: "Limits" }).click();
  const limits = page.getByRole("dialog", { name: /^Sending limits/ });
  await limits.waitFor();
  await settle(page);
  await shot(page, AREA, "04-limits", { clip: limits, pad: 0 });
  await limits.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");

  // 11. Saved test contacts, and the Send a test dialog that offers them.
  await page.setViewportSize({ width: 1440, height: 1400 });
  const tcs = panel(page, "Test contacts");
  await tcs.scrollIntoViewIfNeeded();
  await settle(page);
  await shot(page, AREA, "04-test-contacts", { clip: tcs, pad: 8 });
  await tcs.getByRole("button", { name: "Add test contact" }).click();
  const add = page.getByRole("dialog", { name: "Add a test contact" });
  await add.getByLabel("Name").fill("Nina (marketing)");
  await add.getByLabel("Email").fill("nina@acme-retail.test");
  await add.getByLabel(/^Phone/).fill("+919812399003");
  await settle(page);
  await shot(page, AREA, "04-test-contact-add", { clip: add, pad: 0 });
  await add.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");
  await page.setViewportSize({ width: 1440, height: 900 });
  const tpl = (await api(context, "GET", "/v1/templates?channel=sms")).templates.find((t) => t.name === "Cart rescue SMS");
  await go(page, `/templates/${tpl.id}`);
  await page.getByRole("button", { name: "Send a test" }).click();
  const test = page.getByRole("dialog", { name: /^Send a test/ });
  await test.getByLabel("Send to").selectOption({ label: "Priya (QA) (+919812399001)" });
  await settle(page);
  await shot(page, AREA, "04-send-test-saved", { clip: test, pad: 0 });
  await page.keyboard.press("Escape");

  await webPush(context, page);
  await context.close();
}

// 12. Settings → Web push: the opt-in prompt (changed on screen, not saved), the check from the
// server against a public site, and the browser check on one of the workspace's landing pages
// (the site address is saved for it, then the settings are put back as they were).
async function webPush(context, page) {
  const before = (await api(context, "GET", "/v1/settings/web-push")).settings;
  try {
    await page.setViewportSize({ width: 1440, height: 1800 });
    await go(page, "/settings/web-push");
    const prompt = panel(page, "Opt-in prompt");
    await prompt.locator(".switch input").check();
    await prompt.locator("label.wp-style", { hasText: "Pop-up" }).click();
    await prompt.getByLabel(/^Title/).fill("Never miss a sale");
    await prompt.getByLabel(/^Message/).fill("Get a note when your favourites are back in stock or on sale. At most two a week.");
    await prompt.getByLabel(/^Allow button/).fill("Yes, notify me");
    await prompt.getByLabel(/^After/).fill("10");
    await prompt.getByRole("textbox", { name: /^Never on pages/ }).fill("/checkout");
    await prompt.getByRole("button", { name: "Add", exact: true }).last().click();
    await settle(page, { quiet: 600 });
    await shot(page, AREA, "04-web-push-prompt", { clip: prompt, pad: 8 });

    await go(page, "/settings/web-push");
    const check = panel(page, "Install check");
    await check.getByLabel(/^Your site's address/).fill("https://www.example.com/");
    await check.getByRole("button", { name: "Check my site" }).click();
    await check.locator(".wp-report").first().waitFor({ timeout: 30_000 });
    await settle(page);
    await shot(page, AREA, "04-web-push-check", { clip: check.locator(".wp-check").first(), pad: 8 });

    // The browser check opens the saved site address with a one-off ?pulse_check= link.
    const lp = (await api(context, "GET", "/v1/landing-pages")).pages.map((x) => x.page).find((x) => x.status === "published");
    if (!lp) { console.log("  no published landing page: 04-web-push-browser not taken"); return; }
    await api(context, "PUT", "/v1/settings/web-push", { ...before, site_url: `${BASE}/p/${DOCS.workspace}/${lp.slug}` });
    await go(page, "/settings/web-push");
    const browserCheck = panel(page, "Install check").locator(".wp-check").last();
    await browserCheck.getByRole("button", { name: "Check in my browser" }).click();
    const link = await browserCheck.locator(".wp-link code").innerText();
    const site = await context.newPage();
    await site.goto(link);
    await settle(site, { quiet: 1500 });
    await site.close();
    await browserCheck.locator(".wp-headline").waitFor({ timeout: 30_000 });
    await settle(page);
    await shot(page, AREA, "04-web-push-browser", { clip: browserCheck, pad: 8 });
  } finally {
    await api(context, "PUT", "/v1/settings/web-push", before).catch((e) => console.log(`  web push settings restore: ${e}`));
    await page.setViewportSize({ width: 1440, height: 900 });
  }
}
