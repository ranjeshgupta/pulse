// Screenshots for docs/tutorials/02-contacts.md, in the seeded docs workspace (acme-retail).
// The script follows the tutorial: it defines two contact fields, imports
// docs/tutorials/files/sample-contacts.csv into the static segment "Pune store opening guests",
// then works with one imported contact (store-1001), the filters, duplicates and companies.
// Everything it creates stays (it's what the tutorial creates) and is reused on later runs:
// the fields are saved with PUT, the import updates the same contacts, and the static segment is
// created only the first time.
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { api, clearHighlights, go, highlight, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const CSV = resolve(dirname(fileURLToPath(import.meta.url)), "../../tutorials/files/sample-contacts.csv");
const GUESTS = "Pune store opening guests";

export default async function (browser) {
  const { context, page } = await session(browser);
  await contactFields(context, page);
  await importCsv(context, page);
  await list(page);
  await profile(context, page);
  await contactPage(context, page);
  await filters(page);
  await duplicates(page);
  await companies(page);
  await context.close();
}

/** The page header and the first panel below it: most list pages read well cropped like this. */
const top = (page, height = 520) => ({ x: 232, y: 0, width: 1440 - 232, height });

async function contactFields(context, page) {
  await go(page, "/settings/attributes");
  const define = page.getByRole("button", { name: "Define field" });
  await highlight(define);
  await shot(page, AREA, "02-contact-fields", { clip: page.locator("main .page > *") });
  await clearHighlights(page);

  await define.click();
  const dialog = page.getByRole("dialog", { name: /Define attribute|Edit lifetime_value/ });
  await dialog.getByLabel("Attribute key").fill("lifetime_value");
  await dialog.getByLabel("Label").fill("Lifetime value");
  await dialog.getByLabel("Type").selectOption("number");
  await dialog.getByLabel("Description").fill("Total spent in our stores, in rupees");
  await settle(page);
  await shot(page, AREA, "02-define-field", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Save attribute" }).click();
  await dialog.waitFor({ state: "detached" });
  // A choice field (seeded: loyalty_tier, Bronze / Silver / Gold): its options, then Cancel.
  const tier = page.locator("tr", { has: page.locator("code", { hasText: /^loyalty_tier$/ }) });
  await page.setViewportSize({ width: 1440, height: 1400 });
  await tier.getByRole("button", { name: "Edit" }).click();
  const edit = page.getByRole("dialog", { name: "Edit loyalty_tier" });
  await edit.getByText("Options").waitFor();
  await settle(page);
  await shot(page, AREA, "02-define-choice", { clip: edit, pad: 0 });
  await edit.getByRole("button", { name: "Cancel" }).click();
  await edit.waitFor({ state: "detached" });
  await page.setViewportSize({ width: 1440, height: 900 });
  // The second field the sample file needs, saved the same way (PUT is idempotent).
  await api(context, "PUT", "/v1/attributes/loyalty_member", { key: "loyalty_member", label: "Loyalty member", type: "boolean", pii: false, description: "Joined the in-store loyalty programme" });
}

async function importCsv(context, page) {
  await go(page, "/contacts");
  const imp = page.getByRole("link", { name: "Import", exact: true });
  await highlight(imp);
  await shot(page, AREA, "02-contacts-list", { clip: top(page, 640) });
  await clearHighlights(page);

  await imp.click();
  await page.getByRole("heading", { name: "Import contacts" }).waitFor();
  await page.getByLabel("CSV file").setInputFiles(CSV);
  await page.getByText("sample-contacts.csv").first().waitFor();
  await settle(page);
  const wizard = page.locator("section.panel").first();
  await highlight(page.getByRole("button", { name: "Next" }));
  await shot(page, AREA, "02-import-upload", { clip: page.locator("main .page > .page-head, main .page > section.panel") });
  await clearHighlights(page);

  // 2. Map columns: the guesses are right except "notes", which we don't import.
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByLabel("Import notes as").selectOption("");
  await settle(page);
  await shot(page, AREA, "02-import-map", { clip: wizard, pad: 0 });

  // 3. Static segment: a new one the first time, the same one on later runs.
  await page.getByRole("button", { name: "Next" }).click();
  const exists = (await api(context, "GET", "/v1/segments")).segments.some((s) => s.kind === "static" && s.name === GUESTS);
  await page.getByLabel("Create a new static segment").check();
  await page.getByLabel("New static segment name").fill(GUESTS);
  await settle(page);
  await shot(page, AREA, "02-import-segment", { clip: wizard, pad: 0 });
  if (exists) {
    await page.getByLabel("Add them to a static segment").check();
    await page.getByLabel("Static segment", { exact: true }).selectOption({ label: GUESTS });
  }

  // 4. Review and start.
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByLabel(/These people agreed to hear from us/).check();
  const startBtn = page.getByRole("button", { name: "Start import" });
  await highlight(startBtn);
  await shot(page, AREA, "02-import-review", { clip: wizard, pad: 0 });
  await clearHighlights(page);
  const before = (await api(context, "GET", "/v1/imports")).imports.length;
  await startBtn.click();
  await until(async () => {
    const all = (await api(context, "GET", "/v1/imports")).imports;
    return all.length > before && ["done", "failed", "cancelled"].includes(all[0].status);
  }, { timeout: 60_000, what: "the import to finish" });
  await until(async () => (await api(context, "GET", "/v1/contacts?q=" + encodeURIComponent(`segment:"${GUESTS}"`) + "&limit=10")).count >= 6,
    { timeout: 60_000, what: "the imported contacts to join the static segment" });
  await go(page, "/contacts/import");
  const history = page.locator("section.section", { has: page.getByRole("heading", { name: "Import history" }) });
  await history.getByRole("button", { name: "Show errors" }).first().click();
  await settle(page);
  // The newest import and its errors (older runs of this script are further down).
  await shot(page, AREA, "02-import-history", { clip: history.locator("h3, thead, tbody > tr:nth-child(-n+2)"), pad: 4 });
}

async function list(page) {
  await go(page, "/contacts");
  await page.getByRole("button", { name: "Columns" }).click();
  const dialog = page.getByRole("dialog", { name: "Columns" });
  for (const c of ["Points", "Stage", "Tags"]) await dialog.getByLabel(c, { exact: false }).first().check();
  await settle(page);
  await shot(page, AREA, "02-columns", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Done" }).click();
}

async function profile(context, page) {
  const me = (await api(context, "GET", "/v1/auth/me")).user;
  await go(page, "/contacts?open=store-1001");
  const drawer = page.locator("aside.drawer");
  await drawer.waitFor();
  await drawer.getByText("Owner, stage and company").waitFor();
  const c = (await api(context, "GET", "/v1/contacts/store-1001")).contact;
  // Owner: me; stage: Lead; a tag; 25 points (only the first time, so reruns don't keep adding).
  if (String(c.attrs.owner_id ?? "") !== String(me.id)) await drawer.getByLabel("Owner").selectOption(String(me.id));
  if (!c.attrs.stage) await drawer.getByLabel("Stage").selectOption("Lead");
  const tags = Array.isArray(c.attrs.tags) ? c.attrs.tags : [];
  if (!tags.includes("gift-cards")) {
    await drawer.getByLabel("New tag").fill("gift-cards");
    await drawer.getByRole("button", { name: "Add tag" }).click();
  }
  if (!Number(c.attrs.points ?? 0)) {
    await drawer.getByLabel("Points to add").fill("25");
    await drawer.getByRole("button", { name: "Adjust" }).click();
  }
  await until(async () => {
    const a = (await api(context, "GET", "/v1/contacts/store-1001")).contact.attrs;
    return a.owner_id && a.stage && Number(a.points) > 0 && (a.tags ?? []).includes("gift-cards");
  }, { what: "store-1001's owner, stage, tag and points" });
  await page.setViewportSize({ width: 1440, height: 2600 });
  await go(page, "/contacts?open=store-1001");
  await drawer.getByText("Owner, stage and company").waitFor();
  await settle(page, { quiet: 1500 });
  const section = (name) => drawer.locator("section", { has: page.getByRole("heading", { name, exact: true }) });
  // Top: header, merge, segments, owner/stage/company, tags and points.
  await shot(page, AREA, "02-profile-top", { clip: drawer.locator(".drawer-head").or(section("Tags and points")), pad: 0 });
  await shot(page, AREA, "02-profile-consent", { clip: section("Messages they agreed to"), pad: 12 });
  // Attributes and Activity: from the Attributes heading to the sixth activity row.
  await shot(page, AREA, "02-profile-attributes", { clip: drawer.getByRole("heading", { name: "Attributes", exact: true }).or(drawer.locator(".drawer-body > table").last().locator("tr:nth-child(-n+6)")), pad: 8 });
  await listsAndWhy(context, page);
  await page.setViewportSize({ width: 1440, height: 900 });
}

/** A seeded customer with a loyalty tier, products bought and some segments: the "Lists and choices"
 * section, then "· why?" on one of its segments and "Why not in another segment?". */
async function listsAndWhy(context, page) {
  let ext = null;
  for (let i = 1; i <= 120 && !ext; i++) {
    const id = `cust-${String(i).padStart(4, "0")}`;
    const r = await api(context, "GET", `/v1/contacts/${id}`).catch(() => null);
    const a = r?.contact?.attrs ?? {};
    if (a.loyalty_tier && Array.isArray(a.products) && a.products.length > 1 && r.segments.length > 0) ext = id;
  }
  if (!ext) throw new Error("no seeded customer with a loyalty tier, products and segments (run make docs-seed)");
  await go(page, `/contacts?open=${ext}`);
  const drawer = page.locator("aside.drawer");
  const lists = drawer.locator("section.contact-fields");
  await lists.getByText("Loyalty tier").waitFor();
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "02-profile-lists", { clip: lists, pad: 8 });

  const segs = drawer.getByRole("heading", { name: "Segments", exact: true });
  await drawer.locator("button.seg-badge").first().click();
  await drawer.locator(".seg-why-rows li").first().waitFor();
  await settle(page);
  await shot(page, AREA, "02-segment-why", { clip: segs.or(drawer.locator(".seg-why")), pad: 8 });

  const other = drawer.getByLabel("Check another segment");
  const names = await other.locator("option").allTextContents();
  const pick = ["Repeat buyers in Pune", "Pro plan"].find((n) => names.includes(n)) ?? names[1];
  await other.selectOption({ label: pick });
  await drawer.locator(".seg-why", { hasText: pick }).locator(".seg-why-rows li").first().waitFor();
  await settle(page);
  await shot(page, AREA, "02-why-not", { clip: drawer.getByLabel("Check another segment").or(drawer.locator(".seg-why")), pad: 8 });
}

async function filters(page) {
  await go(page, "/contacts");
  const search = page.getByRole("combobox", { name: "Search and filter contacts" });
  await search.click();
  await search.fill("is:");
  await page.locator("#cv-suggestions").waitFor();
  await settle(page);
  await shot(page, AREA, "02-filter-suggest", { clip: page.locator(".cv-search-wrap, #cv-suggestions"), pad: 12 });

  await search.fill("tag:vip ");
  await search.fill("points:>50");
  await search.press("Enter");
  await page.getByRole("heading", { name: "Contacts", exact: true }).click();
  await page.getByText(/^\d+ contacts?$/).waitFor();
  await settle(page, { quiet: 800 });
  const save = page.getByRole("button", { name: "Save as segment" });
  await highlight(save);
  await shot(page, AREA, "02-filter-chips", { clip: top(page, 460) });
  await clearHighlights(page);

  await save.click();
  const dialog = page.getByRole("dialog", { name: "Save as segment" });
  await dialog.getByLabel("Name").fill("VIPs with over 50 points");
  await settle(page);
  await shot(page, AREA, "02-save-segment", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();

  const exp = page.getByRole("link", { name: "Export CSV" });
  await highlight(exp);
  await shot(page, AREA, "02-export", { clip: page.locator(".cv-search-wrap, .cv-bar"), pad: 12 });
  await clearHighlights(page);

  // Add filter: "Did an event" purchase in the last two weeks, then the chips it makes.
  await go(page, "/contacts");
  const addBtn = page.getByRole("button", { name: "Add filter", exact: true });
  await highlight(addBtn);
  await shot(page, AREA, "02-add-filter-button", { clip: page.locator(".cv-search-row"), pad: 12 });
  await clearHighlights(page);
  await addBtn.click();
  const menu = page.getByRole("dialog", { name: "Add a filter" });
  await menu.getByRole("button", { name: "Did an event", exact: true }).click();
  const fieldOf = (label) => menu.locator("label").filter({ has: page.locator(`xpath=./span[normalize-space(.)=${JSON.stringify(label)}]`) }).locator("input, select").first();
  await fieldOf("Event").selectOption("purchase");
  const day = (n) => new Date(Date.now() - n * 86_400_000).toISOString().slice(0, 10);
  await fieldOf("From").fill(day(13));
  await fieldOf("To").fill(day(0));
  await page.evaluate(() => document.activeElement?.blur());
  await settle(page);
  await shot(page, AREA, "02-add-filter", { clip: menu, pad: 0 });
  await menu.getByRole("button", { name: "Add filter", exact: true }).click();
  await page.getByText(/^(About )?[\d,]+ contacts?$/).waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "02-add-filter-chips", { clip: top(page, 300) });
}

async function duplicates(page) {
  await go(page, "/contacts/duplicates");
  await page.getByRole("heading", { name: "kabir.sethi@example.com" }).waitFor();
  const card = page.locator("section.dup-group", { has: page.getByRole("heading", { name: "kabir.sethi@example.com" }) });
  await card.getByLabel("Keep store-1006").check();
  await highlight(card.getByRole("button", { name: /^Merge into/ }));
  await shot(page, AREA, "02-duplicates", { clip: page.locator("main .page > .page-head, main .page > .tabs").or(card), pad: 8 });
  await clearHighlights(page);
}

async function companies(page) {
  await go(page, "/companies");
  await highlight(page.getByRole("button", { name: "New company" }));
  await shot(page, AREA, "02-companies", { clip: top(page, 420) });
  await clearHighlights(page);
  await page.getByRole("link", { name: "Northwind Traders" }).click();
  await page.getByText("Contacts at this company").waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "02-company", { clip: top(page, 640) });
}

/** The full contact page of a seeded customer whose "Spring catalogue" email bounced. */
async function contactPage(context, page) {
  const bc = (await api(context, "GET", "/v1/broadcasts")).broadcasts.find((b) => b.name === "Spring catalogue");
  if (!bc) throw new Error("no Spring catalogue campaign (run make docs-seed)");
  const q = encodeURIComponent(`campaign:broadcast/${bc.id} outcome:hard_bounced`);
  const who = (await api(context, "GET", `/v1/contacts?q=${q}&limit=5`)).contacts[0];
  if (!who) throw new Error("no hard bounce for Spring catalogue");
  const ext = encodeURIComponent(who.external_id);
  await page.setViewportSize({ width: 1440, height: 1500 });
  await go(page, `/contacts/${ext}`);
  await page.getByRole("heading", { name: "Reachability" }).waitFor();
  await page.locator(".cp-activity").waitFor();
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "02-contact-page", { clip: { x: 232, y: 0, width: 1440 - 232, height: 1100 } });
  await page.getByRole("tab", { name: "Messages" }).click();
  await page.locator("table.cp-messages tbody tr").first().waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "02-contact-messages", { clip: page.locator(".cp-main section.panel").first(), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
}
