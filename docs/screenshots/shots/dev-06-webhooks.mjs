// Screenshots for docs/developer/06-webhooks-and-integrations.md:
//   - Settings → Webhooks → New webhook, filled in but not saved (with the "Verify the signature" panel);
//   - a saved webhook's signing secret and test panels: a throwaway, *paused* webhook (so nothing is
//     ever delivered) named with runId(), deleted again at the end, its secret masked;
//   - Settings → Channels → SMS → Add provider → Custom HTTP gateway, filled with placeholders and
//     cancelled.
import { api, go, maskSecrets, runId, session, settle, shot } from "../lib.mjs";

const AREA = "developer";

const panel = (page, heading) => page.locator("section.panel", { has: page.getByRole("heading", { name: heading, exact: true }) });

export default async function (browser) {
  const { context, page } = await session(browser);
  let hookId = 0;
  try {
    // 1. A new webhook, not saved.
    await page.setViewportSize({ width: 1440, height: 1300 });
    await go(page, "/settings/webhooks/new");
    await page.getByPlaceholder("Purchases to CRM").fill("Purchases to CRM");
    await page.getByPlaceholder("https://example.com/hooks/pulse").fill("https://crm.example.com/hooks/pulse");
    const add = page.getByLabel("Add an event");
    for (const name of ["purchase", "segment_entered"]) {
      await add.fill(name);
      await page.getByRole("button", { name: "Add", exact: true }).click();
    }
    await settle(page);
    await shot(page, AREA, "dev-06-webhook-new", { clip: page.locator(".page > *"), pad: 16 });

    // 2. A saved (paused) webhook: signing secret, masked, and the test panel.
    const made = await api(context, "POST", "/v1/settings/webhooks", {
      name: `Docs example ${runId()}`, url: "https://crm.example.com/hooks/pulse", events: ["purchase"], active: false,
    });
    hookId = made.id;
    await go(page, `/settings/webhooks/${hookId}`);
    const secret = panel(page, "Signing secret");
    await secret.getByRole("button", { name: "Reveal" }).waitFor();
    await maskSecrets(page, [".wh-secret code"]);
    await shot(page, AREA, "dev-06-webhook-secret", { clip: page.locator("section.panel", { has: page.getByRole("heading", { name: /^(Signing secret|Send a test)$/ }) }), pad: 8 });
    await api(context, "DELETE", `/v1/settings/webhooks/${hookId}`);
    hookId = 0;

    // 3. The custom HTTP gateway provider form (SMS) in its standard mode ("Your developers build an
    // endpoint for Pulse", the default), placeholders only, cancelled.
    // Tall enough that the whole form shows without scrolling inside the dialog.
    await page.setViewportSize({ width: 1440, height: 1800 });
    await go(page, "/settings/channels");
    const sms = page.locator("section.panel").nth(1);
    await sms.getByRole("button", { name: "Add provider" }).click();
    const dialog = page.getByRole("dialog", { name: "Add SMS provider" });
    await dialog.waitFor();
    await dialog.getByRole("combobox").first().selectOption("http_sms");
    await dialog.getByLabel("Name in Pulse").fill("Our SMS gateway");
    await dialog.getByLabel("Endpoint URL").fill("https://sms-gateway.example.com/pulse");
    await dialog.getByLabel(/^Signing secret/).fill("placeholder-not-a-secret");
    await dialog.getByLabel("Use for all SMS messages").uncheck();
    await settle(page);
    await shot(page, AREA, "dev-06-gateway", { clip: dialog, pad: 0 });
    await dialog.getByRole("button", { name: "Cancel" }).click();
  } finally {
    if (hookId) await api(context, "DELETE", `/v1/settings/webhooks/${hookId}`).catch(() => {});
    await context.close();
  }
}
