// Screenshots for docs/tutorials/14-campaigns-hub-and-one-time-campaigns.md, in the docs workspace
// (acme-retail). `make docs-seed` tags its broadcasts and journeys and adds the one-time campaigns
// "Diwali offer" (SMS, scheduled) and "Monsoon clearance" (WhatsApp, draft). The wizard is filled in
// and left without saving.
import { clearHighlights, go, highlight, session, settle, shot } from "../lib.mjs";

const AREA = "tutorials";

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept()); // "leave this page?" after typing into the wizard

  // 1. The hub: every campaign, its type, channels, schedule, audience and sends.
  await page.setViewportSize({ width: 1440, height: 1000 });
  await go(page, "/campaigns");
  await page.getByRole("link", { name: "Diwali offer" }).waitFor();
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "14-hub", { clip: page.locator("main .page"), pad: 8 });

  // 2. A tab and a tag filter.
  await page.getByRole("tab", { name: /^One-time/ }).click();
  await page.getByLabel("Tag").selectOption("Festive");
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "14-hub-filtered", { clip: page.locator("main .page > *").filter({ hasNot: page.locator(".toast") }), pad: 8 });

  // 3. A new one-time campaign: choose the channel.
  await go(page, "/campaigns");
  const create = page.getByLabel("New one-time campaign");
  await highlight(create);
  await shot(page, AREA, "14-new", { clip: { x: 0, y: 0, width: 1440, height: 260 } });
  await clearHighlights(page);
  await create.selectOption("sms");
  await page.waitForURL("**/campaigns/new/sms");

  // 4. Audience: segments in and out, tags, and the exact estimate.
  await page.getByLabel("Campaign name").fill("Weekend flash sale");
  await page.getByLabel("Send to members of").selectOption({ label: "Pro plan" });
  await page.getByLabel("Leave out members of (optional)").selectOption({ label: "VIP tag" });
  const tag = page.getByLabel("Add a tag");
  await tag.fill("Flash sale");
  await tag.press("Enter");
  await page.locator(".estimate-total").waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "14-wizard-audience", { clip: page.locator("main .page"), pad: 8 });
  await shot(page, AREA, "14-estimate", { clip: page.locator(".wizard-aside"), pad: 8 });

  // 5. Content, written here (with the variable picker and a live preview).
  await page.getByRole("button", { name: "Next: Content" }).click();
  await page.getByLabel("Message", { exact: true }).fill('Hi {{contact.first_name | default:"there"}}, our weekend flash sale is on: 30% off decor till Sunday midnight. https://acme.example.com/flash');
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "14-wizard-content", { clip: page.locator("main .page"), pad: 8 });

  // 6. Schedule: when, and a send deadline.
  await page.getByRole("button", { name: "Next: Schedule" }).click();
  await page.getByLabel(/^Send deadline/).check();
  await page.getByLabel("Hours late").fill("6");
  await settle(page, { quiet: 400 });
  await shot(page, AREA, "14-wizard-schedule", { clip: page.locator("main .page"), pad: 8 });

  // 7. Review: everything in words, with the estimate (not sent).
  await page.getByRole("button", { name: "Next: Review" }).click();
  await page.locator(".estimate-total").waitFor();
  await settle(page, { quiet: 800 });
  await highlight(page.getByRole("button", { name: /^(Send now|Schedule)$/ }));
  await shot(page, AREA, "14-wizard-review", { clip: page.locator("main .page"), pad: 8 });
  await clearHighlights(page);
  await context.close();
}
