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
  // Top: header, merge, segments, owner/stage/company, tags and points.
  await shot(page, AREA, "02-profile-top", { clip: drawer.locator(".drawer-head, .drawer-body > :nth-child(-n+5)"), pad: 0 });
  const consent = drawer.locator("section", { has: page.getByRole("heading", { name: "Messages they agreed to" }) });
  await shot(page, AREA, "02-profile-consent", { clip: consent, pad: 12 });
  // Attributes and Activity: the last four children of the drawer body.
  const n = await drawer.locator(".drawer-body > *").count();
  await shot(page, AREA, "02-profile-attributes", { clip: drawer.locator(`.drawer-body > :nth-child(n+${n - 3})`), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
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
