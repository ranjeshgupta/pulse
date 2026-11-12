// Screenshots for docs/tutorials/08-website-and-app-messages.md.
// Website: a "Newsletter slide-in" focus item (and the order drawer) and a "Sale banner" content slot
// with two variants (rules, a never-shown warning, the overlap count), both shown on the SDK
// test page. They only show on pages whose address contains "tutorial=8" (the slide-in) or that
// have a "sale-banner" slot (the test page gets one added on the fly, as your own page would have),
// so they don't appear in other guides' pictures of the test page. Mobile app: an in-app message
// editor with its phone preview, saved as a draft. At the end the slide-in is unpublished; the
// slot stays published (only a page with its slot shows it). A rerun deletes what earlier runs made.
import { api, clearHighlights, go, highlight, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const FOCUS = "Newsletter slide-in";
const SLOT_NAME = "Sale banner";
const SLOT = "sale-banner";
const INAPP = "Cart reminder in the app";
const GROUP = "Sale preview testers"; // also made by tutorial 7's script
const MEMBER = { id: "sale-tester-aarav", email: "sale.tester.aarav@example.com", name: "Aarav" };
const MARK = "tutorial=8";

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept());

  // Clean up earlier runs.
  for (const f of (await api(context, "GET", "/v1/focus-items")).focus_items) if (f.name === FOCUS) await api(context, "DELETE", `/v1/focus-items/${f.id}`);
  for (const d of (await api(context, "GET", "/v1/dwc")).slots) if (d.slot === SLOT) await api(context, "DELETE", `/v1/dwc/${d.id}`);
  for (const m of (await api(context, "GET", "/v1/inapp")).messages) if (m.name === INAPP) await api(context, "DELETE", `/v1/inapp/${m.id}`);

  // The static segment the slot's variant targets, with one test contact in it.
  const me = await api(context, "GET", "/v1/auth/me");
  const key = me.workspace.site_key;
  let group = (await api(context, "GET", "/v1/lists")).lists.find((l) => l.name === GROUP);
  if (!group) group = { id: (await api(context, "POST", "/v1/lists", { name: GROUP })).id };
  const sdk = await context.newPage();
  await go(sdk, `/sdk/demo.html?key=${key}`);
  await sdk.evaluate((m) => { Pulse.identify(m.id, { email: m.email, first_name: m.name }); Pulse.flush(); }, MEMBER);
  await sdk.waitForTimeout(1000);
  await sdk.close();
  await api(context, "POST", `/v1/lists/${group.id}/members`, { action: "add", external_ids: [MEMBER.id] });

  // 1. Campaigns → Website & app messages → New.
  await go(page, "/on-site");
  const newBtn = page.getByRole("button", { name: "New", exact: true });
  await highlight(newBtn);
  await shot(page, AREA, "08-list", { clip: page.locator(".page > *"), pad: 12 });
  await clearHighlights(page);
  await newBtn.click();
  const modal = page.getByRole("dialog", { name: "New on-site or in-app message" });
  await modal.getByRole("radio", { name: /^Website/ }).click();
  await settle(page);
  await shot(page, AREA, "08-new", { clip: modal, pad: 0 });

  // 2. A slide-in.
  await modal.getByRole("button", { name: /^Pop-up, banner bar or slide-in/ }).click();
  await page.waitForURL("**/on-site/focus/new");
  await settle(page, { quiet: 800 });
  await field(page, "Name").fill(FOCUS);
  await field(page, "Type").selectOption({ label: "Slide-in" });
  await field(page, "Headline").fill("Get 10% off your first order");
  const body = page.getByRole("textbox", { name: "Body", exact: true });
  await body.click();
  await page.keyboard.type("Join the newsletter for new arrivals and members-only sales.");
  await field(page, "Button text").fill("Sign me up");
  await field(page, "Button link").fill("https://demo.shop/newsletter");
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "08-focus-content", { clip: page.locator(".editor-split > div > :nth-child(-n+5), .editor-split > aside"), pad: 4 });

  // Targeting, trigger, frequency, schedule.
  await page.getByLabel("Never show to members of").selectOption({ label: "VIP customers" });
  await field(page, "Only on pages whose address contains").fill(MARK);
  await field(page, "Show it").selectOption({ label: "After a delay" });
  await field(page, "Seconds on page").fill("2");
  await field(page, "How often").selectOption({ label: "Once per visit" });
  await page.setViewportSize({ width: 1440, height: 1800 });
  await settle(page);
  await shot(page, AREA, "08-focus-targeting", { clip: page.locator("h3:has-text('Who sees it'), h3:has-text('Who sees it') ~ *:not(aside)"), pad: 8 });

  await page.setViewportSize({ width: 1440, height: 900 });
  // Publish, then check what a visitor sees.
  const publish = page.getByRole("button", { name: "Publish" });
  await highlight(publish);
  await shot(page, AREA, "08-focus-publish", { clip: page.locator(".page-head"), pad: 8 });
  await clearHighlights(page);
  await publish.click();
  await page.waitForURL((u) => /\/on-site\/focus\/\d+$/.test(u.pathname));
  await settle(page, { quiet: 800 });
  const focusPath = new URL(page.url()).pathname;

  // The order of pop-ups, bars and slide-ins (the list's button shows with two or more items).
  await go(page, "/on-site");
  const orderBtn = page.getByRole("button", { name: "Order of pop-ups, bars and slide-ins" });
  await highlight(orderBtn);
  await shot(page, AREA, "08-order-button", { clip: page.locator(".page > .row").first(), pad: 8 });
  await clearHighlights(page);
  await orderBtn.click();
  const drawer = page.locator(".drawer");
  await drawer.waitFor();
  const slides = drawer.locator("section.ov-kind", { has: page.getByRole("heading", { name: "Slide-ins" }) });
  await slides.getByRole("button", { name: "Check overlap" }).click();
  await slides.locator(".ov-result").waitFor();
  await settle(page, { quiet: 500 });
  await shot(page, AREA, "08-order-drawer", { clip: drawer, pad: 0 });
  await page.keyboard.press("Escape");

  await go(page, focusPath); // reopen: the check needs the saved item's id (see the tutorial's note)
  const check = page.locator("section.panel", { has: page.getByRole("heading", { name: "Check what a visitor sees" }) });
  await check.getByLabel("Page address").fill(`https://www.acme-retail.example/blog?${MARK}`);
  await check.getByRole("button", { name: "Check" }).click();
  await check.getByText("This visitor sees this focus item").waitFor();
  await shot(page, AREA, "08-focus-check", { clip: check, pad: 8 });

  // 3. It shows on the test page.
  const { context: anon, page: demo } = await session(browser, { signIn: null });
  await go(demo, `/sdk/demo.html?key=${key}&${MARK}`);
  await demo.locator("[data-pulse-focus]").waitFor({ state: "attached", timeout: 15_000 });
  await settle(demo, { quiet: 800 });
  await shot(demo, AREA, "08-focus-live");
  await anon.close();

  // 4. Dynamic web content: a "sale-banner" slot with a variant for the test group.
  await go(page, "/on-site");
  await page.getByRole("button", { name: "New", exact: true }).click();
  await modal.getByRole("radio", { name: /^Website/ }).click();
  await modal.getByRole("button", { name: /^Dynamic web content/ }).click();
  await page.waitForURL("**/on-site/content/new");
  await settle(page, { quiet: 800 });
  await field(page, "Name").fill(SLOT_NAME);
  await field(page, "Slot key").fill(SLOT);
  await page.setViewportSize({ width: 1440, height: 2600 });
  const card = (i) => page.locator("li.ov-card").nth(i);
  const segPick = (i, label) => card(i).locator(`select[aria-label="${label}"]`);
  // Variant 1: the testers. Variant 2: VIP customers on a phone.
  await page.getByRole("button", { name: "Add a variant" }).click();
  await card(0).getByLabel("Variant 1 name").fill("Sale testers");
  await segPick(0, "Show to members of any of").selectOption({ label: GROUP });
  await page.getByRole("textbox", { name: "Variant 1 content", exact: true }).click();
  await page.keyboard.type("Testers: the weekend sale is open to you a day early. Use code PREVIEW15.");
  await page.getByRole("button", { name: "Add a variant" }).click();
  await card(1).getByLabel("Variant 2 name").fill("VIPs on their phone");
  await segPick(1, "Show to members of any of").selectOption({ label: "VIP customers" });
  await card(1).getByRole("checkbox", { name: "Mobile" }).check();
  await page.getByRole("textbox", { name: "Variant 2 content", exact: true }).click();
  await page.keyboard.type("VIPs: tap to see your early-access sale picks.");
  await page.getByRole("textbox", { name: "Everyone else sees", exact: true }).click();
  await page.keyboard.type("Weekend sale: up to 30% off home and kitchen, until Sunday night.");
  await page.getByRole("heading", { name: "Variants" }).click();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "08-dwc", { clip: page.locator(".page-head, .page > .grid-2, .page > h3:text-is('Variants'), .page > h3:text-is('Variants') + p, li.ov-card:nth-child(1)"), pad: 8 });
  await shot(page, AREA, "08-dwc-rules", { clip: card(1), pad: 6 });
  // A third variant that can never show: everyone it targets matches variant 1 first.
  await page.getByRole("button", { name: "Add a variant" }).click();
  await card(2).getByLabel("Variant 3 name").fill("Testers on their phone");
  await segPick(2, "Show to members of any of").selectOption({ label: GROUP });
  await card(2).getByRole("checkbox", { name: "Mobile" }).check();
  await card(2).getByText(/^Never shown/).waitFor();
  await settle(page, { quiet: 500 });
  await shot(page, AREA, "08-dwc-never-shown", { clip: card(2).locator(".panel-head, .ov-who, .alert"), pad: 8 });
  await card(2).getByRole("button", { name: "Remove", exact: true }).first().click();
  // How the variants split the contacts.
  const ov = page.locator("section.ov-overlap");
  await ov.getByRole("button", { name: "Check overlap" }).click();
  await ov.locator(".ov-result").waitFor();
  await settle(page, { quiet: 500 });
  await shot(page, AREA, "08-dwc-overlap", { clip: ov, pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole("button", { name: "Publish" }).click();
  await page.waitForURL((u) => /\/on-site\/content\/\d+$/.test(u.pathname));
  await settle(page, { quiet: 800 });
  const dcheck = page.locator("section.panel", { has: page.getByRole("heading", { name: "Check what a visitor sees" }) });
  await dcheck.getByLabel("Contact's external ID").fill(MEMBER.id);
  await dcheck.getByLabel("Page address").fill("https://www.acme-retail.example/");
  await dcheck.getByRole("button", { name: "Check" }).click();
  await dcheck.locator("iframe").waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "08-dwc-check", { clip: dcheck, pad: 8 });

  // On the test page, with a sale-banner slot added below the existing one: an anonymous visitor,
  // then the test contact (the SDK asks for content when the page loads, so it reloads).
  // A fresh browser profile, so the visitor starts anonymous.
  const { context: visitor, page: page2 } = await session(browser, { signIn: null });
  await page2.route("**/sdk/demo.html*", async (route) => {
    const res = await route.fetch();
    const html = (await res.text()).replace(
      '<div class="slot" data-pulse-slot="home-hero">',
      `<div class="slot" data-pulse-slot="${SLOT}"><em>Sale banner goes here</em></div>\n  <div class="slot" data-pulse-slot="home-hero">`);
    await route.fulfill({ response: res, body: html });
  });
  await go(page2, `/sdk/demo.html?key=${key}`);
  const slot = page2.locator(`[data-pulse-slot="${SLOT}"]`);
  await slot.getByText("Weekend sale: up to 30% off").waitFor();
  await settle(page2, { quiet: 500 });
  await shot(page2, AREA, "08-dwc-anonymous", { clip: page2.locator("h1, h1 ~ p:first-of-type, .slot"), pad: 12 });
  await page2.evaluate((id) => { Pulse.identify(id); Pulse.flush(); }, MEMBER.id);
  await page2.reload();
  await slot.getByText("Testers: the weekend sale").waitFor();
  await settle(page2, { quiet: 500 });
  await shot(page2, AREA, "08-dwc-member", { clip: page2.locator("h1, h1 ~ p:first-of-type, .slot"), pad: 12 });
  await visitor.close();

  // 5. Mobile app: an in-app message (saved as a draft; it renders in the app SDKs).
  await go(page, "/on-site");
  await page.getByRole("button", { name: "New", exact: true }).click();
  await modal.getByRole("radio", { name: /^Mobile app/ }).click();
  await page.waitForURL("**/on-site/in-app/new");
  await settle(page, { quiet: 800 });
  await field(page, "Name").fill(INAPP);
  await field(page, "Title").fill("Still thinking it over?");
  await field(page, "Message").fill("Your cart is saved. Finish your order today and delivery is free.");
  await page.getByLabel("Button 1 label").fill("Not now");
  await page.getByRole("button", { name: "Add a button" }).click();
  await page.getByLabel("Button 2 label").fill("View cart");
  await page.getByLabel("Button 2 action").selectOption({ label: "Opens a link" });
  await page.getByLabel("Button 2 link").fill("acmeretail://cart");
  await page.getByLabel("Show to members of any of").selectOption({ label: "Abandoned cart" });
  await field(page, "Show it when this happens").fill("screen_view");
  await field(page, "On this screen (optional)").fill("cart");
  await field(page, "Wait before showing").fill("3");
  await field(page, "How often").selectOption({ label: "At most once a day" });
  // The editor with the pop-up layout, then each layout's phone preview.
  await field(page, "Layout").selectOption({ label: "Pop-up" });
  await page.getByRole("heading", { name: "Schedule" }).click();
  await settle(page, { quiet: 800 });
  await page.setViewportSize({ width: 1440, height: 1900 });
  await settle(page, { quiet: 500 });
  await shot(page, AREA, "08-inapp", { clip: page.locator(".page-head, .editor-split"), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const [layout, name] of [["Pop-up", "popup"], ["Banner", "banner"], ["Full screen", "fullscreen"]]) {
    await field(page, "Layout").selectOption({ label: layout });
    await settle(page, { quiet: 400 });
    await shot(page, AREA, `08-inapp-${name}`, { clip: page.locator(".ia-phone"), pad: 4 });
  }
  await field(page, "Layout").selectOption({ label: "Pop-up" });
  await page.getByRole("button", { name: "Save draft" }).click();
  await page.waitForURL((u) => /\/on-site\/in-app\/\d+$/.test(u.pathname));
  await settle(page, { quiet: 800 });
  const tryIt = page.locator("section.section", { has: page.getByRole("heading", { name: "Try it with a contact" }) });
  await tryIt.getByLabel("Contact external ID").fill("cust-0042");
  await tryIt.getByRole("button", { name: "See what they get" }).click();
  await tryIt.getByText(/They'd get|No live in-app message/).waitFor();
  await page.waitForTimeout(4500); // let the "Saved as draft" note fade
  await shot(page, AREA, "08-inapp-try", { clip: tryIt, pad: 8 });

  // The list with all three, then unpublish the slide-in.
  await go(page, "/on-site");
  await shot(page, AREA, "08-list-after", { clip: page.locator(".page .panel"), pad: 8 });
  const focus = (await api(context, "GET", "/v1/focus-items")).focus_items.find((f) => f.name === FOCUS);
  await go(page, `/on-site/focus/${focus.id}`);
  await page.getByRole("button", { name: "Unpublish" }).click();
  await until(async () => (await api(context, "GET", `/v1/focus-items/${focus.id}`)).status === "draft", { what: "the slide-in to be unpublished" });
  await context.close();
}

/** The control of a form field by its exact label (the label wraps the control, so its text runs on). */
function field(page, label) {
  const exact = new RegExp(`^${label.replace(/[()?]/g, "\\$&")}$`);
  return page.locator("label.field", { has: page.locator(":scope > span", { hasText: exact }) }).locator("input, select, textarea").first();
}
