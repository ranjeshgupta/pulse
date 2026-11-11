// Screenshots for docs/tutorials/09-integrations.md.
// Vendor APIs (HubSpot, Salesforce, Shopify admin, Facebook, Google) aren't reachable from a local
// Pulse, so forms are filled with obviously fake values and only saved where saving never calls the
// vendor: HubSpot with "Never (push only)", Shopify (its webhooks come *to* Pulse; one signed test
// delivery is posted to the local Pulse, for a fixed test buyer shopify.buyer@example.com), Zapier /
// Make (its subscription targets a fake hooks.example.com address; while its page crashes the script
// only checks the API workaround the tutorial gives) and Facebook (no segment linked, so nothing is
// uploaded). Nothing here clicks Test connection, Pull now or Push. Everything this script saves
// (integrations, the webhook, the draft journey) is deleted at the end.
import { createHmac } from "node:crypto";
import { api, BASE, clearHighlights, go, highlight, maskSecrets, runId, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";

const panel = (page, title) => page.locator("section.panel", { has: page.getByRole("heading", { name: title, exact: true }) });
/** The input or select of a form field, by its exact label (hints are part of the label element). */
const field = (page, label) => page.locator("label.field").filter({ has: page.locator(`xpath=./span[normalize-space(.)=${JSON.stringify(label)}]`) }).locator("input, select, textarea").first();
const section = (page, title) => page.locator("section.section", { has: page.getByRole("heading", { name: title, exact: true }) });

export default async function (browser) {
  const { context, page } = await session(browser);
  const made = { integrations: [], webhooks: [], journeys: [] };
  try {
    await run(context, page, made);
  } finally {
    for (const id of made.journeys) await api(context, "DELETE", `/v1/journeys/${id}`).catch(() => {});
    for (const id of made.integrations) await api(context, "DELETE", `/v1/integrations/${id}`).catch(() => {});
    for (const id of made.webhooks) await api(context, "DELETE", `/v1/settings/webhooks/${id}`).catch(() => {});
    await context.close();
  }
}

/** Opens the Connect form for a kind and waits for it. */
async function connectForm(page, kind) {
  await go(page, `/settings/integrations/new?kind=${kind}`);
  await panel(page, "Settings").waitFor();
}

async function saveNew(page, made, reload = true) {
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await page.waitForURL((u) => /\/settings\/integrations\/\d+$/.test(u.pathname));
  const id = Number(new URL(page.url()).pathname.split("/").pop());
  made.integrations.push(id);
  await settle(page, { quiet: 800 });
  // Right after Connect the form shows the type's defaults again (see the report: reload to see
  // what was saved), so reload before any picture.
  console.log(`  after Connect, the Name field shows: ${await field(page, "Name").inputValue()}`);
  if (reload) {
    await page.reload();
    await settle(page, { quiet: 800 });
  }
  return id;
}

async function run(context, page, made) {
  // A contact with an email, for previews (nothing is sent anywhere).
  const contacts = (await api(context, "GET", "/v1/contacts?limit=50")).contacts;
  const sample = contacts.find((c) => c.email && c.attrs?.phone) ?? contacts.find((c) => c.email);

  // ---- The Integrations page ----
  await go(page, "/settings/integrations");
  const hubspotCard = page.locator(".int-card", { hasText: "HubSpot" });
  await highlight(hubspotCard.getByRole("link", { name: "Connect" }));
  await shot(page, AREA, "09-integrations-list", { clip: page.locator(".page-head, section.section:has(h3:text-is('CRM')), section.section:has(h3:text-is('E-commerce'))"), pad: 8 });
  await clearHighlights(page);

  // ---- HubSpot: the form, filled with a fake token ----
  await page.setViewportSize({ width: 1440, height: 1300 });
  await connectForm(page, "hubspot");
  await field(page, "Private app access token").fill("pat-na1-00000000-0000-0000-0000-docs-example");
  await field(page, "Pull contacts").selectOption("60");
  await settle(page);
  await shot(page, AREA, "09-hubspot-settings", { clip: page.locator(".page-head, section.panel:has(h3:text-is('Settings'))"), pad: 8 });

  // Field mapping: add a row that pushes the lead score out to HubSpot.
  await page.getByRole("button", { name: "Add field" }).click();
  const rows = page.locator(".int-map tbody tr");
  const last = rows.last();
  await last.getByLabel("HubSpot field").fill("pulse_points");
  await last.getByLabel("Direction").selectOption("push");
  await last.getByLabel("Pulse attribute").fill("points");
  await settle(page);
  await shot(page, AREA, "09-field-mapping", { clip: panel(page, "Field mapping"), pad: 8 });

  // Saved with "Never (push only)": a saved HubSpot connector with no pull schedule never calls
  // HubSpot on its own, and this script never clicks Test connection, Pull now or Push.
  await field(page, "Pull contacts").selectOption("0");
  await field(page, "Name").fill("HubSpot (docs example)");
  const hubspotId = await saveNew(page, made);
  await shot(page, AREA, "09-hubspot-saved", { clip: page.locator(".page-head, .page > p.small"), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 2600 });
  await settle(page);
  await shot(page, AREA, "09-crm-actions", {
    clip: page.locator("section.panel:has(h3:text-is('Test connection')), section.panel:has(h3:text-is('Pull contacts')), section.panel:has(h3:text-is('Push a contact'))"),
    pad: 8,
  });

  // Push a contact → Preview (computed in Pulse; nothing goes to HubSpot).
  const push = panel(page, "Push a contact");
  await push.getByLabel("Contact's external ID").fill(sample.external_id);
  await push.getByRole("button", { name: "Preview" }).click();
  await push.locator("pre").waitFor();
  await settle(page);
  await highlight(push.getByRole("button", { name: "Push", exact: true }));
  await shot(page, AREA, "09-push-preview", { clip: push, pad: 8 });
  await clearHighlights(page);

  // ---- Salesforce: the form only ----
  await connectForm(page, "salesforce");
  await field(page, "My Domain URL").fill("https://acme-retail-dev.my.salesforce.com");
  await field(page, "Consumer key").fill("3MVG9-docs-example-not-a-real-key");
  await field(page, "Consumer secret").fill("docs-example-not-a-real-secret");
  await field(page, "Pull contacts").selectOption("60");
  await settle(page);
  await shot(page, AREA, "09-salesforce-settings", { clip: page.locator(".page-head, section.panel:has(h3:text-is('Settings'))"), pad: 8 });

  // ---- Shopify: saved; its webhook URL; one signed delivery to the local Pulse ----
  await connectForm(page, "shopify");
  const shop = "acme-retail-docs.myshopify.com";
  const secret = `docs-shopify-secret-${runId()}`;
  await field(page, "Shop domain").fill(shop);
  await field(page, "Webhook signing secret").fill(secret);
  await settle(page);
  await shot(page, AREA, "09-shopify-settings", { clip: page.locator(".page-head, section.panel:has(h3:text-is('Settings'))"), pad: 8 });
  await field(page, "Name").fill("Shopify (docs example)");
  const shopifyId = await saveNew(page, made);
  const hooks = panel(page, "Shopify webhooks");
  await hooks.waitFor();
  await shot(page, AREA, "09-shopify-webhooks", { clip: hooks, pad: 8 });

  await hooks.getByRole("button", { name: "Preview a sample order" }).click();
  await hooks.locator("pre").waitFor();
  await settle(page);
  await shot(page, AREA, "09-shopify-preview", { clip: hooks.locator("pre"), pad: 8 });

  // What Shopify would send for a new order: signed with the secret, posted to the webhook URL.
  const order = JSON.stringify({
    id: 820982911946154500 + Number(Date.now() % 1000), email: "shopify.buyer@example.com", name: "#1001", order_number: 1001,
    total_price: "1499.00", total_discounts: "0.00", currency: "INR", financial_status: "paid",
    line_items: [{ product_id: 632910392, variant_id: 808950810, title: "Linen shirt", sku: "LS-01", quantity: 1, price: "1499.00" }],
    customer: { id: 115310627314723950, email: null, first_name: "Meera", last_name: "Iyer" },
  });
  const res = await context.request.post(`${BASE}/v1/webhooks/shopify/${shopifyId}`, {
    data: order,
    headers: {
      "Content-Type": "application/json", "X-Shopify-Topic": "orders/create", "X-Shopify-Shop-Domain": shop,
      "X-Shopify-Webhook-Id": `docs-${runId()}`, "X-Shopify-Hmac-Sha256": createHmac("sha256", secret).update(order).digest("base64"),
    },
  });
  if (!res.ok()) throw new Error(`Shopify test delivery: ${res.status()} ${await res.text()}`);
  await until(async () => (await api(context, "GET", `/v1/integrations/${shopifyId}`)).log.length > 0, { what: "the Shopify log entry" });
  await go(page, `/settings/integrations/${shopifyId}`);
  await section(page, "Sync log").locator("tbody tr").first().waitFor();
  await shot(page, AREA, "09-sync-log", { clip: section(page, "Sync log"), pad: 8 });

  // ---- Zapier / Make: saved; the key is shown once (masked here) ----
  // In this version the Zapier / Make page fails to render (the catalog sends "config": null for
  // this type and the page calls .filter on it), so the pictures are taken only once that's fixed.
  await go(page, "/settings/integrations/new?kind=rest_hooks");
  if (await page.getByText("Unexpected Application Error").count()) {
    console.log("  Zapier / Make: the page crashes (config is null), pictures skipped");
    // The workaround the tutorial gives: create the connector through the API, then use its key.
    const r = await api(context, "POST", "/v1/integrations", { kind: "rest_hooks", name: "Zapier (docs example)", active: true, config: {}, secrets: {}, mapping: [], sync_minutes: 0 });
    made.integrations.push(r.id);
    const auth = { Authorization: `Bearer ${r.api_key}`, "Content-Type": "application/json" };
    const me = await context.request.get(`${BASE}/v1/hooks/me`, { headers: auth });
    const vip = (await api(context, "GET", "/v1/segments")).segments.find((s) => s.name === "VIP tag");
    const sub = await context.request.post(`${BASE}/v1/hooks/subscriptions`, {
      data: { target_url: "https://hooks.example.com/zapier/catch/1234567/abcdef/", event: "segment_entered", segment_id: vip.id }, headers: auth,
    });
    console.log(`  API workaround: key ${String(r.api_key).slice(0, 4)}…, GET /v1/hooks/me ${me.status()} ${(await me.text()).slice(0, 160)}, subscribe ${sub.status()}`);
  } else {
    await field(page, "Name").fill("Zapier (docs example)");
    const zapId = await saveNew(page, made, false);
    const keyBox = page.locator(".int-key");
    await keyBox.waitFor();
    const key = (await keyBox.locator("code").textContent()).trim();
    await maskSecrets(page);
    await shot(page, AREA, "09-zapier-key", { clip: page.locator(".page-head, .int-key, section.panel:has(h3:text-is('API key and endpoints'))"), pad: 8 });
    // A trigger subscription, as Zapier makes when a Zap is turned on (the target is a fake address).
    const vip = (await api(context, "GET", "/v1/segments")).segments.find((s) => s.name === "VIP tag");
    const sub = await context.request.post(`${BASE}/v1/hooks/subscriptions`, {
      data: { target_url: "https://hooks.example.com/zapier/catch/1234567/abcdef/", event: "segment_entered", segment_id: vip.id },
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
    });
    if (!sub.ok()) throw new Error(`subscribe: ${sub.status()} ${await sub.text()}`);
    await go(page, `/settings/integrations/${zapId}`);
    await section(page, "Subscriptions").locator("tbody tr").first().waitFor();
    await shot(page, AREA, "09-zapier-subscriptions", { clip: section(page, "Subscriptions"), pad: 8 });
  }

  // ---- Facebook Custom Audiences: saved, no segment linked (nothing is uploaded) ----
  await connectForm(page, "facebook_audiences");
  await field(page, "Ad account ID").fill("act_1234567890");
  await field(page, "Default country calling code (optional)").fill("91");
  await field(page, "System user access token").fill("EAAB-docs-example-not-a-real-token");
  await settle(page);
  await shot(page, AREA, "09-facebook-settings", { clip: page.locator(".page-head, section.panel:has(h3:text-is('Settings'))"), pad: 8 });
  await field(page, "Name").fill("Facebook audiences (docs example)");
  await saveNew(page, made);
  const link = panel(page, "Link a segment");
  await link.getByLabel("Segment").selectOption({ label: "VIP customers" });
  await settle(page);
  await highlight(link.getByRole("button", { name: "Link segment" }));
  await shot(page, AREA, "09-audience-link", { clip: page.locator("section.section:has(h3:text-is('Audiences')), section.panel:has(h3:text-is('Link a segment'))"), pad: 8 });
  await clearHighlights(page);
  const check = panel(page, "Check a contact");
  await check.getByLabel("Contact's external ID").fill(sample.external_id);
  await check.getByRole("button", { name: "Check" }).click();
  await check.locator(".alert").waitFor();
  await settle(page);
  await shot(page, AREA, "09-audience-check", { clip: check, pad: 8 });

  // ---- Google Ads Customer Match: the form only ----
  await connectForm(page, "google_ads");
  await field(page, "Customer ID").fill("123-456-7890");
  await field(page, "Developer token").fill("docs-example-developer-token");
  await field(page, "OAuth client ID").fill("000000000000-docs.apps.googleusercontent.com");
  await field(page, "OAuth client secret").fill("docs-example-client-secret");
  await field(page, "Refresh token").fill("1//docs-example-refresh-token");
  await settle(page);
  await shot(page, AREA, "09-google-settings", { clip: page.locator(".page-head, section.panel:has(h3:text-is('Settings'))"), pad: 8 });

  // ---- The journey step: a draft journey with "Push contact to integration" ----
  const j = await api(context, "POST", "/v1/journeys", {
    name: `Docs: push buyers to HubSpot ${runId()}`,
    graph: {
      trigger: { type: "event", event: "purchase" }, entry: "push1",
      nodes: { push1: { type: "push_integration", integration_id: hubspotId, next: "exit", x: 300, y: 140 }, exit: { type: "exit", x: 300, y: 280 } },
    },
  });
  made.journeys.push(j.id);
  await page.setViewportSize({ width: 1440, height: 900 });
  await go(page, `/journeys/${j.id}`);
  await page.locator('.react-flow__node[data-id="push1"]').click();
  const insp = page.locator("aside.inspector");
  await insp.getByText("Push contact to integration").first().waitFor();
  await settle(page);
  await shot(page, AREA, "09-journey-push", { clip: insp.locator(".insp"), pad: 12 });

  // ---- Webhooks ----
  await go(page, "/settings/webhooks");
  await page.getByRole("link", { name: "New webhook" }).click();
  await settle(page);
  await field(page, "Name").fill("Purchases to CRM");
  await field(page, "URL").fill("https://hooks.example.com/pulse");
  const add = page.getByLabel("Add an event");
  const every = page.getByLabel("Send every event");
  // Check (for the report): does clicking a "+ event" suggestion tick "Send every event"?
  await page.getByRole("button", { name: "+ purchase" }).click();
  console.log(`  after clicking "+ purchase", Send every event is ${await every.isChecked() ? "ticked" : "not ticked"}`);
  if (await every.isChecked()) await every.uncheck();
  for (const e of ["purchase", "segment_entered"]) {
    await add.fill(e);
    await add.press("Enter");
  }
  console.log(`  after typing two events, Send every event is ${await every.isChecked() ? "ticked" : "not ticked"}`);
  await settle(page);
  const addHook = page.getByRole("button", { name: "Add webhook" });
  await highlight(addHook);
  await shot(page, AREA, "09-webhook-new", { clip: page.locator(".page-head, .page > .grid-2, .page > .field, .page > label.check"), pad: 8 });
  await clearHighlights(page);
  await addHook.click();
  await page.waitForURL((u) => /\/settings\/webhooks\/\d+$/.test(u.pathname));
  made.webhooks.push(Number(new URL(page.url()).pathname.split("/").pop()));
  await settle(page, { quiet: 800 });
  const test = panel(page, "Send a test");
  await test.getByRole("button", { name: "Send test" }).click();
  await test.locator(".alert").waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "09-webhook-test", { clip: page.locator("section.panel:has(h3:text-is('Signing secret')), section.panel:has(h3:text-is('Send a test'))"), pad: 8 });
  await go(page, "/settings/webhooks");
  await page.getByRole("link", { name: "Purchases to CRM" }).waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "09-webhooks-list", { clip: page.locator(".page-head, .page > .panel"), pad: 8 });
}
