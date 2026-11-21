// UI regression crawler: opens every page of the web app (lists, "new" pages, detail pages of real
// records, unknown ids and odd query parameters), clicks through every tab and opens the safe
// panels (previews, variable picker, filter menus, AI help with a mocked API), at desktop and phone
// width, in the seeded workspace and in a brand-new empty one. Any uncaught error, console error,
// error page or 5xx response is a failure.
//
//   make ui-crawl                      (Pulse at http://localhost:7080, `make docs-seed` done once)
//   ONLY=templates make ui-crawl       (only paths matching a regular expression)
//   WORKERS=2 NOFRESH=1 NOMOBILE=1     (fewer parallel tabs; skip the empty workspace / phone pass)
//
// It never clicks anything that saves, sends, deletes or launches: buttons are clicked only when
// their name is on the SAFE list and not on the UNSAFE list.
import { api, BASE, launch, session } from "../../docs/screenshots/lib.mjs";

/** Like lib.mjs settle, without waitForFunction (Pulse's CSP forbids the eval it uses). */
async function settle(page, { quiet = 300 } = {}) {
  await page.waitForLoadState("networkidle", { timeout: 5_000 }).catch(() => {});
  const end = Date.now() + 10_000;
  while (Date.now() < end) {
    const busy = await page.evaluate(() => !!document.querySelector('[aria-busy="true"], .skeleton, .spinner')).catch(() => false);
    if (!busy) break;
    await page.waitForTimeout(100);
  }
  await page.waitForTimeout(quiet);
}

const ONLY = process.env.ONLY ? new RegExp(process.env.ONLY) : null;
const WORKERS = Number(process.env.WORKERS || 4);
const VERBOSE = !!process.env.VERBOSE;

/** Buttons worth opening: they show something and change nothing on the server. */
const SAFE = /^(✨ AI|Preview|Preview as contact|Preview request|See what they get|Add filter|\+ Add filter|All filters|Filter|Filters|Show picks|HTML|HTML \(web view\)|Rich text|Plain text|Text|Desktop|Mobile|📊 Report|Report|Results|Steps|Day by day|Hour by hour|Zoom to fit|Known visitor|Card|Form|Blocks|Default|After submit|Before|After|Limits|Python|Node\.js|curl|Drag and drop|✏️ Content|Content|Code|Explain|Details|Columns|Go to Schema|Add a condition|Add condition|Add rule|Add event|Add variable|Define field|Adjust|Fill in the form|Check this report|Check this answer|Always applies|Copy a variable|Variable|\{ \} Variable)$/i;
/** Never clicked, whatever the SAFE list says. */
const UNSAFE = /password|everywhere|delete|remove|send|launch|schedule|approve|reject|archive|publish|save|import|merge|sync|rotate|sign out|turn (on|off)|pull|start|activate|pause|resume|stop|unsubscribe|mark|restore|reset|connection|duplicate|create|upload|discard|enforce|ask the gateway|check now|test/i;

const failures = [];
const visits = [];
let clicks = 0;

function fail(where, kind, detail) {
  const key = `${where.ws}|${where.size}|${where.path}|${where.step || ""}|${kind}|${String(detail).slice(0, 160)}`;
  if (failures.some((f) => f.key === key)) return;
  failures.push({ key, ...where, kind, detail: String(detail).slice(0, 1200) });
  console.log(`  ✗ [${where.ws}/${where.size}] ${where.path}${where.step ? ` › ${where.step}` : ""}: ${kind}: ${String(detail).split("\n")[0].slice(0, 300)}`);
}

/** The AI endpoints answer as if a key were set, so ✨ AI shows and its panels can be opened. */
async function mockAI(context) {
  await context.route("**/v1/ai/**", async (route) => {
    const url = route.request().url();
    const body = url.endsWith("/status") ? { enabled: true, model: "mock" }
      : url.includes("subject-lines") ? { subjects: ["Mocked idea one", "Mocked idea two"] }
      : url.includes("rewrite") ? { text: "Mocked rewrite" }
      : url.includes("translate") ? { fields: {} }
      : url.includes("segment") ? { rule: { op: "gte", event: "purchase", count: 1, days: 30 }, note: "Mocked" }
      : {};
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });
}

