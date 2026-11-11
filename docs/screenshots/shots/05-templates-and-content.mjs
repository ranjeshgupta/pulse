// Screenshots for docs/tutorials/05-templates-and-content.md.
// In the docs workspace it creates, as the tutorial does: an email and an SMS template ("Diwali
// offer"), a landing page (/p/acme-retail/diwali-offer), a multi-page form ("Bulk order enquiry",
// filled in once on its hosted page) and a gated asset ("2026 bulk price list"). Each run first
// deletes what an earlier run made under those names, so they don't pile up. Test sends go to one
// seeded contact (example.com address): the email reaches Mailpit, the SMS the test inbox.
import { api, clearHighlights, go, highlight, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const MAILPIT = process.env.MAILPIT_URL || "http://localhost:18025";

const EMAIL_NAME = "Diwali offer (email)";
const EMAIL_SUBJECT = '{{contact.first_name | default:"Hello"}}, your Diwali offer is here';
const SMS_NAME = "Diwali offer (SMS)";
const PAGE_SLUG = "diwali-offer";
const FORM_NAME = "Bulk order enquiry";
const ASSET_TITLE = "2026 bulk price list";

const EMAIL_STYLE = "font-family:Arial,Helvetica,sans-serif;font-size:15px;line-height:1.6;color:#152238;max-width:600px;margin:0 auto;padding:24px";
const PAGE_STYLE = "font-family:system-ui,-apple-system,Segoe UI,Roboto,Arial,sans-serif;font-size:17px;line-height:1.65;color:#152238;max-width:720px;margin:0 auto;padding:48px 24px";
const EMAIL_HTML = `<div class="pulse-rt" style="${EMAIL_STYLE}"><p>Hi {{contact.first_name | default:"there"}},</p>` +
  `<p>Our Diwali sale is on in {{contact.city}}: <strong>20% off everything</strong> until Sunday.</p>` +
  `<p><a href="https://shop.example.com/diwali">Shop the Diwali sale</a></p>` +
  `<p>See you soon,<br>The Acme Retail team</p></div>`;
const PAGE_HTML = `<div class="pulse-rt" style="${PAGE_STYLE}"><h1>Diwali offer: 20% off everything</h1>` +
  `<p>Until Sunday, everything in our stores and online is 20% off. Sign up and we'll email you your code.</p><p></p></div>`;

// A tiny but real PDF (Pulse checks the file's content, not just its name).
const PDF = Buffer.from("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
  "3 0 obj<</Type/Page/MediaBox[0 0 200 200]/Parent 2 0 R>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n");

const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
/** The input in a Field whose label starts with `text`. */
const field = (scope, text) => scope.locator("label.field").filter({ hasText: new RegExp("^" + esc(text)) }).locator("input, textarea, select").first();
const section = (page, heading) => page.locator("section", { has: page.getByRole("heading", { name: heading, exact: true }) }).last();

async function cleanUp(context) {
  for (const t of (await api(context, "GET", "/v1/templates")).templates) {
    if (t.name === EMAIL_NAME || t.name === SMS_NAME) await api(context, "DELETE", `/v1/templates/${t.id}`);
  }
  for (const { page: p } of (await api(context, "GET", "/v1/landing-pages")).pages) {
    if (p.slug === PAGE_SLUG) await api(context, "DELETE", `/v1/landing-pages/${p.id}`);
  }
  for (const a of (await api(context, "GET", "/v1/assets")).assets) {
    if (a.title === ASSET_TITLE) await api(context, "DELETE", `/v1/assets/${a.id}`);
  }
  for (const f of (await api(context, "GET", "/v1/forms")).forms) {
    if (f.name === FORM_NAME) await api(context, "DELETE", `/v1/forms/${f.id}`);
  }
}

/** A seeded contact with an email, a phone, a first name and a city, who hasn't opted out of anything. */
async function testContact(context) {
  const seeded = await api(context, "GET", "/v1/contacts/cust-0287").then((r) => r.contact, () => null);
  const { contacts } = await api(context, "GET", "/v1/contacts?limit=100");
  const c = [seeded, ...contacts].find((c) => c && c.email && c.attrs.phone && c.attrs.first_name && c.attrs.city &&
    !Object.keys(c.attrs).some((k) => k.startsWith("optout_") && String(c.attrs[k]) === "true") && c.attrs.email_bounced !== true);
  if (!c) throw new Error("no suitable seeded contact");
  return c;
}

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept());
  await cleanUp(context);
  const contact = await testContact(context);

  await templates(page, context, contact);
  await landingPage(page, context);
  await form(page, context);
  await assets(page, context);
  await context.close();
}

