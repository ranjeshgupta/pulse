// Screenshots for docs/tutorials/06-triggered-and-automation-campaigns.md.
// Starts a triggered campaign from Campaigns → New campaign and builds a "Welcome series" journey on the canvas step by step (Start → Send email → Opens email
// → Wait → Contact segments → Send email, with Exit and tag steps on the other paths), saves it,
// takes it live, sends one test contact through it from the SDK test page, shows the welcome
// email in Mailpit and the per-step stats and report, then pauses it. The journey is left paused.
// Then it shows the Campaigns page's Delete / Archive buttons and the Archived status (section 15).
// A rerun first deletes the "Welcome series" journeys earlier runs made, or archives the ones that
// ran (never the seeded ones), so only this run's campaign is in the list.
import { api, clearHighlights, go, highlight, runId, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const NAME = "Welcome series";
const EVENT = "newsletter_signup";
const MAILPIT = process.env.MAILPIT_URL || "http://localhost:18025";

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept());

  // Earlier runs of this script (including ones older versions renamed "(earlier docs run)"):
  // Pulse deletes only journeys that never ran, so the ones that did are archived (hidden from the
  // list, history kept).
  for (const j of (await api(context, "GET", "/v1/journeys")).journeys) {
    if (j.archived_at || (j.name !== NAME && j.name !== `${NAME} (earlier docs run)`)) continue;
    if (j.status === "active") await api(context, "POST", `/v1/journeys/${j.id}/pause`);
    try {
      await api(context, "DELETE", `/v1/journeys/${j.id}`);
    } catch {
      await api(context, "POST", `/v1/journeys/${j.id}/archive`);
    }
  }

  // 1. Campaigns → Triggered tab → New campaign → React when someone does something.
  await page.setViewportSize({ width: 1440, height: 1000 });
  await go(page, "/campaigns?tab=trigger");
  await page.locator("tbody tr").first().waitFor();
  await settle(page, { quiet: 600 });
  const newBtn = page.getByRole("button", { name: "New campaign" }).first();
  await highlight(newBtn);
  await shot(page, AREA, "06-campaigns-triggered", { clip: page.locator("main .page > *").filter({ hasNot: page.locator(".toast") }), pad: 12 });
  await clearHighlights(page);
  await newBtn.click();
  const goal = page.getByRole("dialog").getByRole("button", { name: /^React when someone does something/ });
  await highlight(goal);
  await shot(page, AREA, "06-new-goal", { clip: page.getByRole("dialog"), pad: 0 });
  await clearHighlights(page);
  await goal.click();
  await page.waitForURL("**/journeys/new?start=event");
  await page.setViewportSize({ width: 1440, height: 900 });
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "06-new-campaign");

  // 2. Name and what starts it.
  await page.getByLabel("Campaign name").fill(NAME);
  const insp = page.locator("aside.inspector");
  const kind = insp.getByLabel("Start this campaign for a contact when they");
  await kind.selectOption("event");
  await insp.getByLabel("Event name").fill(EVENT);
  await settle(page);
  await shot(page, AREA, "06-trigger", { clip: insp.locator(".insp > .insp-head, .insp > label.field, .insp > h4"), pad: 4 });
  // The other two starts, for the table under it; then back to the event.
  await kind.selectOption("form_submitted");
  await insp.getByLabel(/^Form/).selectOption({ label: "Newsletter sign-up" });
  await settle(page);
  await shot(page, AREA, "06-trigger-form", { clip: insp.locator(".insp > .insp-head, .insp > label.field"), pad: 4 });
  await kind.selectOption("segment_entered");
  await insp.locator("label.field", { has: page.locator(":scope > span", { hasText: /^Segment$/ }) }).locator("select").selectOption({ label: "VIP customers" });
  await insp.locator(".reach-box").waitFor();
  await page.setViewportSize({ width: 1440, height: 1300 });
  await settle(page, { quiet: 600 });
  const ib = await insp.boundingBox();
  const rb = await insp.locator(".reach-box").boundingBox();
  await shot(page, AREA, "06-trigger-segment", { clip: { x: ib.x, y: ib.y, width: ib.width, height: rb.y + rb.height - ib.y + 8 } });
  await page.setViewportSize({ width: 1440, height: 900 });
  await kind.selectOption("event");
  await insp.getByLabel("Event name").fill(EVENT);

  // 3. "+" → Action → Send email.
  const plus = (name) => page.getByRole("button", { name, exact: true });
  await zoomFit(page);
  await highlight(plus("Add step after Start"));
  await shot(page, AREA, "06-plus", { clip: page.locator(".journey-canvas"), pad: 0 });
  await clearHighlights(page);
  await clickPlus(page, "Add step after Start");
  const chooser = page.getByRole("dialog");
  await chooser.waitFor();
  await settle(page);
  await shot(page, AREA, "06-chooser", { clip: page.locator(".sc"), pad: 0 });
  await chooser.getByRole("button", { name: "Action", exact: true }).click();
  await settle(page);
  await shot(page, AREA, "06-chooser-actions", { clip: page.locator(".sc"), pad: 0 });
  await chooser.getByRole("option", { name: "Send email", exact: true }).click();
  await chooser.waitFor({ state: "hidden" });
  await insp.getByLabel("Email template").selectOption({ label: "Welcome email" });
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "06-send-email", { clip: insp.locator(".insp"), pad: 0 });

  // 4. Decision → Opens email, after the Send email step.
  await clickPlus(page, "Add step after Send email (step 1)");
  await chooser.getByRole("button", { name: "Decision", exact: true }).click();
  await settle(page);
  await shot(page, AREA, "06-chooser-decisions", { clip: page.locator(".sc"), pad: 0 });
  await chooser.getByRole("option", { name: "Opens email", exact: true }).click();
  await chooser.waitFor({ state: "hidden" });
  await zoomFit(page);
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "06-decision", { clip: page.locator(".journey-layout"), pad: 0 });

  // 5. Yes path: Wait 1 day.
  await clickPlus(page, "Add step on Yes path of Opens email (step 2)");
  await chooser.getByRole("button", { name: "Wait", exact: true }).click();
  await chooser.getByRole("option", { name: "Wait", exact: true }).click();
  await chooser.waitFor({ state: "hidden" });
  await settle(page);
  await shot(page, AREA, "06-wait", { clip: insp.locator(".insp"), pad: 0 });

  // 6. Condition → Contact segments, after the wait.
  await clickPlus(page, "Add step after Wait (step 3)");
  await chooser.getByRole("button", { name: "Condition", exact: true }).click();
  await chooser.getByRole("option", { name: "Contact segments", exact: true }).click();
  await chooser.waitFor({ state: "hidden" });
  await insp.getByLabel("Is the contact in this segment?").selectOption({ label: "Repeat buyers in Pune" });
  await settle(page);
  await shot(page, AREA, "06-condition", { clip: insp.locator(".insp"), pad: 0 });

  // The No paths are still open: the issues list says so.
  await zoomFit(page);
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "06-issues", { clip: page.locator(".j-issues"), pad: 8 });

  // 7. Condition No → Send email "Weekend sale" → Exit.
  await clickPlus(page, "Add step on No path of Contact segments (step 4)");
  await chooser.getByRole("button", { name: "Action", exact: true }).click();
  await chooser.getByRole("option", { name: "Send email", exact: true }).click();
  await chooser.waitFor({ state: "hidden" });
  await insp.getByLabel("Email template").selectOption({ label: "Weekend sale" });
  const sale = await selectedStep(page);
  await addExit(page, `Add step after Send email (step ${sale})`);

  // 8. Decision No → Modify contact's tags (welcome-unopened) → Exit.
  await clickPlus(page, "Add step on No path of Opens email (step 2)");
  await chooser.getByRole("button", { name: "Action", exact: true }).click();
  await chooser.locator(".sc-search input").fill("tags");
  await chooser.getByRole("option", { name: "Modify contact's tags", exact: true }).click();
  await chooser.waitFor({ state: "hidden" });
  await insp.getByLabel("Add tags").fill("welcome-unopened");
  const tags = await selectedStep(page);
  await addExit(page, `Add step after Modify contact's tags (step ${tags})`);

  // 9. Toolbar: Tidy up, Zoom to fit; the whole canvas with the minimap.
  const tools = page.locator(".j-toolbar");
  await page.setViewportSize({ width: 1440, height: 1300 });
  await tools.getByRole("button", { name: "Tidy up" }).click();
  await page.waitForTimeout(400);
  await zoomFit(page);
  await page.locator(".react-flow__pane").click({ position: { x: 5, y: 5 } });
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "06-toolbar", { clip: tools, pad: 6 });
  await shot(page, AREA, "06-canvas", { clip: page.locator(".journey-layout"), pad: 0 });

  // Undo and redo: remove a step, undo brings it back.
  // (Not captured; the text explains it.)

  await page.setViewportSize({ width: 1440, height: 900 });

  // 10. Publish window on Start.
  await page.locator(".jnode.c-start").click();
  await settle(page);
  const sched = insp.locator("h4", { hasText: "Publish window" });
  await sched.scrollIntoViewIfNeeded();
  await shot(page, AREA, "06-schedule", { clip: insp.locator("h4:has-text('Publish window'), h4:has-text('Publish window') ~ *"), pad: 8 });

  // 11. Save draft, then publish.
  await page.getByRole("button", { name: "Save draft" }).click();
  await page.waitForURL((u) => /\/journeys\/\d+$/.test(u.pathname));
  await settle(page, { quiet: 800 });
  const id = page.url().split("/").pop();
  const live = page.getByRole("button", { name: "Save and publish" });
  await highlight(live);
  await shot(page, AREA, "06-go-live", { clip: page.locator(".page-head"), pad: 8 });
  await clearHighlights(page);
  await live.click();
  await page.getByText("This campaign is live").waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "06-live", { clip: page.locator(".page-head, .alert.info, .tabs"), pad: 8 });

  // The journey engine picks up newly live journeys every 2 seconds.
  await page.waitForTimeout(5000);

  // 12. A test contact signs up on the SDK test page.
  const run = runId();
  const ext = `meera-${run}`;
  const email = `meera.iyer+${run}@example.com`;
  const me = await api(context, "GET", "/v1/auth/me");
  const test = await context.newPage();
  await go(test, `/sdk/demo.html?key=${me.workspace.site_key}`);
  await test.evaluate(({ ext, email }) => {
    Pulse.identify(ext, { email, first_name: "Meera", city: "Bengaluru" });
    Pulse.flush();
  }, { ext, email });
  // The sign-up event follows once the contact (with their email) is stored, as on a real site.
  await until(async () => (await api(context, "GET", `/v1/contacts?q=${encodeURIComponent(ext)}&limit=1`)).contacts?.[0]?.email === email,
    { what: "the test contact to arrive" });
  await test.evaluate((event) => { Pulse.track(event, { source: "footer" }); Pulse.flush(); }, EVENT);
  await test.waitForTimeout(1500);
  await test.close();

  // 13. The welcome email in Mailpit.
  const msg = await until(async () => {
    const r = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:"${email}"`)}`).then((x) => x.json()).catch(() => null);
    return r?.messages?.[0];
  }, { timeout: 60_000, what: "the welcome email in Mailpit" });
  const mp = await context.newPage();
  await mp.setViewportSize({ width: 1200, height: 760 });
  await go(mp, `${MAILPIT}/view/${msg.ID}`);
  await settle(mp, { quiet: 1500 });
  // Only the message (the inbox list beside it holds other test mail).
  await shot(mp, AREA, "06-mailpit", { clip: { x: 296, y: 56, width: 904, height: 450 } });
  await mp.close();

  // 14. Per-step stats on the canvas, and the report.
  await until(async () => {
    const r = await api(context, "GET", `/v1/journeys/${id}/report`);
    return Object.values(r.messages?.by_node ?? {}).some((c) => c.sent > 0);
  }, { timeout: 60_000, what: "the send to show in the journey report" });
  await page.setViewportSize({ width: 1440, height: 1300 });
  await go(page, `/journeys/${id}`);
  await settle(page, { quiet: 4000 });
  await zoomFit(page);
  await page.locator(".react-flow__pane").click({ position: { x: 5, y: 5 } });
  await settle(page, { quiet: 600 });
  // The top of the canvas, where the test contact is.
  const cb = await page.locator(".journey-canvas").boundingBox();
  await shot(page, AREA, "06-stats", { clip: { x: cb.x, y: cb.y, width: cb.width, height: 800 } });
  await page.setViewportSize({ width: 1440, height: 2200 });
  await page.getByRole("tab", { name: "Report" }).click();
  await settle(page, { quiet: 2000 });
  await shot(page, AREA, "06-report", { clip: page.locator(".page > *").filter({ hasNot: page.locator(".toast") }), pad: 8 });

  await page.setViewportSize({ width: 1440, height: 900 });
  // 15. Unpublish it, holding the people inside (it's left paused).
  await page.getByRole("tab", { name: "Steps" }).click();
  const pause = page.getByRole("button", { name: "Unpublish" });
  await highlight(pause);
  await shot(page, AREA, "06-pause", { clip: page.locator(".page-head, .alert.info"), pad: 8 });
  await clearHighlights(page);
  await pause.click();
  const unpub = page.getByRole("dialog", { name: /^Unpublish / });
  await unpub.waitFor();
  await unpub.getByRole("radio", { name: /Hold them where they are/ }).click();
  await settle(page, { quiet: 400 });
  await shot(page, AREA, "06-unpublish", { clip: unpub, pad: 8 });
  await unpub.getByRole("button", { name: "Unpublish", exact: true }).click();
  await until(async () => (await api(context, "GET", `/v1/journeys/${id}`)).status === "paused", { what: "the journey to unpublish" });

  // 16. Delete or archive: the Campaigns page's row buttons and the Archived status (nothing is clicked).
  await page.setViewportSize({ width: 1440, height: 1000 });
  await go(page, "/campaigns?tab=trigger");
  const row = page.locator("tbody tr", { hasText: NAME }).first();
  await row.waitFor();
  await settle(page, { quiet: 600 });
  await highlight(row.getByRole("button", { name: "Archive" }));
  const draftDelete = page.locator("tbody tr").getByRole("button", { name: "Delete" }).first();
  if (await draftDelete.count()) await highlight(draftDelete);
  await highlight(page.getByRole("combobox", { name: "Status" }));
  await shot(page, AREA, "06-archive", { clip: page.locator("main .page > *").filter({ hasNot: page.locator(".toast") }), pad: 12 });
  await clearHighlights(page);

  // 17. M3: a send deadline on a step, then copy and paste steps (on screen only, not saved).
  await page.setViewportSize({ width: 1440, height: 1100 });
  await go(page, `/journeys/${id}`);
  await zoomFit(page);
  await page.locator(".react-flow__node", { hasText: "Send email" }).first().click();
  const side = page.locator("aside.inspector");
  await side.getByLabel(/^Send deadline/).check();
  await side.getByLabel("Hours late").fill("12");
  await settle(page);
  await shot(page, AREA, "06-deadline", { clip: side.locator(".deadline-box"), pad: 8 });
  const canvasTools = page.locator(".j-toolbar");
  await highlight(canvasTools.getByRole("button", { name: /^Copy/ }));
  await highlight(canvasTools.getByRole("button", { name: "Paste" }));
  await shot(page, AREA, "06-copy-paste", { clip: canvasTools, pad: 8 });
  await clearHighlights(page);
  await canvasTools.getByRole("button", { name: /^Copy/ }).click();
  await canvasTools.getByRole("button", { name: "Paste" }).click();
  const pasteDialog = page.getByRole("dialog", { name: "Paste steps" });
  await pasteDialog.waitFor();
  await settle(page);
  await shot(page, AREA, "06-paste", { clip: pasteDialog, pad: 0 });
  await pasteDialog.getByRole("button", { name: "Paste", exact: true }).click();
  await pasteDialog.waitFor({ state: "hidden" });
  await zoomFit(page);
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "06-pasted", { clip: page.locator(".journey-canvas"), pad: 0 });
  await page.setViewportSize({ width: 1440, height: 900 });
  await context.close();
}

/** Clicks a "+" on the canvas. The minimap can sit over it, so the click goes to the button itself. */
async function clickPlus(page, name) {
  await zoomFit(page);
  await page.getByRole("button", { name, exact: true }).dispatchEvent("click");
}

async function zoomFit(page) {
  await page.locator(".j-toolbar").getByRole("button", { name: "Zoom to fit" }).click();
  await page.waitForTimeout(400);
}

/** The number of the step selected in the inspector. */
async function selectedStep(page) {
  return (await page.locator("aside.inspector .insp-num").innerText()).trim();
}

async function addExit(page, plusName) {
  await clickPlus(page, plusName);
  const chooser = page.getByRole("dialog");
  await chooser.getByRole("button", { name: "Action", exact: true }).click();
  await chooser.getByRole("option", { name: "Exit journey", exact: true }).click();
  await chooser.waitFor({ state: "hidden" });
}
