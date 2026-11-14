// Screenshots for docs/tutorials/11-admin-and-privacy.md: the unsubscribe page of an email sent with
// a topic (the docs workspace's Weekly offers). A test send of an email template to one contact,
// with the topic, lands in Mailpit; its List-Unsubscribe link is opened (GET only: in the
// workspace's "confirm" link mode the page only asks, so nobody is unsubscribed).
import { api, settle, session, shot, until } from "../lib.mjs";

const AREA = "tutorials";
const MAILPIT = process.env.MAILPIT_URL || "http://localhost:18025";

export default async function (browser) {
  const { context, page } = await session(browser);
  const { settings } = await api(context, "GET", "/v1/settings/automation");
  if (settings.unsubscribe?.link_mode !== "confirm") throw new Error("the docs workspace must use the confirm link mode, or opening the link would unsubscribe");
  if (!settings.unsubscribe.topics?.some((t) => t.key === "weekly_offers")) throw new Error('the docs workspace has no topic "weekly_offers"');

  const contacts = (await api(context, "GET", "/v1/contacts?limit=50")).contacts;
  const c = contacts.find((x) => x.email && !Object.keys(x.attrs ?? {}).some((k) => (k.startsWith("optout_") || k.endsWith("_bounced")) && String(x.attrs[k]) === "true"));
  if (!c) throw new Error("no contact with an email who hasn't unsubscribed");
  const tpl = (await api(context, "GET", "/v1/templates")).templates.find((t) => t.channel === "email");
  if (!tpl) throw new Error("no email template");

  const sentAt = Date.now() - 2000;
  await api(context, "POST", "/v1/channels/test", { template_id: tpl.id, external_id: c.external_id, topic: "weekly_offers" });
  const url = await until(async () => {
    const r = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:"${c.email}"`)}`).then((x) => x.json()).catch(() => null);
    const m = r?.messages?.find((x) => new Date(x.Created).getTime() >= sentAt);
    if (!m) return null;
    const h = await fetch(`${MAILPIT}/api/v1/message/${m.ID}/headers`).then((x) => x.json());
    return /<(https?:\/\/[^>]+\/u\/[^>]+)>/.exec((h["List-Unsubscribe"] ?? [])[0] ?? "")?.[1];
  }, { timeout: 60_000, what: "the test email with its List-Unsubscribe header in Mailpit" });

  await page.goto(url);
  await settle(page);
  await shot(page, AREA, "11-unsubscribe-topic", { clip: page.locator("main"), pad: 16 });
  await context.close();
}
