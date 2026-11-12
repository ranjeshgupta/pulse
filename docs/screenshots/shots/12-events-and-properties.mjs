// Screenshots for docs/tutorials/12-events-and-properties.md, in the seeded docs workspace
// (acme-retail), which `make docs-seed` gives the six suggested event schemas and 60 days of
// e-commerce events with properties (some of them not fitting their schema).
// Nothing is saved: the suggested-events and New event dialogs and the Enforce confirmation are cancelled, the
// segment builder and the journey Start are filled in but never saved.
import { clearHighlights, go, highlight, session, settle, shot } from "../lib.mjs";

const AREA = "tutorials";

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept()); // "leave this page?" when we walk away from unsaved editors
  await eventList(page);
  await overview(page);
  await schema(page);
  await problems(page);
  await sending(page);
  await segmentFilter(page);
  await journeyTrigger(page);
  await context.close();
}

const top = (height) => ({ x: 232, y: 0, width: 1440 - 232, height });
const tab = (page, name) => page.getByRole("tab", { name: new RegExp(`^${name}`) });

async function eventList(page) {
  await go(page, "/events");
  await page.getByRole("link", { name: "purchase", exact: true }).waitFor();
  await settle(page, { quiet: 800 });
  const add = page.getByRole("button", { name: "Add suggested events" });
  await highlight(add);
  await shot(page, AREA, "12-events-list", { clip: page.locator("main .page > *"), pad: 8 });
  await clearHighlights(page);

  await add.click();
  const dialog = page.getByRole("dialog", { name: "Add suggested events" });
  await dialog.getByText("Checkout abandoned").waitFor();
  await settle(page);
  await shot(page, AREA, "12-suggested", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await dialog.waitFor({ state: "detached" });

  await page.getByRole("button", { name: "New event" }).click();
  const create = page.getByRole("dialog", { name: "New event" });
  await create.getByLabel("Event name").fill("webinar_joined");
  await create.getByLabel("Label (optional)").fill("Joined a webinar");
  await create.getByLabel("Description (optional)").fill("Someone joined a live webinar.");
  for (const [i, key, type] of [[1, "topic", "Text"], [2, "duration_minutes", "Number"], [3, "source", "One of a list"]]) {
    await create.getByRole("button", { name: "Add property" }).click();
    await create.getByLabel(`Key of property ${i}`).fill(key);
    await create.getByLabel(`Type of ${key}`).selectOption({ label: type });
  }
  await create.getByLabel("topic is required").check();
  await create.getByLabel("Allowed values of source").fill("email, whatsapp, website");
  await settle(page);
  await shot(page, AREA, "12-new-event", { clip: create, pad: 0 });
  await create.getByRole("button", { name: "Cancel" }).click();
  await create.waitFor({ state: "detached" });
}

async function overview(page) {
  await page.setViewportSize({ width: 1440, height: 1500 });
  await go(page, "/events/purchase");
  await page.getByText(/From the latest \d/).waitFor();
  await page.locator("table code", { hasText: "items[].product_code" }).first().waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "12-event-volume", { clip: page.locator("main .page > .page-head, main .page > .tabs").or(page.locator("section.ev-section").first()), pad: 8 });
  await shot(page, AREA, "12-event-properties", { clip: page.locator("section.ev-section").nth(1), pad: 8 });
}

