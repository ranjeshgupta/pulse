// Screenshots for docs/tutorials/03-segments.md, in the seeded docs workspace (acme-retail).
// It builds the dynamic segment "Pune shoppers to thank" in the rule builder (deleting the one an
// earlier run made first), then "Gold skillet buyers, morning send" (choice and list fields, phone
// country, an Exclude group, refreshed daily; also replaced on each run) and an unsaved rule with
// date ranges, shows the static segment "Pune store opening guests" that tutorial 2's
// import created (run 02 first), checks that membership follows a tag change in real time, and
// shows Settings → Scoring (adding one rule, wishlist_added +5, kept on later runs) and Stages.
import { api, clearHighlights, go, highlight, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const DYNAMIC = "Pune shoppers to thank";
const GUESTS = "Pune store opening guests";
const DAILY = "Gold skillet buyers, morning send";

export default async function (browser) {
  const { context, page } = await session(browser);
  await list(page);
  await builder(context, page);
  await listsExcludeDaily(context, page);
  await dates(page);
  await staticSegment(context, page);
  await realTime(context);
  await scoringAndStages(page);
  await reachAndCopy(context, page);
  await context.close();
}

/** M3: who each channel reaches, Duplicate, and copying a group of filters (nothing saved). */
async function reachAndCopy(context, page) {
  const pro = (await api(context, "GET", "/v1/segments")).segments.find((s) => s.name === "Pro plan");
  await page.setViewportSize({ width: 1440, height: 1200 });
  await go(page, `/segments/${pro.id}`);
  const reach = page.locator(".reach-box");
  await reach.locator("table").waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "03-reach", { clip: reach, pad: 8 });
  const head = page.locator("section.fb-group.root > header");
  await highlight(page.getByRole("button", { name: "Duplicate" }));
  await highlight(head.getByRole("button", { name: "Copy" }));
  await highlight(head.getByRole("button", { name: "Paste" }));
  await shot(page, AREA, "03-copy-group", { clip: top(330) });
  await clearHighlights(page);
  await page.setViewportSize({ width: 1440, height: 900 });
}

const top = (height) => ({ x: 232, y: 0, width: 1440 - 232, height });

async function list(page) {
  await go(page, "/segments");
  const btn = page.getByRole("button", { name: "New segment" });
  await highlight(btn);
  await shot(page, AREA, "03-segments-list", { clip: top(560) });
  await clearHighlights(page);
  await btn.click();
  const dialog = page.getByRole("dialog", { name: "New segment" });
  await dialog.getByRole("radio", { name: /Dynamic/ }).click();
  await highlight(dialog.getByRole("button", { name: "Continue" }));
  await shot(page, AREA, "03-new-segment", { clip: dialog, pad: 8 });
  await clearHighlights(page);
  await dialog.getByRole("button", { name: "Continue" }).click();
  await page.waitForURL("**/segments/new");
}

/** Picks a filter in the open "Add a filter" picker by its label (your own fields show their type
 * after it, e.g. "Productslist"). */
async function pickFilter(page, search, label) {
  const picker = page.getByRole("dialog", { name: /Add a filter/ }).last();
  await picker.getByRole("combobox", { name: "Search filters" }).fill(search);
  await picker.locator(".fp-item", { has: page.locator(".fp-item-label", { hasText: new RegExp(`^${label}([a-z /]+)?$`) }) }).first().click();
}

