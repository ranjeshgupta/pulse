// Screenshots for docs/tutorials/15-delivery-and-dashboards.md: the seeded "Spring catalogue" email
// (delivered, bounced, complained, opened, clicked: `make docs-seed`) and "Monsoon preview" WhatsApp
// campaign reports (funnel and reasons), the contacts list a funnel number opens, a contact's
// Messages tab, and the Campaign performance, Users, Products and Live events dashboards. Nothing
// is saved; the live feed is fed by a few events from the SDK test page.
import { api, clearHighlights, go, highlight, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";

export default async function (browser) {
  const { context, page } = await session(browser);
  try {
    await run(browser, context, page);
  } finally {
    await context.close();
  }
}

async function run(browser, context, page) {
  const all = (await api(context, "GET", "/v1/broadcasts")).broadcasts;
  const spring = all.find((b) => b.name === "Spring catalogue");
  const monsoon = all.find((b) => b.name === "Monsoon preview");
  if (!spring || !monsoon) throw new Error("run `make docs-seed`: the Spring catalogue and Monsoon preview campaigns are missing");

  // ---- A campaign's funnel and reasons ----
  await page.setViewportSize({ width: 1440, height: 2400 });
  await go(page, `/broadcasts/${spring.id}`);
  const funnel = page.locator("section.panel", { has: page.getByRole("heading", { name: "Funnel", exact: true }) });
  await funnel.waitFor();
  await settle(page, { quiet: 1200 });
  await shot(page, AREA, "15-funnel", { clip: funnel, pad: 8 });
  const reasons = page.locator("section.panel", { has: page.getByRole("heading", { name: "Why messages didn't go out", exact: true }) });
  await shot(page, AREA, "15-reasons", { clip: reasons, pad: 8 });

  // A number opens its contacts: Bounced.
  const link = funnel.locator(".fn-figs > div", { has: page.locator("dt", { hasText: /^Bounced$/ }) }).locator("a.count-link");
  await highlight(link);
  await shot(page, AREA, "15-funnel-link", { clip: funnel, pad: 8 });
  await clearHighlights(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await link.click();
  await page.waitForURL("**/contacts?**");
  await settle(page, { quiet: 1200 });
  await shot(page, AREA, "15-contacts-list", { clip: page.locator(".page"), pad: 0 });

  // The first contact's Messages tab.
  const ext = (await page.locator(".page table tbody tr").first().locator("td").first().innerText()).trim();
  await go(page, `/contacts/${encodeURIComponent(ext)}`);
  await page.getByRole("tab", { name: "Messages" }).click().catch(async () => page.getByRole("button", { name: "Messages", exact: true }).click());
  await page.getByText("Newest first; the status is the furthest it got").waitFor();
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "15-contact-messages", { clip: page.locator("section.panel", { has: page.getByText("Newest first; the status is the furthest it got") }), pad: 8 });

  // The WhatsApp campaign: read receipts and reasons in words.
  await page.setViewportSize({ width: 1440, height: 2400 });
  await go(page, `/broadcasts/${monsoon.id}`);
  const wf = page.locator("section.panel", { has: page.getByRole("heading", { name: "Funnel", exact: true }) });
  await wf.waitFor();
  await settle(page, { quiet: 1200 });
  const wr = page.locator("section.panel", { has: page.getByRole("heading", { name: "Why messages didn't go out", exact: true }) });
  await shot(page, AREA, "15-whatsapp", { clip: wf, pad: 8 });
  await shot(page, AREA, "15-whatsapp-reasons", { clip: wr, pad: 8 });

  // ---- Campaign performance ----
  await page.setViewportSize({ width: 1440, height: 1600 });
  await go(page, "/analytics/campaigns");
  await page.locator(".cdash-figures").waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "15-campaign-performance", { clip: await top(page, 1000) });
  const toggle = page.getByRole("button", { name: "Show failures by reason for Spring catalogue" });
  await toggle.click();
  await settle(page, { quiet: 600 });
  const row = page.locator("tr", { has: toggle });
  await shot(page, AREA, "15-campaign-row", { clip: page.locator("tr", { has: page.getByRole("button", { name: /failures by reason for Spring catalogue/ }) }).locator("xpath=self::tr | following-sibling::tr[1]"), pad: 4 });
  void row;
  const why = page.locator("section.panel", { has: page.getByRole("heading", { name: "Failures by reason" }) });
  await shot(page, AREA, "15-failures-by-reason", { clip: why, pad: 8 });

  // ---- Users ----
  await page.setViewportSize({ width: 1440, height: 1700 });
  await go(page, "/analytics/users");
  await page.locator(".dash-figures, .figures").first().waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 1200 });
  await shot(page, AREA, "15-users", { clip: page.locator(".page"), pad: 0 });

  // ---- Products ----
  await page.setViewportSize({ width: 1440, height: 1900 });
  await go(page, "/analytics/products");
  await page.locator(".dash-figures").waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 1200 });
  await shot(page, AREA, "15-products", { clip: page.locator(".page"), pad: 0 });

  // ---- Live events: a visitor on the SDK test page sends a few events while it is open ----
  await page.setViewportSize({ width: 1440, height: 900 });
  await go(page, "/analytics/live");
  const me = await api(context, "GET", "/v1/auth/me");
  const { context: anon, page: demo } = await session(browser, { signIn: null });
  await go(demo, `/sdk/demo.html?key=${me.workspace.site_key}`);
  await demo.evaluate(() => {
    Pulse.identify("live-demo-1", { email: "live.demo@example.com", first_name: "Ira" });
    Pulse.track("product_viewed", { product_code: "SKL-10", product_category: "Kitchen", price: 1299 });
    Pulse.track("add_to_cart", { product_code: "SKL-10", product_category: "Kitchen", price: 1299 });
    Pulse.flush();
  });
  await until(async () => (await page.locator(".live-list li").count()) >= 3, { timeout: 30_000, what: "live events" });
  await anon.close();
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "15-live", { clip: await top(page, 640) });
}

/** The top `height` pixels of the page's main column. */
async function top(page, height) {
  const b = await page.locator(".page").boundingBox();
  return { x: b.x, y: b.y, width: b.width, height: Math.min(height, b.height) };
}
