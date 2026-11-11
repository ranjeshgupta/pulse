// Screenshots for docs/developer/01-installation.md: the sign-in page and Mailpit with mail the
// seeded "Acme Retail" workspace sent (`make docs-seed`).
import { DOCS, go, session, settle, shot } from "../lib.mjs";

const AREA = "developer";
const MAILPIT = process.env.PULSE_MAILPIT || "http://localhost:18025";

export default async function (browser) {
  const { context, page } = await session(browser, { signIn: null });

  await go(page, "/login");
  await page.getByLabel("Workspace ID").fill(DOCS.workspace);
  await page.getByLabel("Email").fill(DOCS.email);
  await page.getByLabel("Password").fill("not-the-real-password"); // shows as dots only
  await shot(page, AREA, "dev-01-login", { clip: page.locator(".auth-form > *"), pad: 24 });

  // Mailpit's own UI, filtered to the seeded workspace's broadcast.
  await page.goto(`${MAILPIT}/search?q=${encodeURIComponent('subject:"weekend sale"')}`);
  await settle(page, { quiet: 1500 });
  await shot(page, AREA, "dev-01-mailpit", { clip: { x: 0, y: 0, width: 1440, height: 560 } });
  await context.close();
}
