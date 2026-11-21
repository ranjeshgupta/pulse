// Screenshots for docs/tutorials/19-product-recommendations.md, in the seeded docs workspace.
// Puts the six products the seed's shop events name into the catalog (idempotent: same codes each
// run), works out the daily lists, then takes the catalog, the Add product form (Cancelled) and
// "What would a contact see?" for a seeded buyer.
import { api, clearHighlights, go, highlight, session, settle, shot, until, confirmIfAsked } from "../lib.mjs";

const AREA = "tutorials";
const top = (height) => ({ x: 232, y: 0, width: 1440 - 232, height });

// Same codes, names and categories as cmd/seed, so the seeded events count toward the picks.
const PRODUCTS = [
  { code: "KIT-101", name: "Cast iron skillet", category: "Kitchen", price: 1899 },
  { code: "KIT-204", name: "Chef's knife", category: "Kitchen", price: 2499, sale_price: 2199 },
  { code: "DEC-310", name: "Brass table lamp", category: "Lighting", price: 3299 },
  { code: "DEC-115", name: "Jute rug", category: "Home decor", price: 4599, sale_price: 3999 },
  { code: "BED-220", name: "Cotton bedsheet set", category: "Bedding", price: 1599 },
  { code: "BED-404", name: "Memory foam pillow", category: "Bedding", price: 1299 },
].map((p) => ({ ...p, currency: "INR", in_stock: true, url: `https://acme.example.com/p/${p.code.toLowerCase()}` }));

export default async function (browser) {
  const { context, page } = await session(browser);
  await api(context, "PUT", "/v1/products", { products: PRODUCTS });
  // Lists worked out in the last 5 minutes are refused (429) and are fresh enough.
  await api(context, "POST", "/v1/products/refresh-lists").catch((e) => { if (!/: 429 /.test(e.message)) throw e; });

  // A seeded contact's recent shop visits, sent from a browser through the web SDK as a shop would:
  // two products looked at and one bought, so the picks show "bought lately" and "looked at".
  const buyer = "cust-0007";
  const me = await api(context, "GET", "/v1/auth/me");
  const { context: shopper, page: shop } = await session(browser, { signIn: null });
  await go(shop, `/sdk/demo.html?key=${me.workspace.site_key}`);
  await shop.evaluate((id) => {
    Pulse.identify(id);
    Pulse.track("product_viewed", { product_code: "DEC-310" });
    Pulse.track("product_viewed", { product_code: "BED-404" });
    Pulse.track("purchase", { amount: 1899, currency: "INR", items: [{ product_code: "KIT-101" }] });
    Pulse.flush();
  }, buyer);
  await shop.waitForTimeout(1000);
  await shopper.close();
  await until(async () => {
    const r = await api(context, "GET", `/v1/contacts/${buyer}/recommendations`).catch(() => null);
    return r?.bought?.length && r?.viewed?.length && r?.products?.length;
  }, { timeout: 60_000, every: 2000, what: `${buyer}'s product history and picks` });

  // 1. The catalog.
  await go(page, "/catalog");
  await page.getByRole("cell", { name: /Cast iron skillet/ }).first().waitFor();
  await settle(page, { quiet: 600 });
  await highlight(page.getByRole("button", { name: "Add product" }));
  await shot(page, AREA, "19-catalog", { clip: top(560) });
  await clearHighlights(page);

  // 2. Add product, filled in as in the tutorial, then Cancel.
  await page.getByRole("button", { name: "Add product" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.waitFor();
  await dialog.getByLabel("Code (SKU)").fill("KT-2041");
  await dialog.getByLabel("Name").fill("Steel pressure cooker, 5 L");
  await dialog.getByLabel("Link").fill("https://shop.example.com/p/kt-2041");
  await dialog.getByLabel("Price", { exact: true }).fill("2499");
  await dialog.getByLabel(/Sale price/).fill("1999");
  await dialog.getByLabel("Currency").fill("INR");
  await dialog.getByLabel("Category").fill("Kitchen");
  await page.evaluate(() => document.activeElement?.blur()); // no focus ring or cursor in the picture
  await settle(page);
  await shot(page, AREA, "19-add-product", { clip: dialog, pad: 0 });
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await confirmIfAsked(page, "Discard changes");

  // 3. What would a contact see?
  await page.setViewportSize({ width: 1440, height: 1400 });
  const panel = page.locator("section", { has: page.getByText("What would a contact see?") }).last();
  await panel.scrollIntoViewIfNeeded();
  await panel.getByLabel("Contact external ID").fill(buyer);
  await panel.getByRole("button", { name: "Show picks" }).click();
  await panel.getByText(/bought lately/i).first().waitFor({ timeout: 30_000 });
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "19-picks", { clip: panel, pad: 8 });
  await context.close();
}