async function templates(page, context, contact) {
  // 1. The template list.
  await go(page, "/templates");
  await highlight(page.getByRole("link", { name: "New email template" }));
  await shot(page, AREA, "05-templates", { clip: { x: 0, y: 0, width: 1440, height: 560 } });
  await clearHighlights(page);

  // 2. A new email template: the visual builder first.
  await page.getByRole("link", { name: "New email template" }).click();
  await page.locator(".gjs-host iframe, .gjs-frame").first().waitFor();
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "05-email-builder", { clip: { x: 250, y: 0, width: 1180, height: 780 } });

  // 3. Rich text: written in the HTML tab here (reliable to type), then shown in Rich text.
  await page.getByRole("tab", { name: "Rich text" }).click();
  await page.getByLabel("Template name").fill(EMAIL_NAME);
  await page.getByLabel(/^Subject/).fill(EMAIL_SUBJECT);
  await page.getByRole("tab", { name: "HTML" }).click();
  await page.getByLabel("Email HTML").fill(EMAIL_HTML);
  await page.getByRole("tab", { name: "Rich text" }).click();
  await page.locator(".rt-content .ProseMirror").waitFor();
  await settle(page, { quiet: 800 });
  await highlight(page.getByRole("combobox", { name: "Insert personalization" }));
  await shot(page, AREA, "05-email-richtext", { clip: { x: 0, y: 0, width: 1440, height: 760 } });
  await clearHighlights(page);

  await page.getByRole("tab", { name: "HTML" }).click();
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "05-email-html", { clip: page.locator(".grid-2").last(), pad: 8 });
  await page.getByRole("tab", { name: "Rich text" }).click();

  await page.getByRole("button", { name: "Save template" }).click();
  await page.waitForURL(/\/templates\/\d+$/);
  // Reload after the first save: until then "Send a test" still uses the unsaved template (id 0)
  // and answers "not found" (app bug, see the tutorial's note).
  await go(page, page.url());

  // 4. Preview as a real contact.
  await page.getByRole("button", { name: "Preview as contact" }).click();
  let dialog = page.getByRole("dialog", { name: /^Preview:/ });
  await dialog.getByRole("textbox", { name: "Preview as contact" }).fill(contact.external_id);
  await dialog.getByRole("button", { name: "Preview as contact" }).click();
  await dialog.getByText(`As ${contact.external_id}`).waitFor();
  await page.setViewportSize({ width: 1440, height: 1200 });
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "05-email-preview", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Close" }).click();
  await page.setViewportSize({ width: 1440, height: 900 });

  // 5. Send a test to that contact; it arrives in Mailpit (the server's SMTP relay in development).
  // Mailpit holds every workspace's mail: find this test by its address and subject.
  const ours = async () => ((await (await page.request.get(`${MAILPIT}/api/v1/messages?limit=50`)).json()).messages ?? [])
    .filter((m) => m.To?.some((t) => t.Address === contact.email) && m.Subject.includes("your Diwali offer is here"));
  const before = (await ours()).length;
  await page.getByRole("button", { name: "Send a test" }).click();
  dialog = page.getByRole("dialog", { name: /^Send a test:/ });
  await dialog.getByLabel("Contact's external ID").fill(contact.external_id);
  await settle(page);
  await shot(page, AREA, "05-email-send-test", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Send test" }).click();
  await until(async () => (await ours()).length > before, { what: "the test email in Mailpit", timeout: 60_000 });
  const messages = await ours();
  const mail = await context.newPage();
  await mail.setViewportSize({ width: 1200, height: 900 });
  await mail.goto(`${MAILPIT}/view/${messages[0].ID}`);
  await settle(mail, { quiet: 1500 });
  await shot(mail, AREA, "05-mailpit", { clip: { x: 0, y: 0, width: 1200, height: 760 } });
  await mail.close();

  // 6. An SMS template with tracked links, and its test in the test inbox.
  await go(page, "/templates/new?channel=sms");
  await page.getByLabel("Template name").fill(SMS_NAME);
  await page.getByLabel(/^Message/).fill('Hi {{contact.first_name | default:"there"}}, Diwali sale: 20% off everything until Sunday. Shop now: https://shop.example.com/diwali');
  await page.getByLabel(/^Sender ID/).fill("ACMEIN");
  await page.getByRole("checkbox", { name: /Track link clicks/ }).check();
  await settle(page);
  await shot(page, AREA, "05-sms", { clip: { x: 0, y: 0, width: 1440, height: 760 } });
  await page.getByRole("button", { name: "Save template" }).click();
  await page.waitForURL(/\/templates\/\d+$/);
  await settle(page);
  // No reload: right after the first save, Send a test uses the saved template.
  await page.getByRole("button", { name: "Send a test" }).click();
  dialog = page.getByRole("dialog", { name: /^Send a test:/ });
  await dialog.getByLabel("Contact's external ID").fill(contact.external_id);
  await dialog.getByRole("button", { name: "Send test" }).click();
  await go(page, "/settings/test-inbox");
  await page.getByRole("tab", { name: "SMS" }).click();
  await page.getByRole("cell", { name: /Diwali sale/ }).first().waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 600 });
  await shot(page, AREA, "05-sms-test-inbox", { clip: page.locator(".panel").first(), pad: 8 });

  // 7. The seeded WhatsApp, RCS and push templates (looked at, not changed).
  const all = (await api(context, "GET", "/v1/templates")).templates;
  for (const [name, file] of [["Cart reminder (WhatsApp)", "05-whatsapp"], ["Festive sale (RCS)", "05-rcs"], ["Back in stock (push)", "05-push"]]) {
    const t = all.find((x) => x.name === name);
    if (!t) throw new Error(`seeded template ${name} is missing`);
    await go(page, `/templates/${t.id}`);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await settle(page, { quiet: 600 });
    await shot(page, AREA, file, { clip: page.locator(".editor-split"), pad: 12 });
    await page.setViewportSize({ width: 1440, height: 900 });
  }
}

