// Screenshots for docs/tutorials/17-interactive-amp-emails.md: an email template with an AMP
// version built from blocks (heading, text, carousel, poll, form), the add-block menu, a poll and a
// form block, the check panel with its preview and the providers' AMP support, hand-written AMP code
// with problems, and poll results. Votes are real: the script sends the template as a test to a few
// example contacts (Mailpit catches them), reads each email's poll link and answers it as an email
// client would. The template "Lamp colour poll (AMP)" is replaced on every run; the two example
// email providers made for the providers picture are deleted straight after it.
import { api, clearHighlights, go, highlight, session, settle, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const MAILPIT = process.env.MAILPIT_URL || "http://localhost:18025";
const NAME = "Lamp colour poll (AMP)";
const SENDER = "noreply@pulse.local"; // the server's default sender (PULSE_SMTP_FROM) in the dev stack
const VOTERS = [
  ["amp-voter-1", "Meera", "Brass"], ["amp-voter-2", "Kabir", "Brass"], ["amp-voter-3", "Asha", "Matte black"],
  ["amp-voter-4", "Rohan", "Brass"], ["amp-voter-5", "Isha", "Sage green"],
];
const IMG = (seed) => `https://picsum.photos/seed/${seed}/600/360`;

export default async function (browser) {
  const { context, page } = await session(browser);
  page.on("dialog", (d) => d.accept());
  const made = { providers: [] };
  try {
    await run(browser, context, page, made);
  } finally {
    for (const id of made.providers) await api(context, "DELETE", `/v1/channels/providers/${id}`).catch(() => {});
    await context.close();
  }
}

async function run(browser, context, page, made) {
  // Replace earlier runs' template.
  for (const t of (await api(context, "GET", "/v1/templates")).templates) if (t.name === NAME) await api(context, "DELETE", `/v1/templates/${t.id}`);

  // ---- 1. Where it starts: a new email template's AMP section ----
  await page.setViewportSize({ width: 1440, height: 1400 });
  await go(page, "/templates/new?channel=email");
  const section = page.locator("section.amp-section");
  await section.scrollIntoViewIfNeeded();
  const add = section.getByRole("button", { name: "Add an interactive AMP version" });
  await highlight(add);
  await shot(page, AREA, "17-start", { clip: section, pad: 8 });
  await clearHighlights(page);

  // Hand-written code with problems (not saved): "I have AMP code", then paste broken code.
  await section.getByRole("button", { name: "I have AMP code" }).click();
  const code = section.getByRole("textbox", { name: "AMP code" });
  await code.fill([
    "<!doctype html>",
    "<html ⚡4email>",
    "<head>",
    '<meta charset="utf-8">',
    '<script async src="https://cdn.ampproject.org/v0.js"></script>',
    "<style amp4email-boilerplate>body{visibility:hidden}</style>",
    "</head>",
    "<body>",
    '<h1>New lamps are here</h1>',
    '<img src="https://picsum.photos/600/360" alt="Lamp">',
    '<a href="https://www.acme-retail.example/lamps" onclick="track()">See them all</a>',
    "</body>",
    "</html>",
  ].join("\n"));
  await section.locator(".amp-problems").waitFor({ timeout: 15_000 });
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "17-code-problems", { clip: section.locator(".amp-layout"), pad: 8 });

  // ---- 2. A template built from blocks (saved through the API, as the editor saves it) ----
  const tpl = await api(context, "POST", "/v1/templates", {
    name: NAME, channel: "email", subject: "Help us pick the next lamp colour, {{contact.first_name | default:\"there\"}}",
    html: "<p>Hi {{contact.first_name | default:\"there\"}}, our new lamps are here. Vote for the next colour on our website.</p>",
    content: {
      amp_blocks: {
        style: { background: "#f6f3ee", text: "#2b2b2b", accent: "#b5651d", font: "sans" },
        blocks: [
          { id: "title", type: "heading", text: "Hi {{contact.first_name | default:\"there\"}}, meet the new lamps" },
          { id: "intro", type: "text", text: "Hand-finished brass and ceramic, made in small batches.\n\nSwipe through them below, then tell us which colour should come next." },
          { id: "lamps", type: "carousel", height: 360, items: [{ src: IMG("lamp1"), alt: "Brass desk lamp" }, { src: IMG("lamp2"), alt: "Ceramic table lamp" }, { src: IMG("lamp3"), alt: "Floor lamp" }] },
          { id: "colour", type: "poll", text: "Which colour should our next lamp come in?", options: ["Brass", "Matte black", "Sage green"], show_results: true },
          { id: "feedback", type: "form", text: "Anything else we should make?", fields: [{ key: "idea", label: "Your idea", type: "textarea", required: true }] },
          { id: "shop", type: "button", text: "See all lamps", url: "https://www.acme-retail.example/lamps" },
        ],
      },
    },
  });

  // Votes: a test send to each example contact, then the poll answered from the email's own link.
  const me = await api(context, "GET", "/v1/auth/me");
  for (const [id, name] of VOTERS) { // each in its own browser, so the SDK doesn't merge them
    const { context: v, page: sdk } = await session(browser, { signIn: null });
    await go(sdk, `/sdk/demo.html?key=${me.workspace.site_key}`);
    await sdk.evaluate(([i, n]) => { Pulse.identify(i, { email: `${i}@example.com`, first_name: n }); Pulse.flush(); }, [id, name]);
    await sdk.waitForTimeout(800);
    await v.close();
  }
  for (const [id] of VOTERS) await until(async () => (await api(context, "GET", `/v1/contacts/${id}`).catch(() => ({}))).contact, { what: `contact ${id}` });
  for (const [id, , answer] of VOTERS) {
    const sentAt = Date.now() - 2000;
    await api(context, "POST", "/v1/channels/test", { template_id: tpl.id, external_id: id });
    const link = await until(async () => {
      const r = await context.request.get(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:${id}@example.com subject:"lamp colour"`)}`);
      const msgs = ((await r.json()).messages ?? []).filter((m) => Date.parse(m.Created) >= sentAt); // not an earlier run's email
      if (!msgs.length) return null;
      const raw = await (await context.request.get(`${MAILPIT}/api/v1/message/${msgs[0].ID}/raw`)).text();
      const qp = raw.replace(/=\r?\n/g, "").replace(/=([0-9A-F]{2})/g, (_, h) => String.fromCharCode(parseInt(h, 16)));
      const m = /action-xhr="([^"]+\/v1\/amp\/[^"]+)"/.exec(qp);
      return m && m[1].replace(/&amp;/g, "&");
    }, { what: `the test email to ${id}` });
    const res = await context.request.post(new URL(new URL(link).pathname, page.url()).href, {
      form: { answer }, headers: { "AMP-Email-Sender": SENDER },
    });
    if (!res.ok()) throw new Error(`vote for ${id}: ${res.status()} ${await res.text()}`);
  }

  // ---- 3. The blocks editor ----
  await go(page, `/templates/${tpl.id}`);
  const amp = page.locator("section.amp-section");
  await amp.scrollIntoViewIfNeeded();
  await amp.locator(".amp-frame").first().waitFor();
  await settle(page, { quiet: 2500 });
  await shot(page, AREA, "17-blocks", { clip: amp, pad: 8 });

  const addBlock = amp.getByRole("button", { name: "+ Add a block" });
  await addBlock.click();
  await settle(page, { quiet: 300 });
  await shot(page, AREA, "17-add-block", { clip: amp.locator(".amp-add > button, .amp-add-menu"), pad: 8 });
  await addBlock.click();

  // The poll block, opened.
  await amp.locator(".amp-card-title", { hasText: "Poll" }).click();
  await settle(page, { quiet: 500 });
  await shot(page, AREA, "17-poll-block", { clip: amp.locator(".amp-card.open"), pad: 6 });

  // The form block, opened, with the answer saved to a contact field (not saved).
  await amp.locator(".amp-card-title", { hasText: "Form" }).click();
  await settle(page, { quiet: 300 });
  const form = amp.locator(".amp-card.open");
  const saveTo = form.locator("label.field", { hasText: "Also save the answer to the contact" }).locator("select");
  const options = await saveTo.locator("option").allTextContents();
  // A text field, if the workspace has one (the seeded workspace's fields vary).
  const pick = options.find((o) => /idea|note|interest|feedback|comment|favourite/i.test(o)) ?? options.find((o) => /city/i.test(o));
  if (pick) await saveTo.selectOption({ label: pick });
  await settle(page, { quiet: 500 });
  await shot(page, AREA, "17-form-block", { clip: form, pad: 6 });

  // Poll results (from the votes above).
  await page.reload();
  await settle(page);
  const results = page.locator("section.amp-results");
  await results.scrollIntoViewIfNeeded();
  await results.locator("table").waitFor();
  await settle(page, { quiet: 500 });
  await shot(page, AREA, "17-poll-results", { clip: results, pad: 8 });

  // ---- 4. The check panel with two example email providers (made for this picture only) ----
  for (const [name, host] of [["Main SMTP (example)", "smtp.sendgrid.net"], ["Receipts via Postmark (example)", "smtp.postmarkapp.com"]]) {
    const p = await api(context, "POST", "/v1/channels/providers", { channel: "email", provider: "smtp", name, config: { host, port: "587", from: "Acme Retail <hello@acme-retail.example>" } });
    made.providers.push(p.id);
  }
  await page.reload();
  await settle(page);
  const side = page.locator("section.amp-section .amp-side");
  await side.scrollIntoViewIfNeeded();
  await side.getByText("Receipts via Postmark (example)").waitFor();
  await settle(page, { quiet: 2000 });
  await shot(page, AREA, "17-check", { clip: side, pad: 8 });
  for (const id of made.providers.splice(0)) await api(context, "DELETE", `/v1/channels/providers/${id}`);
}
