// Screenshots for docs/tutorials/07-one-time-campaigns.md: the Topic field of a one-time campaign
// (the docs workspace's saved topic, Weekly offers) and the estimate's warning when the workspace
// leaves email unsubscribers to segment filters. On the seeded draft "Monsoon clearance", changed
// on screen and never saved. For the warning, the workspace's email policy is switched to
// "segment" for the picture and put back in a finally (Settings → Bounces & unsubscribes).
import { api, go, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";

/** The control of a form field by its exact label (the label wraps the control, so its text runs on). */
function field(page, label) {
  return page.locator("label.field", { has: page.locator(":scope > span", { hasText: new RegExp(`^${label.replace(/[()]/g, "\\$&")}$`) }) }).locator("input, select, textarea").first();
}

export default async function (browser) {
  const { context, page } = await session(browser);
  const monsoon = (await api(context, "GET", "/v1/broadcasts")).broadcasts.find((b) => b.name === "Monsoon clearance");
  if (!monsoon) throw new Error('the seeded draft "Monsoon clearance" is missing (run make docs-seed)');

  await page.setViewportSize({ width: 1440, height: 1600 });
  await go(page, `/broadcasts/${monsoon.id}`);
  await field(page, "Send to").selectOption({ label: "Pro plan" });
  await field(page, "Channel").selectOption({ label: "Email" });
  const topic = page.getByRole("combobox", { name: "Topic" });
  await topic.selectOption({ label: "Weekly offers" });
  await settle(page);
  await shot(page, AREA, "07-topic", { clip: page.locator("label.field", { has: topic }), pad: 8 });

  // The same audience with email unsubscribers kept. The server caches an estimate for 30 s, so
  // wait until it answers with the new policy, then open the draft again.
  const saved = (await api(context, "GET", "/v1/settings/automation")).settings.unsubscribe;
  if (saved.email_policy !== "enforce") throw new Error(`expected the docs workspace to enforce email unsubscribes, not ${saved.email_policy}`);
  try {
    await api(context, "PUT", "/v1/settings/automation", { unsubscribe: { ...saved, email_policy: "segment" } });
    const pro = (await api(context, "GET", "/v1/segments")).segments.find((x) => x.name === "Pro plan");
    await until(async () => (await api(context, "POST", "/v1/segments/estimate", { include: [pro.id], exclude: [], channel: "email", transactional: false })).unsubscribed_kept,
      { timeout: 60_000, what: "the estimate with email unsubscribers kept" });
    await go(page, `/broadcasts/${monsoon.id}`);
    await field(page, "Send to").selectOption({ label: "Pro plan" });
    await field(page, "Channel").selectOption({ label: "Email" });
    await page.locator(".estimate .alert.warn").waitFor();
    await settle(page, { quiet: 800 });
    await shot(page, AREA, "07-estimate-unsubscribed", { clip: page.locator(".estimate"), pad: 8 });
  } finally {
    await api(context, "PUT", "/v1/settings/automation", { unsubscribe: saved });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await context.close();
}