async function builder(context, page) {
  // A rerun replaces the segment the previous run made.
  for (const s of (await api(context, "GET", "/v1/segments")).segments) {
    if (s.name === DYNAMIC && s.kind === "rule") await api(context, "DELETE", `/v1/segments/${s.id}`);
  }
  await page.setViewportSize({ width: 1440, height: 1200 });
  await settle(page);
  await page.getByLabel("Name").fill(DYNAMIC);
  const root = page.locator("section.fb-group.root");

  // 1. City is Pune. The picker, searched for "city", is a shot of its own.
  await root.locator(":scope > .fb-add").getByRole("button", { name: "+ Add filter" }).click();
  const picker = page.getByRole("dialog", { name: "Add a filter" });
  await picker.getByRole("combobox", { name: "Search filters" }).fill("city");
  await settle(page);
  await shot(page, AREA, "03-picker", { clip: picker, pad: 8 });
  await picker.locator(".fp-item", { has: page.locator(".fp-item-label", { hasText: /^City$/ }) }).first().click();
  await root.getByLabel("Value").first().fill("Pune");

  // 2. Did the event purchase at least 2 times in the last 30 days.
  await root.locator(":scope > .fb-add").getByRole("button", { name: "+ Add filter" }).click();
  await pickFilter(page, "did an event", "Did an event");
  const ev = root.locator(".fb-sentence").last();
  await ev.getByLabel("Event name").fill("purchase");
  await ev.getByLabel("Number of times").fill("2");

  // 3. A nested ANY group: tagged vip, or opened the Welcome email in the last 30 days.
  await root.locator(":scope > .fb-add").getByRole("button", { name: "+ Add group" }).click();
  await pickFilter(page, "tags", "Tags");
  const group = root.locator("section.fb-group").first();
  await group.getByLabel("Tag", { exact: true }).fill("vip");
  await group.locator(":scope > .fb-add").getByRole("button", { name: "+ Add filter" }).click();
  await pickFilter(page, "read a specific email", "Read a specific email");
  await group.getByLabel("Choose email").selectOption({ label: "Welcome email" });

  // The live count settles a moment after the last change.
  await page.locator(".preview-count[aria-busy=false]").waitFor();
  await settle(page, { quiet: 1200 });
  await shot(page, AREA, "03-builder", { clip: page.locator("main .page > *"), pad: 8 });

  const create = page.getByRole("button", { name: "Create segment" });
  await create.click();
  await page.waitForURL(/\/segments\/\d+$/);
  // Pulse fills a new segment in the background: Queued, then Live with its contacts.
  const id = page.url().split("/").pop();
  await until(async () => (await api(context, "GET", `/v1/segments/${id}`)).status === "ready", { timeout: 60_000, what: "the segment to go live" });
  await page.getByText("Live", { exact: true }).waitFor();
  await page.setViewportSize({ width: 1440, height: 900 });
  await settle(page, { quiet: 1500 });
  const view = page.getByRole("link", { name: "View contacts" });
  await highlight(view);
  await shot(page, AREA, "03-segment-saved", { clip: top(300) });
  await clearHighlights(page);
  await view.click();
  await page.waitForURL(/\/contacts\?q=segment/);
  await page.getByText(/^\d+ contacts?$/).waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "03-view-contacts", { clip: top(420) });
}

/** Loyalty tier (a choice field), Products (a list field), Phone country, an Exclude (NONE) group,
 * and a daily refresh; saved, so the list and the segment page show the refresh time. */
async function listsExcludeDaily(context, page) {
  for (const s of (await api(context, "GET", "/v1/segments")).segments) {
    if (s.name === DAILY && s.kind === "rule") await api(context, "DELETE", `/v1/segments/${s.id}`);
  }
  await page.setViewportSize({ width: 1440, height: 1300 });
  await go(page, "/segments/new");
  await page.getByLabel("Name").fill(DAILY);
  const root = page.locator("section.fb-group.root");
  const addFilter = () => root.locator(":scope > .fb-add").getByRole("button", { name: "+ Add filter" }).click();
  const row = () => root.locator(":scope > .fb-list > li > .fb-row").last();

  await addFilter();
  await pickFilter(page, "loyalty", "Loyalty tier");
  await row().getByLabel("Value", { exact: true }).selectOption("gold");
  await addFilter();
  await pickFilter(page, "products", "Products");
  await row().getByLabel("Value", { exact: true }).fill("KIT-101");
  await addFilter();
  await pickFilter(page, "phone country", "Phone country");
  await row().getByLabel("Value", { exact: true }).fill("IN");

  // Exclude (none of): people who bought in the last 7 days are left out.
  await root.locator(":scope > .fb-add").getByRole("button", { name: "+ Exclude (none of)" }).click();
  await pickFilter(page, "did an event", "Did an event");
  const none = root.locator("section.fb-group.none");
  await none.getByLabel("Event name").fill("purchase");
  await none.getByLabel("Days").fill("7");

  const refresh = page.locator("fieldset.seg-refresh");
  await refresh.getByLabel("Daily at a set time").check();
  await refresh.getByLabel("Refresh time").fill("06:00");
  await refresh.getByLabel("Time zone").selectOption("Asia/Kolkata");
  await page.locator(".preview-count[aria-busy=false]").waitFor();
  await blur(page);
  await settle(page, { quiet: 1200 });
  await shot(page, AREA, "03-lists-exclude", { clip: page.locator(".editor-split"), pad: 8 });
  await shot(page, AREA, "03-refresh", { clip: refresh, pad: 8 });

  await page.getByRole("button", { name: "Create segment" }).click();
  await page.waitForURL(/\/segments\/\d+$/);
  const id = page.url().split("/").pop();
  await until(async () => (await api(context, "GET", `/v1/segments/${id}`)).status === "ready", { timeout: 60_000, what: "the daily segment to be filled" });
  await page.getByText("Next refresh").waitFor();
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "03-daily-saved", { clip: page.locator(".editor-split > aside"), pad: 8 });

  await page.setViewportSize({ width: 1440, height: 900 });
  await go(page, "/segments");
  const tr = page.locator("tr", { hasText: DAILY });
  await tr.getByText(/Refreshed daily at/).waitFor();
  await shot(page, AREA, "03-daily-list", { clip: tr, pad: 4 });
}

