// Screenshots for docs/tutorials/14-campaigns-hub-and-one-time-campaigns.md, in the docs workspace
// (acme-retail). `make docs-seed` tags its campaigns and adds the one-time campaigns "Diwali offer"
// (SMS, scheduled) and "Monsoon clearance" (WhatsApp, draft). The New campaign chooser is opened and
// closed, the wizard is filled in and left without saving, and the two editors of the seeded draft
// "Monsoon clearance" are only looked at.
import { api, clearHighlights, go, highlight, session, settle, shot } from "../lib.mjs";

const AREA = "tutorials";

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept()); // "leave this page?" after typing into the wizard

  // 1. The hub, with the sidebar: one Campaigns entry, every campaign with its type and numbers.
  await page.setViewportSize({ width: 1440, height: 1000 });
  await go(page, "/campaigns");
  await page.getByRole("link", { name: "Diwali offer" }).waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "14-hub", { clip: { x: 0, y: 0, width: 1440, height: 1000 } });

  // 2. A tab and a tag filter.
  await page.getByRole("tab", { name: /^One-time/ }).click();
  await page.getByLabel("Tag").selectOption("Festive");
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "14-hub-filtered", { clip: page.locator("main .page > *").filter({ hasNot: page.locator(".toast") }), pad: 8 });

  // 3. New campaign: what do you want to do, then (one-time) which channel.
  await go(page, "/campaigns");
  const create = page.getByRole("button", { name: "New campaign" }).first();
  await highlight(create);
  await shot(page, AREA, "14-new-button", { clip: page.locator("main .page-head"), pad: 8 });
  await clearHighlights(page);
  await create.click();
  const chooser = page.getByRole("dialog");
  await chooser.getByText("What do you want to do?").waitFor();
  await settle(page, { quiet: 400 });
  await shot(page, AREA, "14-new-goal", { clip: chooser, pad: 0 });
  await chooser.getByRole("button", { name: /^Send a message to a segment/ }).click();
  await chooser.getByText("Which channel?").waitFor();
  await settle(page, { quiet: 300 });
  await shot(page, AREA, "14-new-channel", { clip: chooser, pad: 0 });
  await chooser.getByRole("button", { name: "SMS", exact: true }).click();
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

  // 8. The two editors of a saved one-time campaign link to each other (the seeded draft).
  const monsoon = (await api(context, "GET", "/v1/broadcasts")).broadcasts.find((b) => b.name === "Monsoon clearance");
  await page.setViewportSize({ width: 1440, height: 900 });
  await go(page, `/campaigns/one-time/${monsoon.id}`);
  const all = page.getByRole("link", { name: "All settings and results" });
  await all.waitFor();
  await highlight(all);
  await shot(page, AREA, "14-wizard-header", { clip: page.locator("main .page-head"), pad: 8 });
  await clearHighlights(page);
  await all.click();
  await page.waitForURL(`**/broadcasts/${monsoon.id}`);
  const step = page.getByRole("link", { name: "Edit step by step" });
  await step.waitFor();
  await settle(page, { quiet: 400 });
  await highlight(step);
  await shot(page, AREA, "14-editor-header", { clip: page.locator("main .page-head"), pad: 8 });
  await clearHighlights(page);
  await context.close();
}
