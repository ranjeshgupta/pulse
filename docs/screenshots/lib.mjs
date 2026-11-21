// Shared helpers for the documentation screenshot scripts in shots/.
// Each script exports `default async function (browser)` and is run by run.mjs (`make docs-shots`).
// See README.md for how to write one.
import { createRequire } from "node:module";
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// The playwright package lives in .cache/docs-shots (installed by run.sh), not next to this file.
const here = dirname(fileURLToPath(import.meta.url));
const require = createRequire(resolve(here, "../../.cache/docs-shots/package.json"));
const { chromium } = require("playwright");

/** Where Pulse is. The container runs on the host network, so localhost is the host's Pulse. */
export const BASE = process.env.PULSE_URL || "http://localhost:7080";

/** The seeded "Acme Retail" workspace (`make docs-seed`; credentials in deploy/dev-seed.md). */
export const DOCS = {
  workspace: process.env.PULSE_DOCS_WORKSPACE || "acme-retail",
  email: process.env.PULSE_DOCS_EMAIL || "docs.admin@pulse.test",
  password: process.env.PULSE_DOCS_PASSWORD || "pulse-docs-pass-1",
};

/** Viewports: desktop for every guide; mobile only where a guide talks about small screens. */
export const VIEWPORTS = {
  desktop: { viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 },
  mobile: { viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true },
};

const DOCS_DIR = resolve(here, "..");

export async function launch() {
  return chromium.launch();
}

/**
 * A fresh browser context (own cookies), optionally signed in.
 * @param {import('playwright').Browser} browser
 * @param {{ size?: 'desktop'|'mobile', signIn?: {workspace:string,email:string,password:string}|null }} [opts]
 */
export async function session(browser, { size = "desktop", signIn = DOCS } = {}) {
  const storageState = signIn ? await signedIn(browser, signIn) : undefined;
  const context = await browser.newContext({
    storageState,
    ...VIEWPORTS[size],
    baseURL: BASE,
    locale: "en-GB",
    timezoneId: "Asia/Kolkata",
    colorScheme: "light",
    reducedMotion: "reduce",
  });
  context.setDefaultTimeout(20_000);
  const page = await context.newPage();
  return { context, page };
}

// Pulse allows 10 sign-in attempts per workspace and email in 15 minutes (successful ones count
// too), so a session is signed in once and its cookie kept in .cache/docs-shots/sessions/ for the
// next contexts and the next runs, as long as it still works.
const SESSIONS = resolve(here, "../../.cache/docs-shots/sessions");
const checked = new Map();

async function signedIn(browser, creds) {
  const key = `${creds.workspace}|${creds.email}`;
  if (checked.has(key)) return checked.get(key);
  const file = join(SESSIONS, `${creds.workspace}-${creds.email.replace(/[^a-z0-9.]+/gi, "_")}.json`);
  const ctx = await browser.newContext({ baseURL: BASE });
  try {
    if (existsSync(file)) {
      await ctx.addCookies(JSON.parse(readFileSync(file, "utf8")).cookies);
      const me = await ctx.request.get(`${BASE}/v1/auth/me`);
      if (!me.ok() || (await me.json()).workspace?.slug !== creds.workspace) {
        await ctx.clearCookies();
        await login(ctx, creds);
      }
    } else {
      await login(ctx, creds);
    }
    const state = await ctx.storageState();
    mkdirSync(SESSIONS, { recursive: true });
    writeFileSync(file, JSON.stringify(state), { mode: 0o600 });
    checked.set(key, state);
    return state;
  } finally {
    await ctx.close();
  }
}

/** Signs in through the API (one request, not the form), so the session cookie is set for the context. */
export async function login(context, { workspace, email, password }) {
  const res = await context.request.post(`${BASE}/v1/auth/login`, {
    data: { workspace, email, password },
    headers: { "X-Pulse-CSRF": "1" },
  });
  if (!res.ok()) {
    throw new Error(`sign-in to ${workspace} as ${email} failed: ${res.status()} ${await res.text()}` +
      (workspace === DOCS.workspace ? " (run `make docs-seed`)" : ""));
  }
}

/** Calls the Pulse API with the context's session (for setup a guide needs before a shot). */
export async function api(context, method, path, data) {
  const res = await context.request.fetch(`${BASE}${path}`, {
    method,
    data,
    headers: { "X-Pulse-CSRF": "1", "Content-Type": "application/json" },
  });
  const text = await res.text();
  if (!res.ok()) throw new Error(`${method} ${path}: ${res.status()} ${text}`);
  return text ? JSON.parse(text) : null;
}

/** Opens an app path (e.g. "/contacts") and waits until it has settled. */
export async function go(page, path) {
  await page.goto(path);
  await settle(page);
}

/** Waits for the network to go quiet, fonts to load and loading placeholders to go away. */
export async function settle(page, { quiet = 300 } = {}) {
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.evaluate(() => document.fonts.ready);
  await page.waitForFunction(() => !document.querySelector('[aria-busy="true"], .skeleton, .spinner'), null, { timeout: 10_000 })
    .catch(() => {});
  await page.waitForTimeout(quiet);
}

/**
 * Hides secrets before a capture: server API keys (pk_), integration keys (pzk_) and anything you
 * pass in `selectors` (inputs and text). The first characters stay, so readers see what kind of
 * key it is. Site keys (sk_) are public and stay.
 */
