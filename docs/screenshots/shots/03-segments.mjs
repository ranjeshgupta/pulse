// Screenshots for docs/tutorials/03-segments.md, in the seeded docs workspace (acme-retail).
// It builds the dynamic segment "Pune shoppers to thank" in the rule builder (deleting the one an
// earlier run made first), shows the static segment "Pune store opening guests" that tutorial 2's
// import created (run 02 first), checks that membership follows a tag change in real time, and
// shows Settings → Scoring (adding one rule, wishlist_added +5, kept on later runs) and Stages.
import { api, clearHighlights, go, highlight, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const DYNAMIC = "Pune shoppers to thank";
const GUESTS = "Pune store opening guests";

export default async function (browser) {
  const { context, page } = await session(browser);
  await list(page);
  await builder(context, page);
  await staticSegment(context, page);
  await realTime(context);
  await scoringAndStages(page);
  await context.close();
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

/** Picks a filter in the open "Add a filter" picker by its label (exact). */
async function pickFilter(page, search, label) {
  const picker = page.getByRole("dialog", { name: /Add a filter/ }).last();
  await picker.getByRole("combobox", { name: "Search filters" }).fill(search);
  await picker.locator(".fp-item", { has: page.locator(".fp-item-label", { hasText: new RegExp(`^${label}$`) }) }).first().click();
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
