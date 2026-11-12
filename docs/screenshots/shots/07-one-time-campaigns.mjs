// Screenshots for docs/tutorials/07-one-time-campaigns.md.
// Starts a one-time email campaign from Campaigns → New campaign, saves it as a draft in the wizard,
// opens "All settings and results", and sends it with an A/B test (winner picked by hand) to "Sale
// preview testers", a static segment of six test contacts this script makes, so every email goes to
// Mailpit; shows preview, test send, sending, the winner pick, the results and "Send again". Then
// schedules a weekly campaign for next week in the wizard, cancels it and deletes it. Last, the seeded
// draft "Monsoon clearance" is changed on screen (not saved) for the deadline and estimate pictures.
// It also makes a second email template ("Weekend sale (subject B)") for variant B, once.
// A rerun first deletes the campaigns earlier runs made (by name; never the seeded ones).
import { api, clearHighlights, go, highlight, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const AB_NAME = "Weekend sale preview";
const OLD_NAMES = ["Weekend sale, abandoned carts"]; // earlier versions of this script
const WEEKLY_NAME = "Weekly picks, VIP customers";
const TEMPLATE_B = "Weekend sale (subject B)";
const TEST_ID = "docs-test-inbox";
const TEST_EMAIL = "test.inbox@example.com";
const GROUP = "Sale preview testers";
const TESTERS = ["Aarav", "Diya", "Kabir", "Isha", "Rohan", "Tara"].map((name) => ({
  id: `sale-tester-${name.toLowerCase()}`, email: `sale.tester.${name.toLowerCase()}@example.com`, name,
}));
const MAILPIT = process.env.MAILPIT_URL || "http://localhost:18025";

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept());

  // Clean up earlier runs.
  for (const b of (await api(context, "GET", "/v1/broadcasts")).broadcasts) {
    if (![AB_NAME, WEEKLY_NAME, ...OLD_NAMES].includes(b.name)) continue;
    if (["scheduled", "running", "testing"].includes(b.status)) await api(context, "POST", `/v1/broadcasts/${b.id}/cancel`);
    await api(context, "DELETE", `/v1/broadcasts/${b.id}`);
  }
  // A second version of the sale email for the A/B test.
  const templates = (await api(context, "GET", "/v1/templates")).templates;
  if (!templates.some((t) => t.name === TEMPLATE_B)) {
    await api(context, "POST", "/v1/templates", {
      name: TEMPLATE_B, channel: "email", subject: "Up to 30% off home and kitchen, this weekend only",
      html: `<div style="font-family:Arial;padding:24px"><h1>Hi {{.Attrs.first_name}},</h1><p>Up to 30% off home and kitchen, until Sunday night.</p><p><a href="https://demo.shop/sale">Shop the sale</a></p></div>`,
    });
  }
  // A test contact for "Send a test", and a small static segment of six test contacts to send to
  // (example.com addresses: on the local stack every email lands in Mailpit).
  const me = await api(context, "GET", "/v1/auth/me");
  const sdk = await context.newPage();
  await go(sdk, `/sdk/demo.html?key=${me.workspace.site_key}`);
  await sdk.evaluate(({ id, email, testers }) => {
    Pulse.identify(id, { email, first_name: "Test inbox" });
    for (const t of testers) { Pulse.reset(); Pulse.identify(t.id, { email: t.email, first_name: t.name }); }
    Pulse.flush();
  }, { id: TEST_ID, email: TEST_EMAIL, testers: TESTERS });
  await sdk.waitForTimeout(1500);
  await sdk.close();
  let group = (await api(context, "GET", "/v1/lists")).lists.find((l) => l.name === GROUP);
  if (!group) group = { id: (await api(context, "POST", "/v1/lists", { name: GROUP })).id };
  await api(context, "POST", `/v1/lists/${group.id}/members`, { action: "add", external_ids: TESTERS.map((t) => t.id) });
  await until(async () => (await api(context, "GET", "/v1/lists")).lists.find((l) => l.id === group.id)?.size === TESTERS.length,
    { what: "the test group to fill" });

  // 1. Campaigns → One-time tab.
  await go(page, "/campaigns?tab=one_time");
  await page.locator("tbody tr").first().waitFor();
  await settle(page, { quiet: 600 });
  const newBtn = page.getByRole("button", { name: "New campaign" }).first();
  await highlight(newBtn);
  await shot(page, AREA, "07-campaigns-one-time", { clip: page.locator("main .page > *").filter({ hasNot: page.locator(".toast") }), pad: 12 });
  await clearHighlights(page);

  // 2. New campaign → Send a message to a segment → Email; the wizard's first two steps, saved as a draft.
  await newBtn.click();
  const chooser = page.getByRole("dialog");
  await chooser.getByRole("button", { name: /^Send a message to a segment/ }).click();
  await chooser.getByRole("button", { name: "Email", exact: true }).click();
  await page.waitForURL("**/campaigns/new/email");
  await page.getByLabel("Campaign name").fill(AB_NAME);
  await page.getByLabel("Send to members of").selectOption({ label: GROUP });
  await page.getByRole("button", { name: "Next: Content" }).click();
  await page.getByLabel("Use a saved template").check();
  await field(page, "Template").selectOption({ label: "Weekend sale" });
  await page.getByRole("button", { name: "Save draft" }).click();
  await page.waitForURL(/\/campaigns\/one-time\/\d+$/);
  const id = page.url().split("/").pop();
  const allSettings = page.getByRole("link", { name: "All settings and results" });
  await highlight(allSettings);
  await shot(page, AREA, "07-wizard-draft", { clip: page.locator("main .page-head"), pad: 8 });
  await clearHighlights(page);
  await allSettings.click();
  await page.waitForURL(`**/broadcasts/${id}`);
  await field(page, "Name").waitFor();
  await settle(page, { quiet: 600 });

  // 3. All settings and results: the audience, channel and template (one segment left out for the
  // picture, removed again before sending).
  const leaveOut = page.getByRole("combobox", { name: "Leave out members of (optional)" });
  await leaveOut.selectOption({ label: "VIP customers" });
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "07-editor", { clip: page.locator(".page-head, .page > fieldset > .grid-2:first-child, .page > fieldset > .grid-2:first-child + .row, .page > fieldset > label.check:has-text('A/B test')"), pad: 12 });
  await page.locator(".page > fieldset .badge", { hasText: "VIP customers" }).getByRole("button", { name: "Remove" }).click();

  // Preview and send a test.
  await page.getByRole("button", { name: "Preview", exact: true }).click();
  const preview = page.getByRole("dialog", { name: /^Preview/ });
  await preview.locator("iframe").waitFor();
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "07-preview", { clip: preview, pad: 0 });
  await preview.getByRole("button", { name: "Close" }).click();

  await page.getByRole("button", { name: "Send a test", exact: true }).click();
  const test = page.getByRole("dialog", { name: /^Send a test/ });
  await test.getByLabel("Contact's external ID").fill(TEST_ID);
  await settle(page);
  await shot(page, AREA, "07-send-test", { clip: test, pad: 0 });
  await test.getByRole("button", { name: "Send test" }).click();
  await test.waitFor({ state: "hidden" });
  const msg = await until(async () => {
    const r = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:"${TEST_EMAIL}"`)}`).then((x) => x.json()).catch(() => null);
    const m = r?.messages?.[0];
    return m && Date.now() - new Date(m.Created).getTime() < 120_000 ? m : null;
  }, { timeout: 60_000, what: "the test email in Mailpit" });
  const mp = await context.newPage();
  await mp.setViewportSize({ width: 1200, height: 700 });
  await go(mp, `${MAILPIT}/view/${msg.ID}`);
  await settle(mp, { quiet: 1500 });
  await shot(mp, AREA, "07-test-mailpit", { clip: { x: 296, y: 56, width: 904, height: 420 } });
  await mp.close();

  // 4. A/B test: two subject lines, 50% test group, winner picked by hand.
  await page.getByLabel("A/B test: try up to 5 versions on part of the audience first").check();
  await page.getByLabel("Template for variant B").selectOption({ label: TEMPLATE_B });
  await page.getByRole("spinbutton", { name: "Test group percent" }).fill("50");
  await settle(page);
  await shot(page, AREA, "07-ab-test", { clip: page.locator(".ab-box"), pad: 8 });
  await page.locator(".ab-box").getByLabel("Pick the winner by").selectOption({ label: "By hand" });

  // 5. When, and a conversion goal. First the local-time option, ticked for its picture only.
  const localTime = page.getByLabel("Send at this time in each contact's own time zone");
  await localTime.check();
  await settle(page);
  await shot(page, AREA, "07-local-time", { clip: page.locator(".page > fieldset > label.check:has-text('own time zone'), .page > fieldset > label.check:has-text('own time zone') + p"), pad: 8 });
  await localTime.uncheck();
  await field(page, "Event (optional)").fill("purchase");
  await page.getByRole("heading", { name: "Conversion goal" }).click(); // close the event suggestions
  await page.setViewportSize({ width: 1440, height: 1800 });
  await settle(page);
  await shot(page, AREA, "07-when", {
    clip: page.locator("h3:has-text('When'), h3:has-text('When') ~ *"), pad: 8,
  });
  await page.setViewportSize({ width: 1440, height: 900 });

  // 6. Schedule: the send time is now, so it goes out right away.
  const schedule = page.getByRole("button", { name: "Schedule", exact: true });
  await highlight(schedule);
  await shot(page, AREA, "07-schedule-button", { clip: page.locator(".page-head"), pad: 8 });
  await clearHighlights(page);
  await schedule.click();
  await until(async () => (await api(context, "GET", `/v1/broadcasts/${id}`)).broadcast.status === "testing", { timeout: 60_000, what: "the A/B test to go out" });
  await until(async () => (await api(context, "GET", `/v1/broadcasts/${id}/report`)).totals.sent >= 2, { timeout: 60_000, what: "the test sends" });
  await go(page, `/broadcasts/${id}`);
  await settle(page, { quiet: 6000 });
  await shot(page, AREA, "07-testing", { clip: page.locator(".page-head, .alert.info"), pad: 8 });
  const results = page.locator("section.section", { has: page.getByRole("heading", { name: "Results" }) });
  const pickA = results.getByRole("button", { name: "Pick A as winner" });
  await highlight(pickA);
  await shot(page, AREA, "07-pick-winner", { clip: results.locator("section.panel", { has: page.getByRole("heading", { name: "Variants" }) }), pad: 8 });
  await clearHighlights(page);

  // 7. Pick A: it goes to the rest of the segment.
  await pickA.click();
  await until(async () => (await api(context, "GET", `/v1/broadcasts/${id}`)).runs.some((r) => r.winner_sent_at), { timeout: 60_000, what: "the winner send" });
  // Two testers open the email and one clicks its link (as a mail app would: the tracking image,
  // then the tracked link), so the results have something to show.
  const sent = await until(async () => {
    const r = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent('to:"sale.tester."')}&limit=50`).then((x) => x.json()).catch(() => null);
    const fresh = (r?.messages ?? []).filter((m) => Date.now() - new Date(m.Created).getTime() < 300_000);
    return fresh.length >= TESTERS.length ? fresh : null;
  }, { timeout: 60_000, what: "the campaign emails in Mailpit" });
  for (const [i, m] of sent.slice(0, 2).entries()) {
    const html = (await fetch(`${MAILPIT}/api/v1/message/${m.ID}`).then((x) => x.json())).HTML;
    const pixel = /<img[^>]+src="([^"]+\/o\/[^"]+)"/.exec(html)?.[1] ?? /<img[^>]+src="([^"]+)"/.exec(html)?.[1];
    if (pixel) await fetch(pixel.replaceAll("&amp;", "&")).catch(() => {});
    const link = /<a[^>]+href="([^"]+)"[^>]*>Shop the sale/.exec(html)?.[1];
    if (i === 0 && link) await fetch(link.replaceAll("&amp;", "&"), { redirect: "manual" }).catch(() => {});
  }
  await until(async () => (await api(context, "GET", `/v1/broadcasts/${id}/report`)).totals.opened >= 2, { timeout: 60_000, what: "the opens" });
  // And one buys (the conversion goal is purchase), from a browser of their own.
  const { context: buyer, page: shop } = await session(browser, { signIn: null });
  await go(shop, `/sdk/demo.html?key=${me.workspace.site_key}`);
  await shop.evaluate((id) => { Pulse.identify(id); Pulse.track("purchase", { amount: 2499, currency: "INR" }); Pulse.flush(); }, TESTERS[1].id);
  await shop.waitForTimeout(1000);
  await buyer.close();
  await until(async () => (await api(context, "GET", `/v1/broadcasts/${id}/report`)).totals.converted >= 1, { timeout: 90_000, what: "the conversion" });
  await until(async () => (await api(context, "GET", `/v1/broadcasts/${id}`)).broadcast.status === "completed", { timeout: 60_000, what: "the campaign to finish" });
  await page.setViewportSize({ width: 1440, height: 2400 });
  await go(page, `/broadcasts/${id}`);
  await settle(page, { quiet: 6000 });
  const resultsDone = page.locator("section.section", { has: page.getByRole("heading", { name: "Results" }) });
  await shot(page, AREA, "07-report", { clip: resultsDone, pad: 8 });
  await shot(page, AREA, "07-sends", { clip: page.locator("section.section", { has: page.getByRole("heading", { name: "Sends" }) }), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
  await go(page, `/broadcasts/${id}`);
  const again = page.getByRole("button", { name: "Send again" });
  await again.waitFor();
  await settle(page, { quiet: 600 });
  await highlight(again);
  await shot(page, AREA, "07-send-again", { clip: page.locator(".page-head"), pad: 8 });
  await clearHighlights(page);

  // 8. A weekly campaign for next week, set up in the wizard, then cancelled and deleted.
  await go(page, "/campaigns/new/email");
  await page.getByLabel("Campaign name").fill(WEEKLY_NAME);
  await page.getByLabel("Send to members of").selectOption({ label: "VIP customers" });
  await page.getByRole("button", { name: "Next: Content" }).click();
  await page.getByLabel("Use a saved template").check();
  await field(page, "Template").selectOption({ label: "Weekend sale" });
  await page.getByRole("button", { name: "Next: Schedule" }).click();
  await field(page, "Repeat").selectOption({ label: "Every week" });
  const [start, end] = await page.evaluate(() => {
    const pad = (n) => String(n).padStart(2, "0");
    const local = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
    const s = new Date(); s.setDate(s.getDate() + ((8 - s.getDay()) % 7 || 7)); s.setHours(10, 0, 0, 0); // next Monday 10:00
    const e = new Date(s); e.setDate(e.getDate() + 56); e.setHours(23, 0, 0, 0);
    return [local(s), local(e)];
  });
  await field(page, "First send (required)").fill(start);
  await field(page, "Ends (optional)").fill(end);
  await field(page, "Repeat").click();
  await page.keyboard.press("Escape");
  await settle(page);
  await shot(page, AREA, "07-weekly", { clip: page.locator("main .page > fieldset"), pad: 8 });
  await page.getByRole("button", { name: "Next: Review" }).click();
  await page.getByRole("button", { name: "Schedule", exact: true }).click();
  await page.waitForURL((u) => /\/broadcasts\/\d+$/.test(u.pathname));
  await page.getByText("This campaign is scheduled").waitFor();
  await settle(page, { quiet: 800 });
  const cancel = page.getByRole("button", { name: "Cancel campaign" });
  await highlight(cancel);
  await shot(page, AREA, "07-scheduled", { clip: page.locator(".page-head, .alert.info"), pad: 8 });
  await clearHighlights(page);
  await cancel.click();
  await page.locator(".page-head .badge", { hasText: "Cancelled" }).waitFor();
  await settle(page);
  await shot(page, AREA, "07-cancelled", { clip: page.locator(".page-head"), pad: 8 });
  await page.getByRole("button", { name: "Delete" }).click();
  await page.waitForURL((u) => u.pathname === "/campaigns");

  // 9. More segments, a send deadline and the exact estimate, on the seeded draft (not saved).
  const monsoon = (await api(context, "GET", "/v1/broadcasts")).broadcasts.find((b) => b.name === "Monsoon clearance");
  await page.setViewportSize({ width: 1440, height: 1600 });
  await go(page, `/broadcasts/${monsoon.id}`);
  await field(page, "Send to").selectOption({ label: "Pro plan" });
  await page.getByLabel("Leave out members of (optional)").selectOption({ label: "VIP tag" });
  await field(page, "Channel").selectOption({ label: "Email" });
  await page.getByLabel(/^Send deadline/).check();
  await page.getByLabel("Don't send after").fill("21:00");
  await page.locator(".estimate-total").waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "07-deadline", { clip: page.locator(".deadline-box"), pad: 8 });
  await shot(page, AREA, "07-estimate", { clip: page.locator(".estimate"), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
  await context.close();
}

/** The control of a form field by its exact label (the label wraps the control, so its text runs on). */
function field(page, label) {
  return page.locator("label.field", { has: page.locator(":scope > span", { hasText: new RegExp(`^${label.replace(/[()]/g, "\\$&")}$`) }) }).locator("input, select, textarea").first();
}
