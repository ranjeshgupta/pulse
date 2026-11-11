// Pulse web SDK: track, identify, page views, on-site content (focus items and Dynamic Web Content)
// and push-token registration. Built to a single file (dist/sdk/pulse.js) and loaded with:
//
//   <script src="https://YOUR-PULSE/sdk/pulse.js" defer
//           onload="Pulse.init({ siteKey: 'sk_...' })"></script>
//
// Visitors are anonymous ("anon-…") until identify(userId). Focus items render in a Shadow DOM so
// neither the site's CSS nor ours leaks across. Events are grouped into sessions (30 minutes without
// an event ends one, shared across tabs); identify, page_view and session_start carry the device's
// platform, type, OS, browser and locale.

import { pageProps, parseTouch, profileAttrs, touchFromUrl } from "./attribution";
import { parseUserAgent, type UAData } from "./device";
import { nextSession, parseActivity } from "./session";
import { fileProblem, formPages, UPLOAD_TYPES, type FormField } from "./forms";

type Props = Record<string, unknown>;
interface Config {
  siteKey: string;
  host?: string; // defaults to the origin the script was loaded from
  autoPageview?: boolean; // default true
  onsite?: boolean; // default true: fetch and render focus items and content slots
  /** storagePrefix namespaces this page's stored ids (Pulse-hosted pages use the site key). */
  storagePrefix?: string;
}
interface FocusItem {
  id: number; kind: "modal" | "bar" | "slide";
  content: { title: string; body_html: string; image_url?: string; cta_text?: string; cta_url?: string; background?: string; text_color?: string; button_color?: string; position?: string };
  targeting: { trigger: string; trigger_value?: number; frequency: string };
}

const scriptHost = (() => {
  const s = document.currentScript as HTMLScriptElement | null;
  try { return s?.src ? new URL(s.src).origin : location.origin; } catch { return location.origin; }
})();

let cfg: Required<Config> | null = null;
let queue: Props[] = [];
let timer: number | undefined;

// Storage keys are "pulse.<name>"; with Config.storagePrefix they become "pulse.<prefix>.<name>", so
// several workspaces on one origin (Pulse-hosted landing pages) never share a visitor's identity.
const sk = (k: string) => (cfg?.storagePrefix ? k.replace(/^pulse\./, `pulse.${cfg.storagePrefix}.`) : k);
const store = {
  get(k: string) { try { return localStorage.getItem(sk(k)); } catch { return null; } },
  set(k: string, v: string) { try { localStorage.setItem(sk(k), v); } catch { /* private mode */ } },
  del(k: string) { try { localStorage.removeItem(sk(k)); } catch { /* ignore */ } },
  sget(k: string) { try { return sessionStorage.getItem(sk(k)); } catch { return null; } },
  sset(k: string, v: string) { try { sessionStorage.setItem(sk(k), v); } catch { /* ignore */ } },
};

function randomId() {
  const a = new Uint8Array(12);
  crypto.getRandomValues(a);
  return Array.from(a, (b) => b.toString(16).padStart(2, "0")).join("");
}

function anonymousId() {
  let id = store.get("pulse.aid");
  if (!id) { id = "anon-" + randomId(); store.set("pulse.aid", id); }
  return id;
}

/** The id events are recorded under: the identified user, else this browser's anonymous id. */
export function currentId() {
  return store.get("pulse.uid") || anonymousId();
}

function post(path: string, body: unknown, beacon = false) {
  if (!cfg) return Promise.resolve(undefined);
  const url = `${cfg.host}${path}?site_key=${encodeURIComponent(cfg.siteKey)}`;
  const json = JSON.stringify(body);
  if (beacon && navigator.sendBeacon) {
    navigator.sendBeacon(url, new Blob([json], { type: "text/plain" }));
    return Promise.resolve(undefined);
  }
  return fetch(url, { method: "POST", headers: { "Content-Type": "application/json", "X-Pulse-Site-Key": cfg.siteKey }, body: json, keepalive: true })
    .then((r) => (r.ok && r.status !== 202 && r.status !== 204 ? r.json() : undefined))
    .catch(() => undefined);
}