/** A tab in a browser context, collecting errors into `errs` until they are read. */
async function tab(context) {
  const page = await context.newPage();
  const errs = [];
  page.on("pageerror", (e) => errs.push(["pageerror", `${e.message}\n${e.stack ?? ""}`]));
  page.on("console", (m) => {
    if (m.type() !== "error") return;
    const t = m.text();
    // A 4xx is an answer the page shows (not found, not allowed); 5xx responses are caught below.
    if (/Failed to load resource: the server responded with a status of 4\d\d/.test(t)) return;
    // Requests cut off by leaving the page, or by the host's network changing under Docker.
    if (/Failed to load resource: net::ERR_(ABORTED|NETWORK_CHANGED)/.test(t)) return;
    errs.push(["console", t]);
  });
  page.on("response", (r) => {
    if (r.status() >= 500) errs.push(["http " + r.status(), `${r.request().method()} ${r.url()}`]);
  });
  page.on("dialog", (d) => d.accept().catch(() => {})); // leave-page questions from the browser
  return { page, errs };
}

const ERROR_PAGE = /Something went wrong on this page|Unexpected Application Error/;

async function check(t, where) {
  for (const [kind, detail] of t.errs.splice(0)) fail(where, kind, detail);
  const text = await t.page.evaluate(() => document.body?.innerText ?? "").catch(() => "");
  if (ERROR_PAGE.test(text)) {
    const details = await t.page.locator("details pre").first().textContent({ timeout: 1000 }).catch(() => "");
    fail(where, "error page", details || text.slice(0, 400));
    return false;
  }
  return true;
}

async function open(t, path) {
  try {
    await t.page.goto(path, { waitUntil: "domcontentloaded", timeout: 30_000 });
  } catch (e) {
    t.errs.push(["navigation", e.message.split("\n")[0]]);
  }
  await settle(t.page, { quiet: 250 });
}

/** Closes whatever a click opened; answers "Discard your changes?" with Keep editing. */
async function closeUp(page) {
  for (let i = 0; i < 3; i++) {
    const keep = page.getByRole("button", { name: "Keep editing" });
    if (await keep.isVisible().catch(() => false)) { await keep.click().catch(() => {}); break; }
    await page.keyboard.press("Escape").catch(() => {});
    await page.waitForTimeout(120);
  }
  const keep = page.getByRole("button", { name: "Keep editing" });
  if (await keep.isVisible().catch(() => false)) await keep.click().catch(() => {});
}

async function visible(loc) {
  return loc.isVisible().catch(() => false);
}

/**
 * Finds the next element to click in one round trip: visible, enabled, not done yet. Tabs by their
 * name; buttons when SAFE (and not UNSAFE) or when they open a popup. The element gets a
 * data-crawl attribute to click it by.
 */
async function next(page, kind, done) {
  return page.evaluate(({ kind, done, safe, unsafe }) => {
    const SAFE_RE = new RegExp(safe, "i"), UNSAFE_RE = new RegExp(unsafe, "i");
    const shown = (el) => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden"; };
    const els = [...document.querySelectorAll(kind === "tab" ? '[role="tab"]' : "button")];
    for (const el of els) {
      if (!shown(el) || el.disabled || el.getAttribute("aria-disabled") === "true") continue;
      const label = (el.getAttribute("aria-label") || el.textContent || "").trim().replace(/\s+/g, " ").replace(/\s*\(\d+\)$/, "");
      if (!label) continue;
      let key = label;
      if (kind === "button") {
        const popup = el.hasAttribute("aria-haspopup") && el.getAttribute("aria-expanded") !== "true";
        if (UNSAFE_RE.test(label)) continue;
        if (!popup && !SAFE_RE.test(label) && !SAFE_RE.test(label.replace(/^\{ \}\s*/, ""))) continue;
        // Popup buttons can share a name (one "Variable" per field): each counts.
        if (popup) key = label + "#" + els.indexOf(el);
      }
      if (done.includes(key)) continue;
      document.querySelectorAll("[data-crawl]").forEach((x) => x.removeAttribute("data-crawl"));
      el.setAttribute("data-crawl", "1");
      return { key, label };
    }
    return null;
  }, { kind, done, safe: SAFE.source, unsafe: UNSAFE.source }).catch(() => null);
}

/** Clicks every tab, then the safe buttons, the variable picker and the menus they open. */
async function explore(t, path, where) {
  const { page } = t;
  const home = new URL(page.url()).pathname;
  const back = async () => {
    if (new URL(page.url()).pathname !== home) await open(t, path);
  };
  // Tabs (found again after each click: a tab can change what's on the page).
  const tabsDone = [];
  for (let i = 0; i < 30; i++) {
    const n = await next(page, "tab", tabsDone);
    if (!n) break;
    tabsDone.push(n.key);
    await page.locator("[data-crawl]").click({ timeout: 3000 }).catch(() => {});
    clicks++;
    await settle(page, { quiet: 150 });
    if (!(await check(t, { ...where, step: `tab "${n.label}"` }))) await open(t, path);
  }
  if (tabsDone.length) await back();

  // Safe buttons and popup buttons.
  const done = [];
  for (let i = 0; i < 40; i++) {
    const n = await next(page, "button", done);
    if (!n) break;
    done.push(n.key);
    await page.locator("[data-crawl]").click({ timeout: 3000 }).catch(() => {});
    clicks++;
    await settle(page, { quiet: 200 });
    const step = { ...where, step: `button "${n.label}"` };
    if (!(await check(t, step))) { await open(t, path); continue; }
    await insidePopup(t, step);
    await closeUp(page);
    await check(t, step);
    await back();
  }
}