async function schema(page) {
  // signup: method (one of a list) and social_provider, which applies only when method is social.
  await page.setViewportSize({ width: 1440, height: 1700 });
  await go(page, "/events/signup");
  await tab(page, "Schema").click();
  await page.getByRole("heading", { name: "About this event" }).waitFor();
  await settle(page);
  await shot(page, AREA, "12-schema-about", { clip: page.locator("section.ev-section").first(), pad: 8 });
  await shot(page, AREA, "12-schema-dependent", { clip: page.locator("li.ev-prop").nth(1), pad: 4 });
  await shot(page, AREA, "12-schema-enum", { clip: page.locator("li.ev-prop").first(), pad: 4 });

  // Enforce asks for ENFORCE first; cancelled here, so nothing changes.
  await page.getByRole("radio", { name: "Enforce" }).click();
  const dialog = page.getByRole("dialog", { name: /^Enforce the schema of signup/ });
  await dialog.getByLabel("Type ENFORCE to confirm").fill("ENFORCE");
  await settle(page);
  await shot(page, AREA, "12-enforce", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await dialog.waitFor({ state: "detached" });

  // purchase: the items[] properties, one of them added to the contact's list field "products".
  await go(page, "/events/purchase");
  await tab(page, "Schema").click();
  await page.getByRole("heading", { name: "About this event" }).waitFor();
  const item = page.locator("li.ev-prop", { has: page.locator(".ev-prop-head code", { hasText: /^item_product_code$/ }) });
  await item.scrollIntoViewIfNeeded();
  await settle(page);
  await shot(page, AREA, "12-schema-items", { clip: item, pad: 4 });
}

async function problems(page) {
  // purchase: a few seeded orders sent their value as text ("3198") instead of a number.
  await page.setViewportSize({ width: 1440, height: 1600 });
  await go(page, "/events/purchase");
  await tab(page, "Problems").click();
  await page.getByRole("heading", { name: "Most common problems" }).waitFor();
  await settle(page);
  await shot(page, AREA, "12-problems", { clip: page.locator(".ev-stats").or(page.locator("section.ev-section", { has: page.getByRole("heading", { name: "By day" }) })), pad: 8 });
  await shot(page, AREA, "12-problem-samples", { clip: page.locator("section.ev-section", { has: page.getByRole("heading", { name: /^(Most common problems|Latest events with problems)$/ }) }), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
}

async function sending(page) {
  await go(page, "/events/signup");
  await tab(page, "Sending this event").click();
  await page.getByText("From your website (web SDK)").waitFor();
  await settle(page);
  await shot(page, AREA, "12-sending", { clip: page.locator("section.ev-section"), pad: 8 });
}

/** A dynamic segment's event filter: purchase where items[].product_code is KIT-101, between two dates. */
async function segmentFilter(page) {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await go(page, "/segments/new");
  await page.getByLabel("Name").fill("Skillet buyers in October");
  const root = page.locator("section.fb-group.root");
  await root.locator(":scope > .fb-add").getByRole("button", { name: "+ Add filter" }).click();
  const picker = page.getByRole("dialog", { name: /Add a filter/ }).last();
  await picker.getByRole("combobox", { name: "Search filters" }).fill("did an event");
  await picker.locator(".fp-item", { has: page.locator(".fp-item-label", { hasText: /^Did an event$/ }) }).first().click();
  const ev = root.locator(".fb-sentence").last();
  await ev.getByLabel("Event name").fill("purchase");
  await ev.getByLabel("When").selectOption("between");
  await ev.getByLabel("First day").fill(isoDaysAgo(40));
  await ev.getByLabel("Last day").fill(isoDaysAgo(10));
  await ev.getByRole("button", { name: "+ Only count events where…" }).click();
  const where = ev.locator(".fb-where").last();
  await where.getByLabel("Event property").fill("items[].product_code");
  await where.getByLabel("Value", { exact: true }).fill("KIT-101");
  await page.locator(".preview-count[aria-busy=false]").waitFor();
  await blur(page);
  await settle(page, { quiet: 1200 });
  await shot(page, AREA, "12-segment-items", { clip: page.locator(".editor-split"), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
}

/** A triggered campaign's Start: checkout_abandoned, only when payment_method is upi and cart_value is at least 500. */
async function journeyTrigger(page) {
  await page.setViewportSize({ width: 1440, height: 1200 });
  await go(page, "/journeys/new?start=event");
  const insp = page.locator("aside.inspector");
  await insp.getByLabel("Start this campaign for a contact when they").selectOption("event");
  await insp.getByLabel("Event name").fill("checkout_abandoned");
  const more = insp.getByRole("button", { name: "+ Only when the event's properties match…" });
  await more.click();
  let row = insp.locator(".fb-where").nth(0);
  await row.getByLabel("Event property").fill("payment_method");
  await row.getByLabel("Value", { exact: true }).selectOption("upi");
  await more.click();
  row = insp.locator(".fb-where").nth(1);
  await row.getByLabel("Event property").fill("cart_value");
  await row.getByLabel("Comparison").selectOption({ label: "is at least" });
  await row.getByLabel("Value", { exact: true }).fill("500");
  await blur(page);
  await settle(page);
  await shot(page, AREA, "12-journey-trigger", { clip: insp.locator(".insp-head").first().or(insp.locator(".insp-where")), pad: 6 });
}

function isoDaysAgo(n) {
  return new Date(Date.now() - n * 86_400_000).toISOString().slice(0, 10);
}

/** Takes the focus off the field typed in last, so no caret or selection shows in the picture. */
async function blur(page) {
  await page.evaluate(() => document.activeElement?.blur());
}