/** The identified user's hash from your server (identity verification), if one was given. */
function userHash() {
  return store.get("pulse.uid") ? store.get("pulse.uh") || undefined : undefined;
}

function push(ev: Props) {
  const hash = userHash();
  const props = hash ? { ...(ev.props as Props | undefined), user_hash: hash } : ev.props;
  queue.push({ external_id: currentId(), ts: new Date().toISOString(), id: randomId(), ...ev, props });
  if (queue.length >= 20) flush();
  else if (!timer) timer = window.setTimeout(flush, 1000);
}

/** enqueue queues an event, preceded by session_start when it begins a new session. */
function enqueue(ev: Props) {
  session();
  push(ev);
}

export function flush(beacon = false) {
  if (timer) { clearTimeout(timer); timer = undefined; }
  while (queue.length) post("/v1/web/events", { events: queue.splice(0, 50) }, beacon);
}

export function track(name: string, props?: Props) {
  enqueue({ name, props });
}

/**
 * identify ties this browser to your user id and updates their profile attributes. With identity
 * verification on, pass opts.userHash: hex(HMAC-SHA256(identity secret, userId)) from your server.
 */
export function identify(userId: string, attrs?: Props, opts?: { userHash?: string }) {
  const previous = currentId();
  store.set("pulse.uid", String(userId));
  if (opts?.userHash) store.set("pulse.uh", opts.userHash);
  else store.del("pulse.uh");
  const { email, ...rest } = attrs ?? {};
  // First- and last-touch campaigns go with every identify (your own attributes win).
  const merged = { ...attribution(), ...rest };
  enqueue({ name: "identify", email: typeof email === "string" ? email : undefined, attrs: merged, props: { ...deviceProps(), anonymous_id: previous } });
  flush();
  window.setTimeout(refresh, 1500); // new segments can change what this person should see
}

/** reset forgets the identified user (call it on logout). */
export function reset() {
  store.del("pulse.uid");
  store.del("pulse.uh");
  store.del("pulse.aid");
  store.del("pulse.ft"); // campaigns belong to the person, not the browser
  store.del("pulse.lt");
  store.del("pulse.sid"); // the next event starts a new session
  memSid = null;
}

export function page(props?: Props) {
  const sid = session();
  track("page_view", {
    url: location.href, path: location.pathname, title: document.title, referrer: document.referrer,
    ...pageProps(parseTouch(store.sget("pulse.st"))), // this session's campaign, for revenue attribution
    ...deviceProps(), session_id: sid,
    ...props,
  });
}

// ---------- device and session ----------

type UAHints = UAData & { getHighEntropyValues?: (hints: string[]) => Promise<Record<string, unknown>> };
const nav = () => navigator as Navigator & { userAgentData?: UAHints };
let device: Record<string, string> | null = null;

/** highEntropy reads the cached model and platformVersion Client Hints, ignoring anything malformed. */
function highEntropy(): Pick<UAData, "model" | "platformVersion"> {
  try {
    const v = JSON.parse(store.sget("pulse.uah") || "{}") as Record<string, unknown>;
    const str = (x: unknown) => (typeof x === "string" ? x.slice(0, 100) : undefined);
    return { model: str(v.model), platformVersion: str(v.platformVersion) };
  } catch { return {}; }
}

/** loadHighEntropy asks the browser once per session for the device model and exact OS version
 * (Chromium only). Events never wait for it; device props use it once it has arrived. */
function loadHighEntropy() {
  const ua = nav().userAgentData;
  if (!ua?.getHighEntropyValues || store.sget("pulse.uah")) return;
  ua.getHighEntropyValues(["model", "platformVersion"]).then((v) => {
    store.sset("pulse.uah", JSON.stringify({ model: v.model, platformVersion: v.platformVersion }));
    device = null;
  }).catch(() => { /* not allowed: keep the low-entropy values */ });
}

/** deviceProps are this browser's platform, device_type, os, os_version, browser, device_brand,
 * device_model and locale (unknown ones left out), sent with identify, page_view and session_start. */