/** In an open variable picker, menu or AI dialog: look at a few entries without inserting anything. */
async function insidePopup(t, where) {
  const { page } = t;
  const vp = page.locator(".vp-pop");
  if (await visible(vp)) {
    const items = vp.locator(".vp-item");
    const n = Math.min(await items.count(), 60);
    // Each group's first entry, plus every event variable (they come from the event schema).
    const groups = vp.locator(".vp-group");
    const picks = new Set([0]);
    for (let g = 0, at = 0; g < (await groups.count()); g++) {
      picks.add(at);
      at += await groups.nth(g).locator(".vp-item").count();
    }
    for (let i = 0; i < n; i++) {
      const txt = (await items.nth(i).textContent().catch(() => "")) || "";
      if (/event\./.test(txt)) picks.add(i);
    }
    for (const i of [...picks].slice(0, 25)) {
      const item = vp.locator(".vp-item").nth(i);
      if (!(await visible(item))) continue;
      await item.click({ timeout: 2000 }).catch(() => {});
      clicks++;
      await page.waitForTimeout(80);
      if (!(await check(t, { ...where, step: `${where.step} › variable ${i}` }))) return;
      const back = vp.getByRole("button", { name: "Back" });
      if (await visible(back)) await back.click().catch(() => {});
    }
    return;
  }
  const menu = page.locator('[role="menu"]:visible, [role="listbox"]:visible').first();
  if (await visible(menu)) {
    const items = menu.locator('[role="menuitem"], [role="option"], button');
    const n = Math.min(await items.count(), 12);
    for (let i = 0; i < n; i++) {
      const it = menu.locator('[role="menuitem"], [role="option"], button').nth(i);
      const label = ((await it.textContent().catch(() => "")) || "").trim();
      if (!(await visible(it)) || UNSAFE.test(label)) continue;
      await it.click({ timeout: 2000 }).catch(() => {});
      clicks++;
      await page.waitForTimeout(150);
      if (!(await check(t, { ...where, step: `${where.step} › "${label.slice(0, 40)}"` }))) return;
      await closeUp(page);
      // Reopen the menu for the next entry, if it closed.
      if (!(await visible(menu))) break;
    }
    return;
  }
  const ai = page.getByRole("dialog", { name: "✨ AI help" });
  if (await visible(ai)) {
    for (const name of ["Suggest", "Rewrite"]) {
      const b = ai.getByRole("button", { name });
      if (await visible(b) && !(await b.isDisabled())) {
        await b.click().catch(() => {});
        await settle(page, { quiet: 150 });
        await check(t, { ...where, step: `${where.step} › ${name}` });
      }
    }
  }
}

/** A list of up to n items spread over the whole array (first, last and between). */
function spread(arr, n) {
  if (arr.length <= n) return arr;
  const out = [];
  for (let i = 0; i < n; i++) out.push(arr[Math.round((i * (arr.length - 1)) / (n - 1))]);
  return [...new Set(out)];
}

async function get(context, path, pick) {
  try { return pick(await api(context, "GET", path)) ?? []; } catch (e) { console.log(`  (setup: ${e.message.slice(0, 160)})`); return []; }
}

const CHANNELS = ["email", "sms", "whatsapp", "rcs", "push", "inbox"];

