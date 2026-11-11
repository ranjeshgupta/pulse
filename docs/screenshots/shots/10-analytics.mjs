// Screenshots for docs/tutorials/10-analytics.md: Overview, Reports (presets, a line chart, the
// seeded "Revenue by category" bars, filters), Funnels (the seeded "Browse to purchase" with a
// breakdown), Cohorts (the seeded "Weekly buyers") and Revenue. Nothing is saved: new reports are
// built for the picture and left unsaved.
import { api, clearHighlights, go, highlight, session, settle, shot } from "../lib.mjs";

const AREA = "tutorials";

export default async function (browser) {
  const { context, page } = await session(browser);
  try {
    await run(context, page);
  } finally {
    await context.close();
  }
}

async function run(context, page) {
  // ---- Overview ----
  await go(page, "/");
  await settle(page, { quiet: 2000 });
  await shot(page, AREA, "10-overview", { clip: page.locator(".page"), pad: 0 });

  // ---- Reports ----
  await go(page, "/reports");
  await highlight(page.getByRole("link", { name: "New report" }));
  await shot(page, AREA, "10-reports-list", { clip: page.locator(".page-head, .page > .panel"), pad: 8 });
  await clearHighlights(page);

  await page.setViewportSize({ width: 1440, height: 1200 });
  await go(page, "/reports/new");
  const preset = page.getByRole("button", { name: "Purchases per day" });
  await highlight(preset);
  await shot(page, AREA, "10-report-presets", { clip: page.locator(".page-head, .rb-presets"), pad: 8 });
  await clearHighlights(page);
  await preset.click();
  await page.getByRole("button", { name: "Line", exact: true }).click();
  await page.locator(".rb-line").waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "10-report-line", { clip: page.locator(".rb-split"), pad: 8 });

  // Filters: only purchases of 1,000 or more, only contacts in "Pro plan".
  await page.getByRole("button", { name: "Only count events where…" }).click();
  await page.locator(".rb-where").getByLabel("Event property", { exact: true }).fill("amount");
  await page.locator(".rb-where").getByLabel("Comparison", { exact: true }).selectOption("gte");
  await page.locator(".rb-where").getByLabel("Value", { exact: true }).fill("1000");
  await page.getByLabel("Only contacts in").selectOption({ label: "Pro plan" });
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "10-report-filters", { clip: page.locator(".rb-split"), pad: 8 });

  // The seeded "Revenue by category": bars.
  const reports = (await api(context, "GET", "/v1/reports")).reports;
  const rev = reports.find((r) => r.name === "Revenue by category");
  await go(page, `/reports/${rev.id}`);
  await page.locator(".rb-bars").waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "10-report-bars", { clip: page.locator(".rb-results"), pad: 8 });

  // ---- Funnels ----
  await page.setViewportSize({ width: 1440, height: 1400 });
  const funnels = (await api(context, "GET", "/v1/funnels")).funnels;
  const f = funnels.find((x) => x.name === "Browse to purchase");
  await go(page, `/funnels/${f.id}`);
  await page.locator(".funnel-headline").waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "10-funnel", { clip: page.locator(".page-head, .funnel-split"), pad: 8 });
  await page.getByLabel("Break down by").fill("plan");
  await page.getByRole("heading", { name: "By plan" }).waitFor();
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "10-funnel-breakdown", { clip: page.locator("section.panel:has(h3:text-is('By plan'))"), pad: 8 });

  // ---- Cohorts ----
  const cohorts = (await api(context, "GET", "/v1/cohorts")).cohorts;
  const c = cohorts.find((x) => x.name === "Weekly buyers") ?? cohorts[0];
  await go(page, "/cohorts");
  await shot(page, AREA, "10-cohorts-list", { clip: page.locator(".page-head, .page > .panel"), pad: 8 });
  await go(page, `/cohorts/${c.id}`);
  await page.locator(".cohort-results table").waitFor();
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "10-cohort", { clip: page.locator(".page"), pad: 0 });

  // ---- Revenue ----
  await page.setViewportSize({ width: 1440, height: 1800 });
  await go(page, "/revenue");
  await page.locator(".revenue-figures").waitFor();
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "10-revenue", { clip: page.locator(".page-head, .revenue-controls, .revenue-body"), pad: 8 });
  const settings = page.locator("section.revenue-settings");
  await settings.getByRole("button", { name: "Change" }).click();
  await settle(page);
  await shot(page, AREA, "10-revenue-settings", { clip: settings, pad: 8 });
}
