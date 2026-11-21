// Screenshots for docs/tutorials/04-channels.md, steps 5 and 6: Settings → Bounces & unsubscribes.
// Nothing is saved. The bounce rules are changed on screen and discarded; the unsubscribe panel
// shows the docs workspace's saved topic (Weekly offers); the webhook tokens are hidden like
// secrets. The docs workspace has no bounce mailbox (a made-up one would be polled and fail), so
// the list of mailboxes is the server's answer stood in for in the browser (page.route) with two
// made-up mailboxes, and the Add mailbox form is filled in with placeholders and cancelled.
import { clearHighlights, go, highlight, session, settle, shot, confirmIfAsked } from "../lib.mjs";

const AREA = "tutorials";

const panel = (page, heading) => page.locator("section.panel", { has: page.getByRole("heading", { name: heading, exact: true }) });

/** The bounce and reply webhook URLs end in a workspace token: hide it like a secret. */
async function maskWebhookTokens(page) {
  await page.evaluate(() => {
    for (const el of document.querySelectorAll("code")) {
      el.textContent = el.textContent.replace(/\/v1\/(bounces|replies)\/[0-9a-f]{16,}/g, "/v1/$1/••••••••••••••••");
    }
  });
}

/** Two made-up mailboxes, as GET /v1/settings/bounce-mailboxes answers them. */
function fakeMailboxes() {
  const ago = (min) => new Date(Date.now() - min * 60_000).toISOString();
  const box = (id, name, username, folders, processed, skipped, lastCheck, lastError = "") => ({
    mailbox: { id, name, enabled: true, host: "imap.example.com", port: 993, tls: "tls", username, folders, has_password: true,
      last_check_at: lastCheck, last_error: lastError, processed, skipped },
    password: "••••••••",
  });
  return {
    mailboxes: [
      box(1, "Bounces for acme-retail.example.com", "bounces@acme-retail.example.com",
        [{ name: "INBOX", kind: "auto" }], 128, 3, ago(2)),
      box(2, "Newsletter domain (Gmail labels)", "postmaster@news.acme-retail.example.com",
        [{ name: "Bounces", kind: "bounces" }, { name: "Spam reports", kind: "complaints" }], 41, 0, ago(4)),
    ],
  };
}

export default async function (browser) {
  const { context, page } = await session(browser);
  await page.route("**/v1/settings/bounce-mailboxes", (route) =>
    route.request().method() === "GET" ? route.fulfill({ json: fakeMailboxes() }) : route.continue());
  await page.setViewportSize({ width: 1440, height: 2400 });
  await go(page, "/settings/bounces");

  // 1. Bounce rules: Email, with the soft bounce rule changed on screen (3, stop sending), not saved.
  const rules = panel(page, "Bounce rules");
  await rules.getByRole("tab", { name: "Email" }).waitFor();
  await rules.getByLabel("Soft bounce: count").fill("3");
  await rules.getByLabel("Soft bounce: action").selectOption("stop");
  await settle(page);
  await highlight(rules.getByRole("button", { name: "Save bounce rules" }));
  await shot(page, AREA, "04-bounce-rules", { clip: page.locator(".page-head").or(rules), pad: 8 });
  await clearHighlights(page);
  await rules.getByRole("button", { name: "Discard changes" }).click();

  // The SMS tab, as saved (the defaults).
  await rules.getByRole("tab", { name: "SMS" }).click();
  await settle(page);
  await shot(page, AREA, "04-bounce-rules-sms", { clip: rules.locator("table"), pad: 8 });
  await rules.getByRole("tab", { name: "Email" }).click();

  // 2. Unsubscribes: link mode, the email policy and the saved topic.
  const unsub = panel(page, "Unsubscribes");
  await unsub.getByRole("textbox", { name: "Topic 1 name" }).waitFor();
  await settle(page);
  await shot(page, AREA, "04-unsubscribes", { clip: unsub, pad: 8 });
  // The warning that "Leave them out with segment filters" shows (chosen on screen, not saved).
  await unsub.getByLabel(/Leave them out with segment filters/).check();
  await settle(page);
  await shot(page, AREA, "04-unsubscribe-segment-policy", { clip: unsub.locator("fieldset").nth(1).or(unsub.locator(".alert.warn")), pad: 8 });
  await unsub.getByRole("button", { name: "Discard changes" }).click();

  // 3. The bounce webhook (tokens hidden) and its signature checks.
  const bounces = panel(page, "Bounces and complaints");
  await bounces.scrollIntoViewIfNeeded();
  await maskWebhookTokens(page);
  await shot(page, AREA, "04-bounces", { clip: bounces.locator(".panel-head, .panel-body > p, .panel-body > .row, .panel-body > .grid-2"), pad: 8 });
  await shot(page, AREA, "04-webhook-signatures", { clip: bounces.locator("div", { has: page.getByRole("heading", { name: "Webhook signatures" }) }).last(), pad: 8 });

  // 4. Bounce mailboxes: the list (made-up, see above), then Add mailbox filled in and cancelled.
  const mailboxes = panel(page, "Bounce mailboxes (IMAP)");
  await mailboxes.getByText("Newsletter domain (Gmail labels)").waitFor();
  await settle(page);
  await shot(page, AREA, "04-bounce-mailboxes", { clip: mailboxes, pad: 8 });
  await mailboxes.getByRole("button", { name: "Add mailbox" }).click();
  const editor = mailboxes.locator(".mbx-editor");
  await editor.getByRole("textbox", { name: "Name", exact: true }).fill("Bounces for shop.acme-retail.example.com");
  await editor.getByRole("textbox", { name: "IMAP server", exact: true }).fill("imap.example.com");
  await editor.getByRole("textbox", { name: "Username", exact: true }).fill("bounces@shop.acme-retail.example.com");
  await editor.getByLabel("Folder 1", { exact: true }).fill("Bounces");
  await editor.getByLabel("Folder 1 holds").selectOption("bounces");
  await editor.getByRole("button", { name: "Add folder" }).click();
  await editor.getByLabel("Folder 2", { exact: true }).fill("Spam reports");
  await settle(page);
  await shot(page, AREA, "04-bounce-mailbox", { clip: editor, pad: 8 });
  await editor.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");

  await context.close();
}