/** Every route, with the records this workspace has. */
async function targets(context) {
  const T = [];
  const add = (path, opts = {}) => T.push({ path, ...opts });
  const templates = await get(context, "/v1/templates", (r) => r.templates);
  const lists = ["/", "/contacts", "/contacts/duplicates", "/contacts/import", "/companies", "/segments", "/segments?type=static",
    "/events", "/campaigns", "/campaigns/calendar", "/on-site", "/templates", "/templates/blocks", "/catalog", "/surveys",
    "/landing-pages", "/forms", "/assets", "/reports", "/funnels", "/cohorts", "/revenue", "/customer-value", "/paths",
    "/analytics/users", "/analytics/campaigns", "/analytics/products", "/analytics/live", "/account",
    "/settings", "/settings/team", "/settings/roles", "/settings/access", "/settings/audit", "/settings/attributes",
    "/settings/scoring", "/settings/stages", "/settings/data", "/settings/identity", "/settings/automation",
    "/settings/channels", "/settings/test-inbox", "/settings/bounces", "/settings/web-push", "/settings/integrations",
    "/settings/webhooks"];
  for (const p of lists) add(p, { main: true });
  // Old addresses that redirect.
  for (const p of ["/journeys", "/broadcasts", "/lists", "/lists/1", "/imports", "/focus-items", "/focus-items/1", "/web-content",
    "/web-content/1", "/in-app", "/in-app/1", "/channels", "/test-inbox", "/campaigns/new", "/no-such-page"]) add(p, { light: true });

  // "New" pages.
  for (const c of [...CHANNELS, "xxx"]) add(`/templates/new?channel=${c}`);
  add("/templates/new");
  for (const c of [...CHANNELS, "xxx"]) add(`/campaigns/new/${c}`);
  for (const s of ["event", "segment", "xxx"]) add(`/journeys/new?start=${s}`);
  add("/journeys/new");
  for (const p of ["/segments/new", "/surveys/new", "/landing-pages/new", "/forms/new", "/reports/new", "/funnels/new",
    "/cohorts/new", "/on-site/focus/new", "/on-site/content/new", "/on-site/content/new?platform=app",
    "/on-site/content/new?platform=xxx", "/on-site/in-app/new", "/broadcasts/new", "/settings/webhooks/new",
    "/settings/integrations/new", "/settings/integrations/new?kind=xxx"]) add(p);
  for (const k of await get(context, "/v1/integrations/catalog", (r) => (r.kinds ?? []).map((k) => k.id))) add(`/settings/integrations/new?kind=${k}`);

  // Odd query parameters.
  for (const p of ["/templates?channel=xxx", "/templates?tab=nope", "/campaigns?tab=nope", "/campaigns?status=nope",
    "/campaigns?channel=xxx", "/campaigns?q=%25%25%27%22", "/campaigns?tag=%E2%9C%A8", "/campaigns?new=1", "/campaigns?tab=nope&new=1",
    "/on-site?channel=xxx", "/on-site?channel=website&type=xxx", "/on-site?channel=app", "/on-site?channel=website&type=focus",
    "/segments?type=xxx", "/contacts?open=nobody-at-all", "/contacts/import?segment=999999999", "/contacts/import?segment=abc",
    "/login?next=%2Fcontacts&error=xxx"]) add(p, { light: true });
  // Sign-in states: the two-step parts single sign-on lands on, and its error messages. Looked at
  // only (never submitted): the crawler's session stays signed in.
  for (const p of ["/login?two_factor=code", "/login?two_factor=setup", "/login?error=sso_link_needed", "/login?two_factor=xxx"]) add(p, { light: true });
  for (const q of ["is:nonsense", "segment:999999999", 'segment:"No such segment"', "points:>abc", "points:>", "event:purchase period:2026-13-45",
    "no_event:", "tag:%22%22", "((( )))", "%", "\\", "email:@ name:' OR 1=1 --", "value:Nope churn:maybe", "unsubscribed:xxx",
    "company:999999 stage:nope owner:nobody", "journey:999999 step:x outcome:y", "campaign:abc run:-1", "product:x category:y active:z",
    "template:abc via:x utm:y", "amp_poll:1 answer:2 form:3", "a".repeat(600)]) {
    add(`/contacts?q=${encodeURIComponent(q)}`, { light: true });
  }

  // Names that are also keys every plain object inherits ("constructor", "toString", "__proto__"):
  // a lookup table indexed by one must not return a function.
  for (const k of ["constructor", "toString", "__proto__", "hasOwnProperty"]) {
    for (const p of [`/templates?channel=${k}`, `/templates/new?channel=${k}`, `/templates/new?channel=email&event=${k}`, `/campaigns?tab=${k}`,
      `/campaigns?status=${k}&channel=${k}&tag=${k}`, `/campaigns/new/${k}`, `/journeys/new?start=${k}`, `/on-site?channel=${k}&type=${k}`,
      `/on-site/content/new?platform=${k}`, `/segments?type=${k}`, `/settings/integrations/new?kind=${k}`, `/events/${k}`,
      `/contacts?q=${encodeURIComponent(`is:${k} stage:${k} value:${k} churn:${k} unsubscribed:${k} ${k}:${k}`)}`, `/login?error=${k}`,
      `/contacts/${k}`, `/contacts/import?segment=${k}`]) add(p, { light: true });
    if (templates?.[0]) add(`/templates/${templates[0].id}?tab=${k}&event=${k}`, { light: true });
  }

  // Unknown ids: a not-found message, not a crash.
  for (const p of ["/contacts/nobody-xyz-123", "/companies/999999999", "/companies/abc", "/segments/999999999", "/segments/abc",
    "/journeys/999999999", "/journeys/abc", "/campaigns/one-time/999999999", "/broadcasts/999999999",
    "/broadcasts/abc", "/on-site/focus/999999999", "/on-site/content/999999999", "/on-site/in-app/999999999",
    "/templates/999999999", "/templates/abc", "/surveys/999999999", "/landing-pages/999999999", "/forms/999999999",
    "/forms/abc", "/reports/999999999", "/funnels/999999999", "/cohorts/999999999", "/settings/integrations/999999999",
    "/settings/webhooks/999999999"]) add(p, { notFound: true });

  // Records.
  for (const c of CHANNELS) {
    for (const x of spread(templates.filter((t) => t.channel === c), 3)) add(`/templates/${x.id}`);
  }
  const tplByChannel = Object.fromEntries(CHANNELS.map((c) => [c, templates.find((t) => t.channel === c)]));
  for (const c of CHANNELS) if (tplByChannel[c]) add(`/templates/${tplByChannel[c].id}?tab=report`);
  if (templates[0]) add(`/templates/${templates[0].id}?tab=nope`, { light: true });

  // Events: every event's page, and the template editor scoped to each event with a schema.
  const names = await get(context, "/v1/events/names", (r) => r.names);
  const schemas = await get(context, "/v1/events/schemas", (r) => r.schemas);
  for (const n of names) add(`/events/${encodeURIComponent(n)}`, { light: !schemas.some((s) => s.name === n) });
  const rich = (s) => (s.props ?? []).some((p) => ["list", "object"].includes(p.type) || /\[\]|\./.test(p.path || p.key));
  for (const s of schemas) {
    add(`/templates/new?channel=email&event=${encodeURIComponent(s.name)}`);
    if (rich(s) || s.name === "purchase") {
      for (const c of CHANNELS.slice(1)) add(`/templates/new?channel=${c}&event=${encodeURIComponent(s.name)}`);
      for (const c of CHANNELS) if (tplByChannel[c]) add(`/templates/${tplByChannel[c].id}?event=${encodeURIComponent(s.name)}`);
    }
  }
  for (const n of names.filter((n) => !schemas.some((s) => s.name === n)).slice(0, 5)) add(`/templates/new?channel=email&event=${encodeURIComponent(n)}`);
  add(`/templates/new?channel=email&event=no_such_event_xyz`);
  add("/events/no_such_event_xyz"); // an event not seen yet: its page is where a schema is defined first
  add(`/templates/new?channel=xxx&event=purchase`);

  const broadcasts = await get(context, "/v1/broadcasts", (r) => r.broadcasts);
  for (const c of CHANNELS) {
    for (const b of spread(broadcasts.filter((b) => b.channel === c), 2)) { add(`/broadcasts/${b.id}`); add(`/campaigns/one-time/${b.id}`); }
  }
  for (const st of [...new Set(broadcasts.map((b) => b.status))]) {
    const b = broadcasts.find((x) => x.status === st);
    add(`/broadcasts/${b.id}`); add(`/campaigns/one-time/${b.id}`);
  }
  const journeys = await get(context, "/v1/journeys", (r) => r.journeys);
  for (const j of spread(journeys, 8)) add(`/journeys/${j.id}`);
  for (const st of [...new Set(journeys.map((j) => j.status))]) add(`/journeys/${journeys.find((j) => j.status === st).id}`);
  const segments = await get(context, "/v1/segments", (r) => r.segments);
  for (const s of spread(segments.filter((s) => s.kind !== "static"), 6)) add(`/segments/${s.id}`);
  for (const s of spread(segments.filter((s) => s.kind === "static"), 3)) add(`/segments/${s.id}`);
  for (const s of spread(await get(context, "/v1/lists", (r) => r.lists), 2)) add(`/segments/${s.id}`);
  for (const f of spread(await get(context, "/v1/forms", (r) => r.forms), 5)) add(`/forms/${f.id}`);
  for (const p of spread(await get(context, "/v1/landing-pages", (r) => r.pages), 5)) add(`/landing-pages/${p.page?.id ?? p.id}`);
  for (const s of spread(await get(context, "/v1/surveys", (r) => r.surveys), 4)) add(`/surveys/${s.id}`);
  for (const x of spread(await get(context, "/v1/focus-items", (r) => r.focus_items), 4)) add(`/on-site/focus/${x.id}`);
  for (const x of spread(await get(context, "/v1/dwc", (r) => r.slots), 4)) add(`/on-site/content/${x.id}`);
  for (const x of spread(await get(context, "/v1/inapp", (r) => r.messages), 4)) add(`/on-site/in-app/${x.id}`);
  for (const x of await get(context, "/v1/reports", (r) => r.reports)) add(`/reports/${x.id}`);
  for (const x of await get(context, "/v1/funnels", (r) => r.funnels)) add(`/funnels/${x.id}`);
  for (const x of await get(context, "/v1/cohorts", (r) => r.cohorts)) add(`/cohorts/${x.id}`);
  for (const x of await get(context, "/v1/integrations", (r) => r.integrations)) add(`/settings/integrations/${x.id}`);
  for (const x of await get(context, "/v1/settings/webhooks", (r) => r.webhooks)) add(`/settings/webhooks/${x.id}`);
  for (const x of spread(await get(context, "/v1/companies", (r) => r.companies), 3)) add(`/companies/${x.id}`);

  // Contacts: known and anonymous ones, the ones with the most attributes and with list or object values.
  const contacts = await get(context, "/v1/contacts?limit=500", (r) => r.contacts);
  const odd = (c) => Object.values(c.attrs ?? {}).some((v) => v !== null && typeof v === "object");
  const picks = new Set([
    ...spread(contacts, 4),
    ...[...contacts].sort((a, b) => Object.keys(b.attrs ?? {}).length - Object.keys(a.attrs ?? {}).length).slice(0, 3),
    ...contacts.filter(odd).slice(0, 3),
    ...contacts.filter((c) => !c.email).slice(0, 2),
    ...contacts.filter((c) => /[^\w.@+-]/.test(c.external_id)).slice(0, 2),
  ]);
  const tabs = ["overview", "messages", "events", "consent", "nope"];
  let first = true;
  for (const c of picks) {
    add(`/contacts/${encodeURIComponent(c.external_id)}`);
    if (first) for (const tb of tabs) add(`/contacts/${encodeURIComponent(c.external_id)}?tab=${tb}`, { light: true });
    first = false;
  }
  return T;
}