async function landingPage(page, context) {
  await go(page, "/landing-pages");
  await highlight(page.getByRole("link", { name: "New page" }));
  await shot(page, AREA, "05-landing-pages", { clip: { x: 0, y: 0, width: 1440, height: 420 } });
  await clearHighlights(page);

  await page.getByRole("link", { name: "New page" }).click();
  await settle(page);
  await field(page, "Title").fill("Diwali offer: 20% off everything");
  await field(page, "Address").fill(PAGE_SLUG);
  await field(page, "Meta description").fill("20% off everything at Acme Retail until Sunday. Sign up to get your code.");
  await page.getByRole("group", { name: "Status" }).getByRole("button", { name: "Published" }).click();
  await page.getByRole("tab", { name: "Rich text" }).click();
  await page.getByRole("tab", { name: "HTML" }).click();
  await page.getByLabel("Page HTML").fill(PAGE_HTML);
  await page.getByRole("tab", { name: "Rich text" }).click();
  const editor = page.locator(".rt-content .ProseMirror");
  await editor.waitFor();
  await editor.locator("p").last().click();
  await page.locator("#lp-insert-form").selectOption({ label: "Newsletter sign-up" });
  await page.setViewportSize({ width: 1440, height: 1500 });
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "05-landing-settings", { clip: page.locator(".lp-settings"), pad: 8 });
  await highlight(page.locator("#lp-insert-form"));
  await shot(page, AREA, "05-landing-editor", { clip: page.locator(".lp-page > section.section"), pad: 8 });
  await clearHighlights(page);

  await page.getByRole("button", { name: "Save", exact: true }).click();
  await page.waitForURL(/\/landing-pages\/\d+$/);
  await settle(page, { quiet: 1000 });
  await shot(page, AREA, "05-landing-use-it", { clip: page.locator(".lp-below"), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });

  const { base_url } = await api(context, "GET", "/v1/landing-pages");
  const pub = await context.newPage();
  await pub.setViewportSize({ width: 1200, height: 1000 });
  await go(pub, `${base_url}${PAGE_SLUG}`);
  await settle(pub, { quiet: 1500 });
  await shot(pub, AREA, "05-landing-public", { clip: { x: 0, y: 0, width: 1200, height: 660 } });
  await pub.close();
}

