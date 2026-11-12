// The no-code web push opt-in prompt (Settings → Web push): when to show it, and its markup. The
// SDK shows it before the browser's own permission prompt, which opens only when the visitor clicks
// "Allow" (browsers require a click, and a refused browser prompt can't be asked again).

export interface PushPrompt {
  enabled: boolean;
  style: "bell" | "bar" | "modal";
  position?: string;
  title: string;
  text: string;
  allow: string;
  later: string;
  background?: string;
  text_color?: string;
  button?: string;
  delay_seconds: number;
  scroll_percent: number;
  page_views: number;
  repeat_days: number;
  show_on?: string[];
  hide_on?: string[];
}

export interface PromptState {
  permission: string; // Notification.permission, or "unsupported"
  dismissedAt: number | null; // when "Not now" was last clicked (ms)
  pageViews: number; // pages seen in this visit, including this one
  url: string; // the page
}

/** pagePart is the path and query of a URL (so "/shop" never matches a host like shop.example.com). */
export function pagePart(u: string): string {
  try {
    const p = new URL(u);
    return p.pathname + p.search;
  } catch {
    return u;
  }
}

/** pageAllowed applies the prompt's page rules: any of show_on (none: every page), none of hide_on. */
export function pageAllowed(p: Pick<PushPrompt, "show_on" | "hide_on">, url: string): boolean {
  const page = pagePart(url);
  const show = (p.show_on ?? []).filter(Boolean);
  if (show.length && !show.some((s) => page.includes(s))) return false;
  return !(p.hide_on ?? []).some((s) => s && page.includes(s));
}

/** promptBlocked says why the prompt must not show now ("" when it may, once its delay and scroll
 * triggers are met). */
export function promptBlocked(p: PushPrompt | undefined | null, s: PromptState, now: number): string {
  if (!p?.enabled) return "off";
  if (s.permission === "unsupported") return "unsupported";
  if (s.permission !== "default") return "decided"; // granted or denied: the browser's answer stands
  if (!pageAllowed(p, s.url)) return "page";
  if (s.dismissedAt != null) {
    if (!p.repeat_days) return "dismissed";
    if (now - s.dismissedAt < p.repeat_days * 86_400_000) return "dismissed";
  }
  if (p.page_views > 0 && s.pageViews < p.page_views) return "page_views";
  return "";
}

const esc = (s = "") => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
const color = (c: string | undefined, d: string) => (/^#[0-9a-f]{6}$/i.test(c ?? "") ? c! : d);

/** promptHTML is the prompt's markup (rendered in a Shadow DOM; every text is escaped). */
export function promptHTML(p: PushPrompt): string {
  const bg = color(p.background, "#ffffff"), fg = color(p.text_color, "#152238"), btn = color(p.button, "#2747d6");
  const left = p.position === "bottom-left";
  const card = `<div class="card" role="dialog" aria-label="${esc(p.title || "Notifications")}">
    ${p.title ? `<strong class="t">${esc(p.title)}</strong>` : ""}<p>${esc(p.text)}</p>
    <div class="b"><button class="later">${esc(p.later || "Not now")}</button><button class="allow">${esc(p.allow || "Allow")}</button></div></div>`;
  const css = `<style>
  :host{all:initial}
  .card{background:${bg};color:${fg};font:14px/1.45 system-ui,sans-serif;padding:16px 18px;border-radius:10px;box-shadow:0 12px 40px rgba(0,0,0,.22);max-width:340px}
  .t{display:block;font-size:16px;margin-bottom:4px}p{margin:0 0 12px}
  .b{display:flex;gap:8px;justify-content:flex-end}
  button{font:inherit;font-weight:600;border-radius:6px;padding:8px 14px;cursor:pointer;border:1px solid transparent}
  .allow{background:${btn};color:#fff}.later{background:transparent;color:${fg};border-color:rgba(0,0,0,.18)}
  .wrap{position:fixed;z-index:2147483646}
  .bell{width:52px;height:52px;border-radius:50%;background:${btn};color:#fff;border:0;box-shadow:0 6px 20px rgba(0,0,0,.25);display:grid;place-items:center;padding:0}
  .bell svg{width:24px;height:24px}
  .bar{left:0;right:0;${p.position === "bottom" ? "bottom:0" : "top:0"}}
  .bar .card{max-width:none;border-radius:0;display:flex;gap:16px;align-items:center;justify-content:center;flex-wrap:wrap}
  .bar .card p{margin:0}.bar .t{margin:0}
  .bd{position:fixed;inset:0;z-index:2147483645;background:rgba(15,23,42,.45)}
  .modal{left:50%;top:50%;transform:translate(-50%,-50%)}
  .pop{position:absolute;bottom:64px;${left ? "left:0" : "right:0"};width:min(320px,calc(100vw - 32px))}
  </style>`;
  if (p.style === "bar") return `${css}<div class="wrap bar">${card}</div>`;
  if (p.style === "modal") return `${css}<div class="bd"></div><div class="wrap modal">${card}</div>`;
  const side = left ? "left:16px" : "right:16px";
  return `${css}<div class="wrap" style="bottom:16px;${side}"><div class="pop" hidden>${card}</div>
    <button class="bell" aria-label="${esc(p.title || "Get notifications")}"><svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12 22a2.5 2.5 0 0 0 2.45-2h-4.9A2.5 2.5 0 0 0 12 22Zm7-6V11a7 7 0 0 0-5.5-6.84V3.5a1.5 1.5 0 0 0-3 0v.66A7 7 0 0 0 5 11v5l-2 2v1h18v-1l-2-2Z"/></svg></button></div>`;
}

/** checkToken reads ?pulse_check=… from a page URL (the install check's browser test). */
export function checkToken(url: string): string | null {
  try {
    const t = new URL(url).searchParams.get("pulse_check");
    return t && /^pc[0-9a-f]{20,62}$/.test(t) ? t : null;
  } catch {
    return null;
  }
}

export interface Diagnosis {
  sdk_version: string; url: string; https: boolean; push_supported: boolean; permission: string;
  service_worker: string; worker_found: boolean; worker_pulse: boolean; subscribed: boolean;
  prompt_enabled: boolean; identified: boolean; problems: string[];
}

/** problems lists, in plain words, what stops web push working, from a diagnosis's facts. */
export function problems(d: Omit<Diagnosis, "problems">): string[] {
  const out: string[] = [];
  if (!d.https) out.push("The page isn't on https, so the browser won't allow notifications.");
  if (!d.push_supported) out.push("This browser can't receive web push (on iPhone, add the site to the home screen first).");
  if (d.permission === "denied") out.push("Notifications are blocked for this site in this browser's settings.");
  if (d.push_supported && !d.worker_found) out.push(`No service worker at ${d.service_worker}.`);
  if (d.worker_found && !d.worker_pulse) out.push(`${d.service_worker} doesn't load Pulse's worker (importScripts(".../sdk/pulse-sw.js")).`);
  if (!d.prompt_enabled) out.push("The opt-in prompt is off (Settings → Web push), so visitors are only asked if your site calls Pulse.enablePush().");
  return out;
}