export function deviceProps(): Record<string, string> {
  if (!device) {
    const n = nav();
    const ua = n.userAgentData;
    const data: UAData | undefined = ua ? { mobile: ua.mobile, platform: ua.platform, brands: ua.brands, ...highEntropy() } : undefined;
    const d: Record<string, string> = { ...parseUserAgent(n.userAgent, data, n.maxTouchPoints) };
    const locale = n.language?.trim().slice(0, 100);
    if (locale) d.locale = locale;
    device = d;
  }
  return device;
}

// The session lives in localStorage (shared by tabs); without usable storage, in memory.
let memSid: string | null = null;
let memLast: number | null = null;
let lsUsable: boolean | undefined;
function storageWorks() {
  if (lsUsable === undefined) {
    try { localStorage.setItem("pulse.t", "1"); localStorage.removeItem("pulse.t"); lsUsable = true; } catch { lsUsable = false; }
  }
  return lsUsable;
}

/** session returns the current session id, starting a new session (and queueing session_start)
 * after 30 minutes without events, and records this moment as the last activity. */
function session(): string {
  const now = Date.now();
  const ls = storageWorks();
  const sid = ls ? store.get("pulse.sid") : memSid;
  const last = ls ? parseActivity(store.get("pulse.sla")) : memLast;
  const s = nextSession(now, last, sid, randomId);
  memLast = now;
  store.set("pulse.sla", String(now));
  if (s.started) {
    memSid = s.sid;
    store.set("pulse.sid", s.sid);
    push({
      name: "session_start",
      props: {
        session_id: s.sid, url: location.href, path: location.pathname, referrer: document.referrer,
        ...deviceProps(), ...pageProps(parseTouch(store.sget("pulse.st"))),
      },
    });
  }
  return s.sid;
}

// ---------- UTM attribution ----------

/** captureTouch records the landing URL's campaign: first touch once, last touch and this session's every time. */
function captureTouch() {
  const t = touchFromUrl(location.href, document.referrer);
  if (!t) return;
  const json = JSON.stringify(t);
  if (!store.get("pulse.ft")) store.set("pulse.ft", json);
  store.set("pulse.lt", json);
  store.sset("pulse.st", json);
}

/** attribution is the visitor's first- and last-touch campaign as profile attributes (first_utm_source, ...). */
export function attribution(): Record<string, string> {
  return profileAttrs(parseTouch(store.get("pulse.ft")), parseTouch(store.get("pulse.lt")));
}

/** registerPushToken stores a web push token (e.g. from Firebase Messaging getToken) for this user. */
export function registerPushToken(token: string) {
  return post("/v1/web/devices", { external_id: currentId(), token, user_hash: userHash() });
}

/**
 * enablePush asks the visitor for notification permission and subscribes this browser to the
 * workspace's web push (no Firebase needed). Your site must serve a service worker file, by
 * default /pulse-sw.js, containing: importScripts("https://YOUR-PULSE/sdk/pulse-sw.js");
 * Call it from a user gesture (a click), as browsers require. Resolves to true when subscribed.
 */
export async function enablePush(opts: { serviceWorker?: string } = {}): Promise<boolean> {
  if (!cfg || !("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) return false;
  if ((await Notification.requestPermission()) !== "granted") return false;
  const res = await fetch(`${cfg.host}/v1/web/push-config?site_key=${encodeURIComponent(cfg.siteKey)}`).catch(() => undefined);
  if (!res?.ok) return false;
  const { vapid_public_key } = (await res.json()) as { vapid_public_key: string };
  const reg = await navigator.serviceWorker.register(opts.serviceWorker ?? "/pulse-sw.js");
  await navigator.serviceWorker.ready;
  const key = Uint8Array.from(atob(vapid_public_key.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(vapid_public_key.length / 4) * 4, "=")), (c) => c.charCodeAt(0));
  const sub = (await reg.pushManager.getSubscription()) ?? (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key }));
  await registerPushToken(JSON.stringify(sub));
  return true;
}

// ---------- app inbox ----------

export interface InboxMessage {
  id: number; message_id: string; title: string; body: string; image_url?: string; url?: string; category?: string;
  data?: Record<string, string>; created_at: string; read_at?: string; expires_at: string;
}

async function inboxAction(action: "read" | "delete" | "click", ids: number[]) {
  await post("/v1/web/inbox", { id: currentId(), user_hash: userHash(), action, message_ids: ids });
}