async function form(page, context) {
  await go(page, "/forms");
  await highlight(page.getByRole("link", { name: "New form" }));
  await shot(page, AREA, "05-forms", { clip: { x: 0, y: 0, width: 1440, height: 420 } });
  await clearHighlights(page);

  await page.getByRole("link", { name: "New form" }).click();
  await settle(page);
  await field(page, "Name").fill(FORM_NAME);
  const rows = page.locator(".ff");
  const add = async (label, type) => {
    await page.getByRole("button", { name: "Add field" }).click();
    const r = rows.last();
    await field(r, "Label").fill(label);
    if (type) await field(r, "Type").selectOption(type);
    return r;
  };
  await add("Company");
  await page.getByRole("button", { name: "Add page break" }).click();
  await field(rows.last(), "Page title").fill("About your order");
  const qty = await add("Quantity", "number");
  await qty.getByRole("checkbox", { name: "Required" }).check();
  const po = await add("Purchase order", "file");
  for (const kind of ["Images (PNG, JPEG, GIF, WebP)", "Word, Excel, PowerPoint", "Text and CSV"]) {
    await po.getByRole("checkbox", { name: kind }).uncheck();
  }
  const hidden = await add("Campaign source", "hidden");
  await hidden.getByLabel("Attribute key").fill("utm_source");
  await field(hidden, "Value sent").fill("website");

  await field(page, "Description").fill("Buying for an office or an event? Tell us what you need and we'll send a quote within one working day.");
  await field(page, "Button text").fill("Send enquiry");
  const after = section(page, "After they submit");
  await field(after, "Message").fill("Thanks! We'll send your quote within one working day.");
  await field(after, "Add these tags").fill("bulk-order");
  const consent = section(page, "Consent");
  await field(consent, "Consent checkbox").fill("Email me about bulk offers. I can unsubscribe at any time.");
  await consent.getByRole("checkbox", { name: "Email" }).check();
  await page.getByRole("group", { name: "Status" }).getByRole("button", { name: "Live" }).click();

  await page.setViewportSize({ width: 1440, height: 2600 });
  await settle(page, { quiet: 800 });
  // Name and status down to the Quantity field, with the preview beside it.
  await shot(page, AREA, "05-form-fields", { clip: page.locator(".editor-split > div > .grid-2").first().or(rows.nth(4)).or(page.locator(".editor-split > aside")), pad: 8 });
  await shot(page, AREA, "05-form-file-hidden", { clip: rows.nth(5).or(rows.nth(6)), pad: 8 });
  await shot(page, AREA, "05-form-after-submit", { clip: after.or(consent), pad: 8 });

  await page.getByRole("button", { name: "Save", exact: true }).click();
  await page.waitForURL(/\/forms\/\d+$/);
  await settle(page, { quiet: 800 });
  const id = page.url().split("/").pop();
  await shot(page, AREA, "05-form-use-it", { clip: page.locator("section.panel", { has: page.getByRole("heading", { name: "Use it" }) }), pad: 8 });

  // The hosted page: page 1, page 2, then submit with a PDF.
  const { hosted_url } = await api(context, "GET", `/v1/forms/${id}`);
  const pub = await context.newPage();
  await pub.setViewportSize({ width: 900, height: 1000 });
  await go(pub, hosted_url);
  await pub.locator('input[name="email"]').fill("kiran.shah@example.com");
  await pub.locator('input[name="first_name"]').fill("Kiran");
  await pub.locator('input[name="company"]').fill("Shah Interiors");
  await shot(pub, AREA, "05-form-hosted-1", { clip: pub.locator("body > *"), pad: 16 });
  await pub.locator("[data-next]").first().click();
  await pub.locator('input[name="quantity"]').fill("40");
  await pub.locator('input[name="purchase_order"]').setInputFiles({ name: "PO-2026-118.pdf", mimeType: "application/pdf", buffer: PDF });
  await pub.locator('input[name="_consent"]').check();
  await settle(pub, { quiet: 2500 }); // forms filled in under 2 seconds are treated as bots
  await shot(pub, AREA, "05-form-hosted-2", { clip: pub.locator("body > *"), pad: 16 });
  await pub.getByRole("button", { name: "Send enquiry" }).click();
  await pub.getByText("Thanks! We'll send your quote").waitFor();
  await pub.close();

  await until(async () => (await api(context, "GET", `/v1/forms/${id}/submissions`)).total > 0, { what: "the form submission" });
  await go(page, `/forms/${id}`);
  await page.getByRole("heading", { name: "Recent" }).waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "05-form-submissions", { clip: section(page, "Submissions"), pad: 8 });
  await page.setViewportSize({ width: 1440, height: 900 });
}

