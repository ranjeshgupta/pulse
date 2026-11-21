// Screenshots for docs/tutorials/18-contacts-behind-a-number.md, in the seeded docs workspace.
// Segment sizes as links, the "Spring catalogue" funnel (seeded by make docs-seed), the list opened
// from its "Opened" number with its chips and note, and "Create segment from this list" (Cancelled).
import { api, clearHighlights, go, highlight, session, settle, shot, confirmIfAsked } from "../lib.mjs";

const AREA = "tutorials";
const top = (height) => ({ x: 232, y: 0, width: 1440 - 232, height });

export default async function (browser) {
  const { context, page } = await session(browser);

  // 1. Segments: every size is a link.
  await go(page, "/segments");
  const size = page.locator("table a.count-link").first();
  await size.waitFor();
  await highlight(size);
  await shot(page, AREA, "18-segment-sizes", { clip: top(460) });
  await clearHighlights(page);

  // 2. A one-time campaign's funnel: the Opened number.
  const bc = (await api(context, "GET", "/v1/broadcasts")).broadcasts.find((b) => b.name === "Spring catalogue");
  if (!bc) throw new Error("no Spring catalogue campaign (run make docs-seed)");
  await page.setViewportSize({ width: 1440, height: 1600 });
  await go(page, `/broadcasts/${bc.id}`);
  const funnel = page.locator("section.panel", { has: page.locator(".fn-layout") }).first();
  await funnel.waitFor();
  const opened = funnel.locator(".fn-step", { has: page.locator(".fn-label", { hasText: /^Opened/ }) }).locator("a.count-link");
  await opened.waitFor();
  await funnel.scrollIntoViewIfNeeded();
  await settle(page, { quiet: 800 });
  await highlight(opened);
  await shot(page, AREA, "18-funnel", { clip: funnel, pad: 8 });
  await clearHighlights(page);

  // 3. The list behind it.
  await opened.click();
  await page.waitForURL((u) => u.pathname === "/contacts");
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByText(/^(About )?[\d,]+ contacts?$/).waitFor();
  await settle(page, { quiet: 800 });
  const create = page.getByRole("button", { name: "Create segment from this list" });
  await highlight(create);
  await shot(page, AREA, "18-list", { clip: top(520) });
  await clearHighlights(page);

  // 4. Create segment from this list (filled in, then Cancel).
  await create.click();
  const dialog = page.getByRole("dialog", { name: "Create segment from this list" });
  await dialog.getByLabel("Name").fill("Opened the Spring catalogue");
  await settle(page);
  await shot(page, AREA, "18-create-segment", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");

  // 5. A dashboard: Campaign performance, where each campaign's numbers link but the totals don't.
  await go(page, "/analytics/campaigns");
  await page.locator("table a.count-link").first().waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 800 });
  await highlight(page.locator("table a.count-link").first());
  await shot(page, AREA, "18-dashboard", { clip: top(640) });
  await clearHighlights(page);
  await context.close();
}