export async function maskSecrets(page, selectors = []) {
  await page.evaluate((sels) => {
    const secret = /\b(pk_|pzk_)[A-Za-z0-9_-]{6,}/g;
    const mask = (s) => s.replace(secret, (m, p) => p + "••••••••••••••••");
    for (const el of document.querySelectorAll("input, textarea")) {
      if (secret.test(el.value)) el.value = mask(el.value);
      secret.lastIndex = 0;
    }
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walk.nextNode(); n; n = walk.nextNode()) {
      if (secret.test(n.nodeValue)) n.nodeValue = mask(n.nodeValue);
      secret.lastIndex = 0;
    }
    for (const sel of sels) {
      for (const el of document.querySelectorAll(sel)) {
        if ("value" in el && el.tagName !== "BUTTON") el.value = "••••••••••••••••••••••••";
        else el.textContent = "••••••••••••••••••••••••";
      }
    }
  }, selectors);
}

/**
 * Draws a "click here" outline around an element (a locator), on top of the page. Remove it with
 * clearHighlights before the next shot of the same page.
 */
export async function highlight(locator, { color = "#e8590c", pad = 4 } = {}) {
  await locator.scrollIntoViewIfNeeded();
  const box = await locator.boundingBox();
  if (!box) throw new Error("highlight: element is not visible");
  await locator.page().evaluate(({ box, color, pad }) => {
    const d = document.createElement("div");
    d.className = "docs-shot-highlight";
    Object.assign(d.style, {
      position: "absolute", zIndex: 2147483647, pointerEvents: "none", borderRadius: "8px",
      left: `${box.x + window.scrollX - pad}px`, top: `${box.y + window.scrollY - pad}px`,
      width: `${box.width + pad * 2}px`, height: `${box.height + pad * 2}px`,
      outline: `3px solid ${color}`, boxShadow: `0 0 0 6px ${color}33`,
    });
    document.body.appendChild(d);
  }, { box, color, pad });
}

export async function clearHighlights(page) {
  await page.evaluate(() => document.querySelectorAll(".docs-shot-highlight").forEach((d) => d.remove()));
}

/**
 * Saves a PNG to docs/<area>/img/<name>.png and returns its path relative to docs/.
 * - clip: a locator (the element, or the box around all elements it matches, plus `pad` pixels;
 *   scroll it into view first if needed) or {x, y, width, height}
 * - fullPage: the whole scrollable page (use sparingly; prefer a clip)
 * Without either it captures the viewport.
 */
export async function shot(page, area, name, { clip, pad = 16, fullPage = false } = {}) {
  const file = join(DOCS_DIR, area, "img", `${name}.png`);
  mkdirSync(dirname(file), { recursive: true });
  let rect;
  if (clip && typeof clip.boundingBox === "function") {
    // A locator matching several elements clips to the box around all of them.
    await clip.first().scrollIntoViewIfNeeded();
    const boxes = [];
    for (const el of await clip.all()) {
      const b = await el.boundingBox();
      if (b) boxes.push(b);
    }
    if (boxes.length === 0) throw new Error(`shot ${name}: clip element is not visible`);
    const left = Math.min(...boxes.map((b) => b.x)), top = Math.min(...boxes.map((b) => b.y));
    const b = { x: left, y: top, width: Math.max(...boxes.map((b) => b.x + b.width)) - left,
      height: Math.max(...boxes.map((b) => b.y + b.height)) - top };
    const vp = page.viewportSize();
    const x = Math.max(0, b.x - pad), y = Math.max(0, b.y - pad);
    rect = { x, y, width: Math.min(vp.width - x, b.width + pad * 2), height: Math.min(vp.height - y, b.height + pad * 2) };
  } else if (clip) {
    rect = clip;
  }
  await page.screenshot({ path: file, clip: rect, fullPage, animations: "disabled", caret: "hide" });
  const kb = Math.round(statSync(file).size / 1024);
  const rel = `${area}/img/${name}.png`;
  console.log(`  ${rel} (${kb} KB)`);
  return rel;
}

/** Polls fn() until it returns something truthy (and returns that), or throws after `timeout` ms. */
export async function until(fn, { timeout = 30_000, every = 1000, what = "condition" } = {}) {
  const end = Date.now() + timeout;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, every));
  }
}

/** A short unique suffix for things a script creates (names, emails), so reruns don't collide. */
export function runId() {
  return Date.now().toString(36).slice(-5);
}

/**
 * Answers the app's confirm prompt (an alertdialog such as "Discard your changes?" or "Leave the
 * drag-and-drop builder?") with the given button, if one opens within `timeout` ms. Returns whether
 * it did. Use it after closing a filled-in dialog or switching editors.
 */
export async function confirmIfAsked(page, button, { timeout = 1500 } = {}) {
  const ask = page.getByRole("alertdialog");
  try {
    await ask.waitFor({ timeout });
  } catch {
    return false;
  }
  const answer = ask.getByRole("button", { name: button, exact: true });
  if (!(await answer.isVisible())) {
    throw new Error(`confirmIfAsked: no "${button}" button in the prompt: ${(await ask.innerText()).replace(/\s+/g, " ")}`);
  }
  await answer.click();
  await ask.waitFor({ state: "detached" });
  return true;
}
