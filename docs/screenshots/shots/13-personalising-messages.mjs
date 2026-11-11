// Screenshots for docs/tutorials/13-personalising-messages.md, in the seeded docs workspace
// (acme-retail), which `make docs-seed` gives the Cart rescue templates (email, SMS, WhatsApp) and
// an active Cart rescue journey whose first step picks SMS for Indian numbers and WhatsApp otherwise.
// Nothing is saved: fields are typed into and left, dialogs are cancelled.
import { api, go, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept()); // "leave this page?" after typing into a template
  const templates = (await api(context, "GET", "/v1/templates")).templates;
  const byName = (n) => {
    const t = templates.find((x) => x.name === n);
    if (!t) throw new Error(`seeded template ${n} is missing: run make docs-seed`);
    return t.id;
  };
  await picker(page, byName("Cart rescue SMS"));
  await email(page, byName("Cart rescue email"));
  await testSend(page, byName("Cart rescue SMS"));
  await rules(page, context);
  await context.close();
}

/** Chooses the event whose properties {{event.x}} offers (remembered by the browser). */
async function scope(page) {
  await page.getByLabel("Trigger event for event variables").selectOption("cart_abandoned");
  await settle(page);
}

async function picker(page, id) {
  await page.setViewportSize({ width: 1440, height: 1100 });
  await go(page, `/templates/${id}`);
  await scope(page);
  await shot(page, AREA, "13-toolbar", { clip: page.locator(".vp-toolbar"), pad: 6 });

  const field = page.locator(".vp-field").first();
  await field.locator(".vp-btn").click();
  const pop = page.getByRole("dialog", { name: "Insert a variable" });
  await pop.getByText("Trigger event", { exact: true }).waitFor();
  // Scroll the list so the end of Contact and the start of Trigger event both show.
  await pop.locator(".vp-list").evaluate((el) => { el.scrollTop = el.querySelectorAll(".vp-group")[1].offsetTop - el.offsetTop - 110; });
  await settle(page);
  await shot(page, AREA, "13-picker", { clip: field.or(pop), pad: 8 });

  await pop.getByRole("button", { name: /^Product name/ }).first().click();
  await pop.getByLabel("Format").selectOption({ label: "Shorten to 40 characters" });
  await settle(page);
  await shot(page, AREA, "13-picker-format", { clip: pop, pad: 4 });
  await page.keyboard.press("Escape");

  // Variables Pulse can't fill are listed under the field (typed, not saved).
  const text = field.locator("textarea");
  await text.fill("Hi {{contact.nick_name}}, use {{event.coupon}} on your {{event.product_name | truncate:30}}");
  await settle(page);
  await shot(page, AREA, "13-lint", { clip: field, pad: 8 });
}

async function email(page, id) {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await go(page, `/templates/${id}`);
  await page.getByRole("tab", { name: "HTML" }).click();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "13-email-template", { clip: page.locator("main .page > *").filter({ hasNot: page.locator(".toast, .alert") }), pad: 8 });

  // Preview as a real contact, with their latest cart_abandoned.
  await page.getByRole("button", { name: "Preview as contact" }).first().click();
  const dialog = page.getByRole("dialog", { name: /^Preview/ });
  await dialog.getByLabel("Preview as contact").fill("cust-0201");
  await dialog.getByRole("button", { name: "Preview as contact" }).click();
  await dialog.getByText(/The contact's latest cart_abandoned/).waitFor({ timeout: 20_000 });
  await dialog.getByText("As cust-0201").waitFor();
  await page.setViewportSize({ width: 1440, height: 1500 });
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "13-preview-event", { clip: dialog, pad: 0 });
  await page.keyboard.press("Escape");
  await page.setViewportSize({ width: 1440, height: 900 });
}

async function testSend(page, id) {
  await go(page, `/templates/${id}`);
  await page.getByRole("button", { name: "Send a test" }).click();
  const dialog = page.getByRole("dialog", { name: /^Send a test/ });
  await dialog.getByLabel("Contact's external ID").fill("cust-0202");
  await dialog.getByLabel("Event properties").waitFor({ timeout: 20_000 });
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "13-test-send", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
}

async function rules(page, context) {
  const j = (await api(context, "GET", "/v1/journeys")).journeys.find((x) => x.name === "Cart rescue");
  if (!j) throw new Error("the Cart rescue journey is missing: run make docs-seed");
  await until(async () => {
    const r = await api(context, "GET", `/v1/journeys/${j.id}/report`);
    const st = r.steps?.s1 ?? {};
    return (st["ch:sms"] ?? 0) + (st["ch:whatsapp"] ?? 0) > 0;
  }, { timeout: 60_000, what: "the Cart rescue step stats" });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await go(page, `/journeys/${j.id}`);
  await settle(page, { quiet: 3000 });
  await page.locator(".j-toolbar").getByRole("button", { name: "Zoom to fit" }).click();
  await settle(page, { quiet: 600 });
  const node = page.locator(".jnode", { hasText: "Send marketing message" }).first();
  await shot(page, AREA, "13-rules-stats", { clip: node, pad: 10 });
  await node.click();
  await page.locator("aside.inspector .j-rules").waitFor();
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "13-rules", { clip: page.locator("aside.inspector .field", { has: page.locator(".j-rules") }), pad: 8 });
}