/**
 * inbox reads and updates this visitor's app inbox. list() returns the newest 50 messages (pass
 * before: the last id for older ones) and the unread count. Opening and clicking are counted in the
 * sending journey's or broadcast's report.
 */
export const inbox = {
  async list(before?: number): Promise<{ messages: InboxMessage[]; unread: number }> {
    if (!cfg) return { messages: [], unread: 0 };
    const q = new URLSearchParams({ site_key: cfg.siteKey, id: currentId() });
    const uh = userHash();
    if (uh) q.set("user_hash", uh);
    if (before) q.set("before", String(before));
    const res = await fetch(`${cfg.host}/v1/web/inbox?${q}`).catch(() => undefined);
    return res?.ok ? res.json() : { messages: [], unread: 0 };
  },
  /** markRead marks these messages read (none given: all of them). */
  markRead: (ids: number[] = []) => inboxAction("read", ids),
  remove: (ids: number[] = []) => inboxAction("delete", ids),
  /** click records a tap on a message; open its url yourself. */
  click: (id: number) => inboxAction("click", [id]),
};

// ---------- on-site content ----------

const shown = new Set<number>();

function frequencyAllows(it: FocusItem) {
  const key = `pulse.f.${it.id}`;
  const last = Number(store.get(key) || 0);
  switch (it.targeting.frequency) {
    case "once": return !last;
    case "session": return !store.sget(key);
    case "daily": return Date.now() - last > 86_400_000;
    default: return true;
  }
}

function remember(it: FocusItem) {
  store.set(`pulse.f.${it.id}`, String(Date.now()));
  store.sset(`pulse.f.${it.id}`, "1");
}