/**
 * Odd but valid data, in a disposable workspace: event schemas whose property paths clash (a list
 * with items[] fields, a string with children, objects inside lists, enums without values,
 * dependencies on missing properties) and contacts and events with nested, empty, long and
 * markup-like values. Returns the pages that show them.
 */
async function seedOdd(context, key) {
  const T = [];
  const schemas = {
    odd_order: [
      { key: "items", type: "list" }, { key: "item_code", path: "items[].code", type: "string" },
      { key: "item_price", path: "items[].price", type: "number" }, { key: "item_tags", path: "items[].tags", type: "list" },
      { key: "item_meta", path: "items[].meta", type: "object" }, { key: "item_meta_size", path: "items[].meta.size", type: "string" },
      { key: "tags", type: "list" }, { key: "product", type: "object" }, { key: "product_code", path: "product.code", type: "string" },
      { key: "product_dims", path: "product.dims", type: "object" }, { key: "product_dims_w", path: "product.dims.w", type: "number" },
      { key: "amount", type: "number", required: true }, { key: "paid_on", type: "date" }, { key: "gift", type: "bool" },
      { key: "method", type: "enum", values: [{ value: "card", label: "Card" }, { value: "upi" }] },
      { key: "card_last4", type: "string", depends_on: { prop: "method", values: ["card"] } },
      { key: "upi_id", type: "string", personal: true, depends_on: { prop: "method", values: ["upi"] } },
    ],
    odd_clash: [
      { key: "name", type: "string" }, { key: "name_first", path: "name.first", type: "string" },
      { key: "list", type: "list" }, { key: "list_x", path: "list.x", type: "string" },
      { key: "obj", type: "object" }, { key: "obj_items", path: "obj.items[].id", type: "number" },
      { key: "orphan", type: "string", depends_on: { prop: "missing", values: ["x"] } },
      { key: "deep", path: "a.b.c.d.e", type: "string" }, { key: "nested_list", path: "rows[].cells[].v", type: "string" },
    ],
    odd_empty: [],
    // Inherited object keys as names (event, property, path and enum value).
    constructor: [
      { key: "constructor", type: "string" }, { key: "toString", type: "object" }, { key: "valueOf", path: "toString.valueOf", type: "number" },
      { key: "hasOwnProperty", type: "list" }, { key: "proto_items", path: "hasOwnProperty[].constructor", type: "string" },
      { key: "kind", type: "enum", values: [{ value: "constructor" }, { value: "__proto__" }] },
    ],
  };
  for (const [name, props] of Object.entries(schemas)) {
    // Each schema alone, then without the properties the server refuses, so the rest still lands.
    let body = { label: `Odd ${name}`, description: "Made by the UI crawler", mode: "warn", props };
    try {
      await api(context, "PUT", `/v1/events/${name}/schema`, body);
    } catch (e) {
      console.log(`  (odd schema ${name}: ${e.message.slice(0, 200)}; trying property by property)`);
      const ok = [];
      for (const p of props) {
        try { await api(context, "PUT", `/v1/events/${name}/schema`, { ...body, props: [...ok, p] }); ok.push(p); } catch { /* refused */ }
      }
      console.log(`  (odd schema ${name}: ${ok.length}/${props.length} properties kept)`);
    }
    T.push({ path: `/events/${name}` });
    for (const c of CHANNELS) T.push({ path: `/templates/new?channel=${c}&event=${name}` });
    T.push({ path: `/journeys/new?start=event` });
  }
  for (const [path, body, method] of [
    ["/v1/attributes/constructor", { key: "constructor", label: "constructor", type: "string", description: "" }, "PUT"],
    ["/v1/attributes/toString", { key: "toString", label: "toString", type: "enum", description: "", options: [{ value: "constructor" }] }, "PUT"],
    ["/v1/segments", { name: "constructor", kind: "rule", rule: { op: "gte", event: "constructor", count: 1, days: 30 } }],
    ["/v1/segments", { name: "__proto__", kind: "rule", rule: { all: [{ attr: "constructor", op: "eq", value: "toString" }] } }],
  ]) {
    try { await api(context, method ?? "POST", path, body); } catch (e) { console.log(`  (odd ${path} ${body.key ?? body.name}: ${e.message.slice(0, 160)})`); }
  }
  for (const s of await get(context, "/v1/segments", (r) => r.segments)) T.push({ path: `/segments/${s.id}` });
  const long = "x".repeat(2000);
  const events = [
    { external_id: "odd-nested", name: "odd_order", email: "odd.nested@example.com",
      attrs: { first_name: "Ödd 🧪", tags_attr: ["a", "b"], profile: { city: "Pune", geo: { lat: 1, lng: 2 } }, empty: "", nothing: null, n: 0, flag: false, html: "<b>bold</b><script>x</script>", long },
      props: { items: [{ code: "A", price: 1, tags: ["x"], meta: { size: "M" } }, "plain", null], tags: ["t"], product: { code: "P", dims: { w: 3 } }, amount: 12.5, method: "card", card_last4: "4242" } },
    { external_id: "odd-clash", name: "odd_clash", attrs: { list: [], obj: {}, arr_of_obj: [{ a: 1 }, { b: [2] }], date_like: "2026-13-45", num_str: "1e999" },
      props: { name: { first: "A" }, list: "not a list", obj: { items: "nope" }, status: "weird", a: { b: "flat" }, rows: [{ cells: [{ v: 1 }] }] } },
    { external_id: "odd \"quoted\" / id?#&", name: "odd_clash", props: { name: "plain", list: [1, [2, 3], { x: 1 }] } },
    { external_id: "odd-empty", name: "odd_empty", props: {} },
    { external_id: "constructor", name: "constructor", email: "constructor@example.com",
      attrs: { constructor: "c", toString: "t", valueOf: 1, hasOwnProperty: ["x"], __proto__x: "p" },
      props: { constructor: "x", toString: { valueOf: 2 }, hasOwnProperty: [{ constructor: "y" }], kind: "__proto__" } },
    { external_id: "toString", name: "toString", props: { constructor: { prototype: 1 } } },
    { external_id: "odd-anon-" + Date.now(), name: "page_view", props: { url: "https://example.com/?q=<x>", ref: null } },
  ];
  for (const e of events) {
    const r = await context.request.post(`${BASE}/v1/events`, { data: e, headers: { Authorization: `Bearer ${key}` } });
    if (!r.ok()) console.log(`  (odd event ${e.name} for ${e.external_id}: ${r.status()} ${(await r.text()).slice(0, 200)})`);
  }
  // Events are processed in the background: wait for the contacts to appear.
  const end = Date.now() + 30_000;
  let contacts = [];
  while (Date.now() < end) {
    contacts = await get(context, "/v1/contacts?limit=50", (r) => r.contacts);
    if (contacts.length >= 6) break;
    await new Promise((r) => setTimeout(r, 1000));
  }
  for (const c of contacts) {
    const id = encodeURIComponent(c.external_id);
    for (const tb of ["", "?tab=messages", "?tab=events", "?tab=consent"]) T.push({ path: `/contacts/${id}${tb}` });
  }
  for (const p of ["/contacts", "/events", "/", "/analytics/users", "/analytics/live", "/paths", "/paths?start=constructor", "/settings/attributes",
    "/segments", "/segments/new", "/events/toString", "/settings/scoring", "/reports/new", "/funnels/new", "/cohorts/new",
    `/contacts?q=${encodeURIComponent("segment:constructor event:constructor")}`]) T.push({ path: p });
  return [...new Map(T.map((t) => [t.path, t])).values()];
}