async function assets(page, context) {
  await go(page, "/assets");
  await page.locator('input[type="file"]').setInputFiles({ name: "acme-bulk-price-list-2026.pdf", mimeType: "application/pdf", buffer: PDF });
  const up = page.locator("section.as-upload");
  await field(up, "Title").fill(ASSET_TITLE);
  await field(up, "Description").fill("Prices for orders of 20 items or more. Tell us who you are and the download starts.");
  await field(up, "Who can download").selectOption({ label: "Newsletter sign-up" });
  await highlight(up.getByRole("button", { name: "Upload" }));
  await shot(page, AREA, "05-asset-upload", { clip: up, pad: 8 });
  await clearHighlights(page);
  await up.getByRole("button", { name: "Upload" }).click();
  const drawer = page.locator("aside.drawer");
  await drawer.getByRole("heading", { name: "Use it" }).waitFor();
  await page.setViewportSize({ width: 1440, height: 1400 });
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "05-asset-drawer", { clip: page.locator("aside.drawer .drawer-head, aside.drawer .drawer-body > *"), pad: 0 });
  await drawer.getByRole("button", { name: "Close" }).first().click();
  await page.setViewportSize({ width: 1440, height: 900 });
  await settle(page);
  await shot(page, AREA, "05-assets", { clip: page.locator(".as-list"), pad: 8 });

  const a = (await api(context, "GET", "/v1/assets")).assets.find((x) => x.title === ASSET_TITLE);
  const pub = await context.newPage();
  await pub.setViewportSize({ width: 900, height: 1000 });
  await go(pub, `/a/${a.public_key}`);
  await shot(pub, AREA, "05-asset-gated", { clip: pub.locator("body > *"), pad: 16 });
  await pub.close();
}