const esc = (s = "") => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
const safeUrl = (u = "") => (/^https?:\/\//i.test(u) ? u : "");
const color = (c: string | undefined, d: string) => (/^#[0-9a-f]{3,8}$/i.test(c ?? "") ? c! : d);

function render(it: FocusItem) {
  if (shown.has(it.id) || !frequencyAllows(it)) return;
  shown.add(it.id);
  remember(it);
  const c = it.content;
  const host = document.createElement("div");
  host.setAttribute("data-pulse-focus", String(it.id));
  const root = host.attachShadow({ mode: "open" });
  const bg = color(c.background, "#ffffff"), fg = color(c.text_color, "#152238"), btn = color(c.button_color, "#2747d6");
  const place = it.kind === "bar"
    ? `left:0;right:0;${c.position === "bottom" ? "bottom:0" : "top:0"};display:flex;gap:14px;align-items:center;justify-content:center;padding:12px 44px 12px 16px;border-radius:0`
    : it.kind === "slide" ? "right:16px;bottom:16px;width:min(320px,calc(100vw - 32px))"
    : "left:50%;top:50%;transform:translate(-50%,-50%);width:min(420px,calc(100vw - 32px))";
  const cta = c.cta_text && safeUrl(c.cta_url) ? `<a class="cta" href="${esc(safeUrl(c.cta_url))}">${esc(c.cta_text)}</a>` : "";
  root.innerHTML = `<style>
    .box{position:fixed;z-index:2147483646;${place};background:${bg};color:${fg};font:15px/1.45 system-ui,sans-serif;padding:20px 22px;border-radius:10px;box-shadow:0 12px 40px rgba(0,0,0,.22)}
    .bd{position:fixed;inset:0;z-index:2147483645;background:rgba(15,23,42,.45)}
    h2{font-size:18px;margin:0 0 6px}.body{margin:0}.body p{margin:0 0 8px}img{max-width:100%;border-radius:6px;margin-bottom:10px}
    .cta{display:inline-block;margin-top:10px;background:${btn};color:#fff;padding:9px 16px;border-radius:6px;text-decoration:none;font-weight:600}
    .x{position:absolute;top:8px;right:10px;border:0;background:none;color:inherit;font-size:20px;cursor:pointer;opacity:.6}
  </style>
  ${it.kind === "modal" ? '<div class="bd"></div>' : ""}
  <div class="box" role="${it.kind === "modal" ? "dialog" : "region"}" aria-label="${esc(c.title || "Message")}">
    <button class="x" aria-label="Close">×</button>
    ${c.image_url && safeUrl(c.image_url) ? `<img src="${esc(c.image_url)}" alt="">` : ""}
    ${c.title ? `<h2>${esc(c.title)}</h2>` : ""}<div class="body">${c.body_html || ""}</div>${cta}
  </div>`;
  const close = (how: string) => { host.remove(); track("focus_closed", { focus_id: it.id, how }); };
  root.querySelector(".x")?.addEventListener("click", () => close("button"));
  root.querySelector(".bd")?.addEventListener("click", () => close("backdrop"));
  root.querySelector(".cta")?.addEventListener("click", () => { track("focus_clicked", { focus_id: it.id }); flush(true); });
  document.addEventListener("keydown", function onKey(e) { if (e.key === "Escape" && host.isConnected) { close("escape"); document.removeEventListener("keydown", onKey); } });
  document.body.appendChild(host);
  track("focus_shown", { focus_id: it.id });
}

function arm(it: FocusItem) {
  const t = it.targeting;
  const show = () => render(it);
  if (t.trigger === "delay") window.setTimeout(show, (t.trigger_value ?? 5) * 1000);
  else if (t.trigger === "scroll") {
    const onScroll = () => {
      const h = document.documentElement;
      const pct = ((h.scrollTop + innerHeight) / Math.max(1, h.scrollHeight)) * 100;
      if (pct >= (t.trigger_value ?? 50)) { removeEventListener("scroll", onScroll); show(); }
    };
    addEventListener("scroll", onScroll, { passive: true });
    onScroll();
  } else if (t.trigger === "exit_intent") {
    const onOut = (e: MouseEvent) => { if (!e.relatedTarget && e.clientY <= 0) { document.removeEventListener("mouseout", onOut); show(); } };
    document.addEventListener("mouseout", onOut);
  } else show();
}

/** refresh asks Pulse what this visitor should see on this page and renders it. */
export async function refresh() {
  if (!cfg?.onsite) return;
  const slots = Array.from(document.querySelectorAll<HTMLElement>("[data-pulse-slot]"));
  const res = (await post("/v1/web/decide", {
    external_id: currentId(), user_hash: userHash(), url: location.href, slots: slots.map((s) => s.dataset.pulseSlot),
  })) as { focus: FocusItem[]; content: Record<string, string> } | undefined;
  if (!res) return;
  for (const el of slots) {
    const html = res.content[el.dataset.pulseSlot!];
    if (html !== undefined) el.innerHTML = html; // the workspace's own content
  }
  res.focus.forEach(arm);
}

// ---- forms ----

interface FormDef {
  key: string; name: string; fields: FormField[]; shown: number;
  settings: { submit_text?: string; success_message?: string; redirect_url?: string; consent_text?: string; description?: string; theme?: string };
}

const FORM_CSS = ".pulse-f{display:grid;gap:12px;max-width:520px}.pulse-f label{display:grid;gap:4px;font-weight:600}" +
  ".pulse-f input:not([type=checkbox]):not([type=file]),.pulse-f select,.pulse-f textarea{font:inherit;font-weight:400;padding:9px 11px;border:1px solid #c3cbd6;border-radius:6px}" +
  ".pulse-f .pc{display:flex;gap:8px;font-weight:400}.pulse-f .pe{color:#b42318;font-weight:400;font-size:.85em}.pulse-f .phint{font-weight:400;font-size:.85em;color:#5b6474}" +
  ".pulse-f fieldset{border:0;margin:0;padding:0;display:grid;gap:12px}.pulse-f legend{font-weight:700;padding:0;margin-bottom:4px}" +
  ".pulse-f .pst{font-size:.85em;color:#5b6474}.pulse-f .pbar{height:6px;background:#e4e8ee;border-radius:3px;margin-top:6px;overflow:hidden}.pulse-f .pbar i{display:block;height:100%}" +
  ".pulse-f .pnav{display:flex;gap:8px}.pulse-f .pback{background:#fff!important;color:#152238!important;border:1px solid #c3cbd6!important}" +
  ".pulse-f button{font:inherit;font-weight:600;color:#fff;border:0;border-radius:6px;padding:10px 16px;cursor:pointer}.pulse-f .ph{position:absolute;left:-9999px}";

function el<K extends keyof HTMLElementTagNameMap>(tag: K, attrs: Record<string, string> = {}, text?: string) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  if (text) e.textContent = text; // workspace text is never parsed as HTML
  return e;
}

/** form renders a Pulse form into target. Fields the visitor's profile already has may be skipped.
 * Page breaks split it into pages (each checked before the next shows, with a progress bar); file
 * fields are sent as multipart/form-data. */
export async function form(target: HTMLElement, key: string) {
  if (!cfg) return;
  const q = `?id=${encodeURIComponent(currentId())}&user_hash=${encodeURIComponent(userHash() ?? "")}`;
  const res = await fetch(`${cfg.host}/v1/web/forms/${encodeURIComponent(key)}${q}`).catch(() => undefined);
  if (!res?.ok) return;
  const def = (await res.json()) as FormDef;
  if (!document.getElementById("pulse-f-css")) document.head.append(el("style", { id: "pulse-f-css" }, FORM_CSS));
  const accent = /^#[0-9a-f]{6}$/i.test(def.settings.theme ?? "") ? def.settings.theme! : "#2747d6";
  const f = el("form", { class: "pulse-f", novalidate: "" });
  if (def.settings.description) f.append(el("p", {}, def.settings.description));
  const errs: Record<string, HTMLElement> = {};
  const fileFields: Record<string, FormField> = {};
  const pages = formPages(def.fields);
  const steps = el("div", { class: "pst" });
  const stepLabel = el("span");
  const bar = el("i", { style: `background:${accent}` });
  if (pages.length > 1) { const b = el("div", { class: "pbar" }); b.append(bar); steps.append(stepLabel, b); f.append(steps); }
  const sets = pages.map((p) => {
    const set = el("fieldset");
    if (p.title) set.append(el("legend", {}, p.title));
    for (const fd of p.fields) {
      if (fd.type === "hidden") { set.append(el("input", { type: "hidden", name: fd.key, value: fd.default ?? "" })); continue; }
      const label = el("label", fd.type === "checkbox" ? { class: "pc" } : {});
      const input = fd.type === "textarea" ? el("textarea") : fd.type === "select" ? el("select") : el("input", { type: fd.type === "checkbox" ? "checkbox" : fd.type });
      input.setAttribute("name", fd.key);
      if (fd.required) input.setAttribute("required", "");
      if (input instanceof HTMLSelectElement) {
        input.append(el("option", { value: "" }, "Choose…"));
        for (const o of fd.options ?? []) input.append(el("option", {}, o));
      }
      if (fd.type === "file") {
        fileFields[fd.key] = fd;
        input.setAttribute("accept", (fd.accept?.length ? fd.accept : UPLOAD_TYPES).join(","));
        input.addEventListener("change", () => (input as HTMLInputElement).setCustomValidity(""));
      }
      if (fd.type === "checkbox") label.append(input, el("span", {}, fd.label));
      else label.append(el("span", {}, fd.label + (fd.required ? " *" : "")), input);
      if (fd.type === "file") label.append(el("span", { class: "phint" }, `Up to ${fd.max_mb || 5} MB.`));
      errs[fd.key] = el("span", { class: "pe", role: "alert" });
      label.append(errs[fd.key]);
      set.append(label);
    }
    f.append(set);
    return set;
  });
  const last = el("div", { style: "display:grid;gap:12px" });
  if (def.settings.consent_text) {
    const label = el("label", { class: "pc" });
    label.append(el("input", { type: "checkbox", name: "_consent", required: "" }), el("span", {}, def.settings.consent_text));
    errs._consent = el("span", { class: "pe", role: "alert" });
    label.append(errs._consent);
    last.append(label);
  }
  const hp = el("input", { class: "ph", name: "_hp", tabindex: "-1", autocomplete: "off", "aria-hidden": "true" });
  const btn = el("button", { type: "submit", style: `background:${accent}` }, def.settings.submit_text || "Submit");
  const nav = el("div", { class: "pnav" });
  const back = el("button", { type: "button", class: "pback" }, "Back");
  const next = el("button", { type: "button", style: `background:${accent}` }, "Next");
  last.append(btn);
  nav.append(back, next, last);
  f.append(hp, nav);
  let cur = 0;
  const show = (i: number) => {
    cur = i;
    sets.forEach((s, j) => (s.hidden = j !== i));
    back.hidden = i === 0;
    next.hidden = i === sets.length - 1;
    last.hidden = i !== sets.length - 1;
    stepLabel.textContent = `Page ${i + 1} of ${sets.length}`;
    bar.style.width = `${Math.round(((i + 1) * 100) / sets.length)}%`;
  };
  // check validates one page in the browser (required, email, number, file size and type).
  const check = (scope: HTMLElement) => {
    let ok = true;
    scope.querySelectorAll<HTMLInputElement>("input,select,textarea").forEach((x) => {
      const fd = fileFields[x.name];
      if (fd && x.files?.[0]) x.setCustomValidity(fileProblem(fd, x.files[0]));
      if (errs[x.name]) errs[x.name].textContent = x.checkValidity() ? "" : x.validationMessage;
      if (!x.checkValidity()) ok = false;
    });
    return ok;
  };
  next.addEventListener("click", () => { if (check(sets[cur])) show(cur + 1); });
  back.addEventListener("click", () => show(cur - 1));
  show(0);
  f.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!check(sets[cur]) || !check(last)) return;
    const values: Record<string, string> = {};
    const files: [string, File][] = [];
    for (const [k, v] of new FormData(f)) {
      if (k === "_hp") continue;
      if (fileFields[k]) { if (v instanceof File && v.size > 0) files.push([k, v]); continue; }
      values[k] = String(v);
    }
    Object.values(errs).forEach((x) => (x.textContent = ""));
    btn.disabled = true;
    const payload = JSON.stringify({ values, id: currentId(), user_hash: userHash(), page_url: location.href, shown: def.shown, hp: hp.value, attribution: attribution() });
    let body: BodyInit = payload;
    const headers: Record<string, string> = {};
    if (files.length) {
      const fd = new FormData(); // the browser sets the multipart boundary
      fd.append("payload", payload);
      for (const [k, file] of files) fd.append(k, file, file.name);
      body = fd;
    } else headers["Content-Type"] = "application/json";
    const r = await fetch(`${cfg!.host}/v1/web/forms/${encodeURIComponent(key)}`, { method: "POST", headers, body }).catch(() => undefined);
    btn.disabled = false;
    const out = r ? await r.json().catch(() => ({})) : { error: "Couldn't send. Check your connection and try again." };
    if (r?.ok) {
      if (out.redirect_url) { location.href = out.redirect_url; return; }
      f.replaceWith(el("p", { class: "pulse-f", role: "status" }, out.message || "Thanks! We've got your details."));
      return;
    }
    let first = -1;
    for (const fe of out.fields ?? []) {
      if (errs[fe.field]) errs[fe.field].textContent = fe.message;
      const page = sets.findIndex((s) => s.contains(errs[fe.field] ?? null));
      if (page >= 0 && (first < 0 || page < first)) first = page;
    }
    if (first >= 0) show(first);
    if (!out.fields) alert(out.error || "Something went wrong. Please try again.");
  });
  target.replaceChildren(f);
}

function mountForms() {
  document.querySelectorAll<HTMLElement>("[data-pulse-form]").forEach((n) => {
    if (n.dataset.pulseMounted) return;
    n.dataset.pulseMounted = "1";
    void form(n, n.dataset.pulseForm!);
  });
}

export function init(c: Config) {
  if (!c?.siteKey) throw new Error("Pulse.init needs a siteKey");
  cfg = { host: scriptHost, autoPageview: true, onsite: true, storagePrefix: "", ...c, siteKey: c.siteKey } as Required<Config>;
  cfg.host = cfg.host.replace(/\/$/, "");
  anonymousId();
  captureTouch();
  loadHighEntropy();
  if (cfg.autoPageview) page();
  const go = () => { mountForms(); void refresh(); };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", go);
  else go();
  addEventListener("pagehide", () => flush(true));
}