/** Unsaved: Date added is between two dates, and an event "since" a date more than a year ago (the
 * sweep note). */
async function dates(page) {
  await page.setViewportSize({ width: 1440, height: 1100 });
  await go(page, "/segments/new");
  await page.getByLabel("Name").fill("Autumn sign-ups who bought before");
  const root = page.locator("section.fb-group.root");
  await root.locator(":scope > .fb-add").getByRole("button", { name: "+ Add filter" }).click();
  await pickFilter(page, "date added", "Date added");
  const row = root.locator(".fb-row").last();
  await row.getByLabel("Comparison").selectOption({ label: "is between" });
  await row.getByLabel("From", { exact: true }).fill("2026-09-01");
  await row.getByLabel("To", { exact: true }).fill("2026-11-30");
  await root.locator(":scope > .fb-add").getByRole("button", { name: "+ Add filter" }).click();
  await pickFilter(page, "did an event", "Did an event");
  const ev = root.locator(".fb-sentence").last();
  await ev.getByLabel("Event name").fill("purchase");
  await ev.getByLabel("When").selectOption("since");
  await ev.getByLabel("First day").fill("2025-01-01");
  await page.locator(".fb-sweep-note").waitFor();
  await blur(page);
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "03-dates", { clip: root, pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
}

async function staticSegment(context, page) {
  const seg = (await api(context, "GET", "/v1/segments")).segments.find((s) => s.kind === "static" && s.name === GUESTS);
  if (!seg) throw new Error(`no static segment "${GUESTS}": run SHOT=02 first`);
  await go(page, `/segments/${seg.id}`);
  await page.getByRole("textbox").first().fill("store-1002\nstore-1004");
  await highlight(page.getByRole("button", { name: /^Add 2 to segment/ }));
  await highlight(page.getByRole("link", { name: "Import CSV" }));
  await shot(page, AREA, "03-static", { clip: top(520) });
  await clearHighlights(page);
}

/** Tagging a contact vip puts it in the seeded "VIP tag" segment within seconds; removing the tag
 * takes it out again. Checked here, so the tutorial's claim stays true. */
async function realTime(context) {
  const vip = (await api(context, "GET", "/v1/segments")).segments.find((s) => s.name === "VIP tag");
  const inVip = async () => (await api(context, "GET", "/v1/contacts/store-1004")).segments.includes(vip.id);
  await api(context, "POST", "/v1/contacts/store-1004/tags", { add: ["vip"] });
  await until(inVip, { timeout: 15_000, what: "store-1004 to join VIP tag" });
  await api(context, "POST", "/v1/contacts/store-1004/tags", { remove: ["vip"] });
  await until(async () => !(await inVip()), { timeout: 15_000, what: "store-1004 to leave VIP tag" });
}

async function scoringAndStages(page) {
  await go(page, "/settings/scoring");
  if (!(await page.getByRole("cell", { name: "wishlist_added" }).count())) {
    await page.getByLabel("Event").fill("wishlist_added");
    await page.getByLabel("Points").fill("5");
    await page.getByRole("button", { name: "Save rule" }).click();
    await page.getByRole("cell", { name: "wishlist_added" }).waitFor();
  }
  await page.getByLabel("Event").fill("review_written");
  await page.getByLabel("Points").fill("15");
  await highlight(page.getByRole("button", { name: "Save rule" }));
  await shot(page, AREA, "03-scoring", { clip: page.locator("main .page > *"), pad: 8 });
  await clearHighlights(page);

  await go(page, "/settings/stages");
  await shot(page, AREA, "03-stages", { clip: page.locator("main .page > *"), pad: 8 });
}

/** Takes the focus off the field typed in last, so no caret or selection shows in the picture. */
async function blur(page) {
  await page.evaluate(() => document.activeElement?.blur());
}