/** Runs targets on `n` tabs of one context in parallel. */
async function crawl(context, list, where, { interact = true } = {}) {
  const queue = [...list];
  const worker = async () => {
    const t = await tab(context);
    while (queue.length) {
      const target = queue.shift();
      const w = { ...where, path: target.path };
      const started = Date.now();
      await open(t, target.path);
      const ok = await check(t, w);
      if (ok && target.notFound) {
        const text = await t.page.evaluate(() => (document.querySelector("main") ?? document.body).innerText).catch(() => "");
        if (!/not found|doesn't exist|does not exist|no longer exists|deleted|couldn't find|can't find|isn't there|no such|wasn't found/i.test(text)) {
          fail(w, "no not-found message", text.replace(/\s+/g, " ").slice(0, 300));
        }
      }
      if (ok && interact && !target.light && !target.notFound) await explore(t, target.path, w);
      await check(t, w);
      visits.push({ ...w, ms: Date.now() - started });
      if (VERBOSE) console.log(`  ✓ [${where.ws}/${where.size}] ${target.path} (${Date.now() - started} ms)`);
    }
    await t.page.close();
  };
  await Promise.all(Array.from({ length: Math.min(WORKERS, list.length) }, worker));
}

const only = (list) => (ONLY ? list.filter((t) => ONLY.test(t.path)) : list);

