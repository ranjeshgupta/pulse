// Screenshots for docs/developer/03-api.md: where the keys live (Settings → Sign-in & keys → Keys).
// Read-only: nothing is saved. The server API key is never on this page (only "Replace API key"
// shows a new one), and maskSecrets() hides any pk_/pzk_ value anyway; the site key is public.
import { go, maskSecrets, session, shot } from "../lib.mjs";

const AREA = "developer";

const panel = (page, heading) => page.locator("section.panel", { has: page.getByRole("heading", { name: heading, exact: true }) });

export default async function (browser) {
  const { context, page } = await session(browser);

  await go(page, "/settings/access");
  const keys = panel(page, "Keys");
  await keys.waitFor();
  await maskSecrets(page);
  await shot(page, AREA, "dev-03-keys", { clip: keys, pad: 8 });

  await context.close();
}
