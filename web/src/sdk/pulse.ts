// Pulse web SDK: track, identify, page views, on-site content (focus items and Dynamic Web Content)
// and push-token registration. Built to a single file (dist/sdk/pulse.js) and loaded with:
//
//   <script src="https://YOUR-PULSE/sdk/pulse.js" defer
//           onload="Pulse.init({ siteKey: 'sk_...' })"></script>
//
// Visitors are anonymous ("anon-…") until identify(userId). Focus items render in a Shadow DOM so
// neither the site's CSS nor ours leaks across.

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

function enqueue(ev: Props) {
  const hash = userHash();
  const props = hash ? { ...(ev.props as Props | undefined), user_hash: hash } : ev.props;
  queue.push({ external_id: currentId(), ts: new Date().toISOString(), id: randomId(), ...ev, props });
  if (queue.length >= 20) flush();
  else if (!timer) timer = window.setTimeout(flush, 1000);
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
  enqueue({ name: "identify", email: typeof email === "string" ? email : undefined, attrs: rest, props: { anonymous_id: previous } });
  flush();
  window.setTimeout(refresh, 1500); // new segments can change what this person should see
}

/** reset forgets the identified user (call it on logout). */
export function reset() {
  store.del("pulse.uid");
  store.del("pulse.uh");
  store.del("pulse.aid");
}

export function page(props?: Props) {
  track("page_view", { url: location.href, path: location.pathname, title: document.title, referrer: document.referrer, ...props });
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

interface FormField { key: string; label: string; type: string; required?: boolean; options?: string[]; default?: string }
interface FormDef {
  key: string; name: string; fields: FormField[]; shown: number;
  settings: { submit_text?: string; success_message?: string; redirect_url?: string; consent_text?: string; description?: string; theme?: string };
}

const FORM_CSS = ".pulse-f{display:grid;gap:12px;max-width:520px}.pulse-f label{display:grid;gap:4px;font-weight:600}" +
  ".pulse-f input:not([type=checkbox]),.pulse-f select,.pulse-f textarea{font:inherit;font-weight:400;padding:9px 11px;border:1px solid #c3cbd6;border-radius:6px}" +
  ".pulse-f .pc{display:flex;gap:8px;font-weight:400}.pulse-f .pe{color:#b42318;font-weight:400;font-size:.85em}" +
  ".pulse-f button{font:inherit;font-weight:600;color:#fff;border:0;border-radius:6px;padding:10px 16px;cursor:pointer}.pulse-f .ph{position:absolute;left:-9999px}";

function el<K extends keyof HTMLElementTagNameMap>(tag: K, attrs: Record<string, string> = {}, text?: string) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  if (text) e.textContent = text; // workspace text is never parsed as HTML
  return e;
}

/** form renders a Pulse form into target. Fields the visitor's profile already has may be skipped. */
export async function form(target: HTMLElement, key: string) {
  if (!cfg) return;
  const q = `?id=${encodeURIComponent(currentId())}&user_hash=${encodeURIComponent(userHash() ?? "")}`;
  const res = await fetch(`${cfg.host}/v1/web/forms/${encodeURIComponent(key)}${q}`).catch(() => undefined);
  if (!res?.ok) return;
  const def = (await res.json()) as FormDef;
  if (!document.getElementById("pulse-f-css")) document.head.append(el("style", { id: "pulse-f-css" }, FORM_CSS));
  const f = el("form", { class: "pulse-f", novalidate: "" });
  if (def.settings.description) f.append(el("p", {}, def.settings.description));
  const errs: Record<string, HTMLElement> = {};
  const field = (name: string, label: HTMLElement) => { errs[name] = el("span", { class: "pe", role: "alert" }); label.append(errs[name]); f.append(label); };
  for (const fd of def.fields) {
    if (fd.type === "hidden") { f.append(el("input", { type: "hidden", name: fd.key, value: fd.default ?? "" })); continue; }
    const label = el("label", fd.type === "checkbox" ? { class: "pc" } : {});
    const input = fd.type === "textarea" ? el("textarea") : fd.type === "select" ? el("select") : el("input", { type: fd.type === "checkbox" ? "checkbox" : fd.type });
    input.setAttribute("name", fd.key);
    if (fd.required) input.setAttribute("required", "");
    if (input instanceof HTMLSelectElement) {
      input.append(el("option", { value: "" }, "Choose…"));
      for (const o of fd.options ?? []) input.append(el("option", {}, o));
    }
    if (fd.type === "checkbox") label.append(input, el("span", {}, fd.label));
    else label.append(el("span", {}, fd.label + (fd.required ? " *" : "")), input);
    field(fd.key, label);
  }
  if (def.settings.consent_text) {
    const label = el("label", { class: "pc" });
    label.append(el("input", { type: "checkbox", name: "_consent", required: "" }), el("span", {}, def.settings.consent_text));
    field("_consent", label);
  }
  const hp = el("input", { class: "ph", name: "_hp", tabindex: "-1", autocomplete: "off", "aria-hidden": "true" });
  const btn = el("button", { type: "submit", style: `background:${/^#[0-9a-f]{6}$/i.test(def.settings.theme ?? "") ? def.settings.theme : "#2747d6"}` }, def.settings.submit_text || "Submit");
  f.append(hp, btn);
  f.addEventListener("submit", async (e) => {
    e.preventDefault();
    const values: Record<string, string> = {};
    for (const [k, v] of new FormData(f)) if (k !== "_hp") values[k] = String(v);
    Object.values(errs).forEach((x) => (x.textContent = ""));
    btn.disabled = true;
    const r = await fetch(`${cfg!.host}/v1/web/forms/${encodeURIComponent(key)}`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ values, id: currentId(), user_hash: userHash(), page_url: location.href, shown: def.shown, hp: hp.value }),
    }).catch(() => undefined);
    btn.disabled = false;
    const body = r ? await r.json().catch(() => ({})) : { error: "Couldn't send. Check your connection and try again." };
    if (r?.ok) {
      if (body.redirect_url) { location.href = body.redirect_url; return; }
      f.replaceWith(el("p", { class: "pulse-f", role: "status" }, body.message || "Thanks! We've got your details."));
      return;
    }
    for (const fe of body.fields ?? []) if (errs[fe.field]) errs[fe.field].textContent = fe.message;
    if (!body.fields) alert(body.error || "Something went wrong. Please try again.");
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
  if (cfg.autoPageview) page();
  const go = () => { mountForms(); void refresh(); };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", go);
  else go();
  addEventListener("pagehide", () => flush(true));
}