const browser = await launch();
const t0 = Date.now();
try {
  // 1. The seeded workspace, desktop.
  const { context } = await session(browser);
  await mockAI(context);
  const list = only(await targets(context));
  console.log(`acme-retail, desktop: ${list.length} pages`);
  await crawl(context, list, { ws: "acme-retail", size: "desktop" });

  // 2. Phone width: the main pages, the "new" pages and one record of each kind.
  if (!process.env.NOMOBILE) {
    const m = await session(browser, { size: "mobile" });
    await m.page.close();
    await mockAI(m.context);
    const seen = new Set();
    const mobile = list.filter((t) => {
      if (t.light || t.notFound) return false;
      if (t.main || /\/new/.test(t.path)) return !t.path.includes("event=");
      const kind = t.path.replace(/\/\d+.*$|\/[^/]+$/, "");
      if (seen.has(kind)) return false;
      seen.add(kind);
      return true;
    });
    console.log(`acme-retail, phone (375 px): ${mobile.length} pages`);
    const page0 = await m.context.newPage();
    await page0.close();
    m.context.on("page", (p) => p.setViewportSize({ width: 375, height: 812 }).catch(() => {}));
    await crawl(m.context, mobile, { ws: "acme-retail", size: "phone" }, { interact: true });
    await m.context.close();
  }

  // 3. A brand-new workspace: every empty state.
  if (!process.env.NOFRESH) {
    const fresh = await session(browser, { signIn: null });
    await fresh.page.close();
    const run = Date.now().toString(36);
    const res = await fresh.context.request.post(`${BASE}/v1/auth/signup`, {
      data: { workspace: `UI crawl ${run}`, email: `ui-crawl-${run}@example.com`, password: "ui crawl password 1" },
      headers: { "X-Pulse-CSRF": "1" },
    });
    if (!res.ok()) {
      fail({ ws: "fresh", size: "desktop", path: "POST /v1/auth/signup" }, "setup", `${res.status()} ${await res.text()}`);
    } else {
      await mockAI(fresh.context);
      const flist = only(await targets(fresh.context));
      console.log(`fresh workspace, desktop: ${flist.length} pages`);
      await crawl(fresh.context, flist, { ws: "fresh", size: "desktop" });

      // 4. The same workspace with odd data: schemas whose paths clash, contacts with nested values.
      const { api_key: key } = await res.json();
      const odd = only(await seedOdd(fresh.context, key));
      console.log(`fresh workspace with odd data: ${odd.length} pages`);
      await crawl(fresh.context, odd, { ws: "odd", size: "desktop" });
    }
    await fresh.context.close();
  }
  await context.close();
} finally {
  await browser.close();
}

const by = {};
for (const v of visits) by[`${v.ws}/${v.size}`] = (by[`${v.ws}/${v.size}`] ?? 0) + 1;
console.log(`\nVisited ${visits.length} pages (${Object.entries(by).map(([k, n]) => `${k}: ${n}`).join(", ")}) and clicked ${clicks} tabs, buttons and menu entries, in ${Math.round((Date.now() - t0) / 1000)} s.`);
if (failures.length) {
  console.log(`\n${failures.length} failure(s):`);
  for (const f of failures) console.log(`\n✗ [${f.ws}/${f.size}] ${f.path}${f.step ? ` › ${f.step}` : ""}\n  ${f.kind}: ${f.detail.replace(/\n/g, "\n    ")}`);
  process.exit(1);
}
console.log("No failures.");
