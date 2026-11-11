// Screenshots for docs/tutorials/01-getting-started.md.
// Part 1 signs up a new "Acme Retail" workspace (its ID gets a suffix, as Acme Retail already
// exists) and follows the tutorial in it: workspace ready, empty overview, website snippet, the
// test page, the first contact. Part 2 uses the seeded docs workspace for Team and Roles, which
// look more like a real team there.
import { api, clearHighlights, go, highlight, maskSecrets, runId, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";

export default async function (browser) {
  await newWorkspace(browser);
  await teamAndRoles(browser);
}

async function newWorkspace(browser) {
  const { context, page } = await session(browser, { signIn: null });

  // 1. Sign up. The password is a throwaway test value for this run's workspace.
  await go(page, "/signup");
  await page.getByLabel("Company or team name").fill("Acme Retail");
  await page.getByLabel("Your name").fill("Asha Rao");
  await page.getByLabel("Work email").fill(`asha.rao+${runId()}@acme-retail.test`);
  await page.getByLabel("Password").fill(`docs-run-${runId()}-pass`);
  const create = page.getByRole("button", { name: "Create workspace" });
  await highlight(create);
  await shot(page, AREA, "01-signup", { clip: page.locator(".auth-form > *"), pad: 24 });
  await clearHighlights(page);

  await create.click();
  await page.getByRole("heading", { name: "Your workspace is ready" }).waitFor();
  await maskSecrets(page);
  await shot(page, AREA, "01-workspace-ready", { clip: page.locator(".auth-form > *"), pad: 24 });

  await page.getByRole("button", { name: "Open Pulse" }).click();
  await page.waitForURL((u) => u.pathname === "/");
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "01-overview-empty");

  // 3. The sidebar.
  await shot(page, AREA, "01-navigation", { clip: page.locator("nav.sidebar"), pad: 0 });

  // 6. Settings → Sign-in & keys → Your website.
  await go(page, "/settings/access");
  const website = page.locator("section.panel", { has: page.getByRole("heading", { name: "Your website" }) });
  await highlight(website.getByRole("link", { name: "Open the test page" }));
  await shot(page, AREA, "01-website-snippet", { clip: website, pad: 8 });
  await clearHighlights(page);

  // 7. The test page. Like a real visitor, it browses for a moment (the SDK records the anonymous
  // page view) before identifying as cust-0001 and tracking a purchase.
  const me = await api(context, "GET", "/v1/auth/me");
  const anonymous = async () => (await api(context, "GET", "/v1/contacts?q=is%3Aanonymous&limit=10")).contacts.length;
  const test = await context.newPage();
  await go(test, `/sdk/demo.html?key=${me.workspace.site_key}`);
  await until(anonymous, { what: "the anonymous visitor to appear" });
  await test.locator("#uid").fill("cust-0001");
  await test.getByRole("button", { name: "Identify" }).click();
  await test.getByRole("button", { name: "Track purchase" }).click();
  await test.getByText("Events are recorded as: cust-0001").waitFor();
  await settle(test, { quiet: 1500 });
  await shot(test, AREA, "01-test-page", { clip: test.locator("body > *"), pad: 16 });
  await test.close();

  // The contact shows up within seconds, and the anonymous visitor is merged into it.
  await until(async () => (await anonymous()) === 0, { what: "the anonymous visitor to be merged" });
  const row = page.getByRole("row", { name: /cust-0001/ });
  await until(async () => { await go(page, "/contacts"); return row.count(); }, { what: "cust-0001 in the list" });
  await row.first().waitFor();
  await shot(page, AREA, "01-contacts-first");

  const search = page.getByRole("combobox", { name: "Search and filter contacts" });
  await search.fill("is:anonymous");
  await search.press("Enter");
  await search.press("Escape");
  await page.getByRole("heading", { name: "Contacts", exact: true }).click();
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "01-contacts-anonymous", { clip: { x: 0, y: 0, width: 1440, height: 420 } });

  await page.getByRole("button", { name: /^Remove filter/ }).first().click();
  await settle(page);
  await row.first().click();
  const drawer = page.locator("aside.drawer");
  await drawer.waitFor();
  // A tall window shows the whole profile; the shot is the profile panel only.
  await page.setViewportSize({ width: 1440, height: 2400 });
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "01-contact-profile", { clip: page.locator("aside.drawer .drawer-head, aside.drawer .drawer-body > *"), pad: 0 });
  await page.setViewportSize({ width: 1440, height: 900 });

  await go(page, "/");
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "01-overview-live");
  await context.close();
}

async function teamAndRoles(browser) {
  const { context, page } = await session(browser);

  // 4. Team, and the Add teammate form (filled in, not sent).
  await go(page, "/settings/team");
  const add = page.getByRole("button", { name: "Add teammate" });
  await highlight(add);
  await shot(page, AREA, "01-team");
  await clearHighlights(page);
  await add.click();
  const dialog = page.getByRole("dialog", { name: "Add a teammate" });
  await dialog.getByLabel("Email").fill("rahul.verma@acme-retail.test");
  await dialog.getByLabel("Name").fill("Rahul Verma");
  await dialog.getByLabel("Role").selectOption("editor");
  await settle(page);
  await shot(page, AREA, "01-add-teammate", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();

  // 5. Roles, and a new "Content writer" role (filled in, not saved).
  await go(page, "/settings/roles");
  const newRole = page.getByRole("button", { name: "New role" });
  await highlight(newRole);
  await shot(page, AREA, "01-roles");
  await clearHighlights(page);
  await newRole.click();
  await settle(page);
  await page.getByLabel("Name").first().fill("Content writer");
  await page.getByLabel("Description").first().fill("Writes templates, pages and forms; reads reports");
  const levels = { Contacts: "None", Segments: "None", Journeys: "None", Broadcasts: "None", Content: "Edit",
    Reports: "View", Channels: "None", Settings: "None" };
  for (const [area, level] of Object.entries(levels)) {
    await page.getByRole("group", { name: `${area} access` }).getByRole("button", { name: level, exact: true }).click();
  }
  await page.setViewportSize({ width: 1440, height: 1600 });
  await page.locator("aside.drawer .drawer-body").evaluate((el) => el.scrollTo(0, 0));
  await settle(page);
  await shot(page, AREA, "01-new-role", { clip: page.locator("aside.drawer .drawer-head, aside.drawer .drawer-body > *"), pad: 0 });
  await context.close();
}
