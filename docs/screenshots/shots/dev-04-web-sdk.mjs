// Screenshots for docs/developer/04-web-sdk.md: the install snippet and identity verification
// panels (Settings → Sign-in & keys), and the SDK test page (/sdk/demo.html?key=<site key>).
// Nothing is saved: the identity secret stays behind "Show secret", and on the test page the SDK's
// calls to /v1/web/* are answered by the script, so no visitor is recorded in the docs workspace.
import { api, go, maskSecrets, session, settle, shot } from "../lib.mjs";

const AREA = "developer";

const panel = (page, heading) => page.locator("section.panel", { has: page.getByRole("heading", { name: heading, exact: true }) });

export default async function (browser) {
  const { context, page } = await session(browser);
  const me = await api(context, "GET", "/v1/auth/me");
  const siteKey = me.workspace.site_key;

  // 1. Settings → Sign-in & keys → Your website, and Identity verification (secret not shown).
  await go(page, "/settings/access");
  const website = panel(page, "Your website");
  await website.waitFor();
  await maskSecrets(page);
  await shot(page, AREA, "dev-04-snippet", { clip: website, pad: 8 });
  const identity = panel(page, "Identity verification");
  await identity.getByRole("button", { name: "Show secret" }).waitFor();
  await shot(page, AREA, "dev-04-identity", { clip: identity, pad: 8 });

  // 2. The SDK test page. Stub the browser API so the docs workspace gets no anonymous visitor.
  await page.route("**/v1/web/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/v1/web/decide") {
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ focus: [], content: {} }) });
    }
    return route.fulfill({ status: 202, body: "" });
  });
  await page.setViewportSize({ width: 1000, height: 640 });
  await page.goto(`/sdk/demo.html?key=${encodeURIComponent(siteKey)}`);
  await settle(page);
  await page.getByPlaceholder("external ID, e.g. cust-0007").fill("cust-0007");
  await shot(page, AREA, "dev-04-test-page", { clip: { x: 0, y: 0, width: 1000, height: 460 } });

  await context.close();
}
