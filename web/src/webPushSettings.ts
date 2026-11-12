// Settings → Web push: pure helpers for the opt-in prompt settings and the install check. The
// rules mirror the server's (internal/webcheck/prompt.go), so most mistakes show before saving.
import { promptHTML, type PushPrompt } from "./sdk/pushPrompt";

export type PromptStyle = PushPrompt["style"];
export type PromptConfig = PushPrompt;

export interface WebPushSettings {
  prompt: PromptConfig;
  site_url?: string;
  service_worker?: string;
}

export interface WebPushResponse { settings: WebPushSettings; defaults: WebPushSettings; pulse_url: string }

export type CheckStatus = "ok" | "warn" | "fail" | "skip";
export type CheckKey = "https" | "page" | "snippet" | "site_key" | "service_worker" | "sw_imports" | "vapid";
export interface CheckItem { key: CheckKey | string; label: string; status: CheckStatus; detail?: string }
export interface CheckReport { site_url: string; service_worker_url: string; ok: boolean; checked_at: string; items: CheckItem[] }

export interface CheckToken { token: string; url: string; expires_at: string }
export interface BrowserReport {
  sdk_version: string; url: string; https: boolean; push_supported: boolean; permission: string;
  service_worker: string; worker_found: boolean; worker_pulse: boolean; subscribed: boolean;
  prompt_enabled: boolean; identified: boolean; problems: string[]; reported_at: string; site_key_matches: boolean;
}

export const DEFAULT_SERVICE_WORKER = "/pulse-sw.js";
export const MAX_RULES = 20;

export const STYLES: { value: PromptStyle; label: string; hint: string }[] = [
  { value: "bell", label: "Bell", hint: "A small round bell in a corner; the message opens when it's clicked." },
  { value: "bar", label: "Bar", hint: "A slim strip across the top or bottom of the page." },
  { value: "modal", label: "Pop-up", hint: "A box in the middle of the page over a dimmed background." },
];

/** positionsFor lists where a style can sit (the pop-up has no choice: the middle). */
export function positionsFor(style: PromptStyle): { value: string; label: string }[] {
  if (style === "bell") return [{ value: "bottom-right", label: "Bottom right" }, { value: "bottom-left", label: "Bottom left" }];
  if (style === "bar") return [{ value: "top", label: "Top" }, { value: "bottom", label: "Bottom" }];
  return [];
}

/** withStyle switches the style, keeping the position when it still fits, else the first one. */
export function withStyle(p: PromptConfig, style: PromptStyle): PromptConfig {
  const opts = positionsFor(style);
  const position = opts.length === 0 ? "" : opts.some((o) => o.value === p.position) ? p.position : opts[0].value;
  return { ...p, style, position };
}

/** normaliseList trims page rules, drops empty and repeated ones and keeps at most MAX_RULES. */
export function normaliseList(list: string[] | undefined | null): string[] {
  const out: string[] = [];
  for (const raw of list ?? []) {
    const v = raw.trim();
    if (v && !out.includes(v)) out.push(v);
  }
  return out.slice(0, MAX_RULES);
}

/** normalise tidies settings before saving: page rules, the site address and the worker path. */
export function normalise(s: WebPushSettings): WebPushSettings {
  return {
    ...s,
    prompt: { ...s.prompt, show_on: normaliseList(s.prompt.show_on), hide_on: normaliseList(s.prompt.hide_on) },
    site_url: (s.site_url ?? "").trim(),
    service_worker: (s.service_worker ?? "").trim() || DEFAULT_SERVICE_WORKER,
  };
}

// The server counts bytes (Go's len), so a limit of 60 means 60 bytes.
const bytes = (s = "") => new TextEncoder().encode(s).length;
const COLOR = /^#[0-9a-fA-F]{6}$/;
const SW = /^\/[A-Za-z0-9._/-]{1,200}\.js$/;
const int = (n: number) => Number.isInteger(n);

/** validate returns what's wrong with the settings, in plain words ([] when they can be saved). */
export function validate(s: WebPushSettings): string[] {
  const p = s.prompt;
  const out: string[] = [];
  if (p.style === "bell") {
    if (p.position && p.position !== "bottom-right" && p.position !== "bottom-left") out.push("The bell sits bottom right or bottom left.");
  } else if (p.style === "bar") {
    if (p.position && p.position !== "top" && p.position !== "bottom") out.push("The bar sits at the top or the bottom.");
  } else if (p.style !== "modal") {
    out.push("Choose a bell, a bar or a pop-up.");
  }
  if (p.enabled && (!p.text?.trim() || !p.allow?.trim())) out.push("Write the message and the Allow button's label.");
  if (bytes(p.title) > 60) out.push("The title is longer than 60 characters.");
  if (bytes(p.allow) > 60) out.push("The Allow button's label is longer than 60 characters.");
  if (bytes(p.later) > 60) out.push("The Not now button's label is longer than 60 characters.");
  if (bytes(p.text) > 300) out.push("The message is longer than 300 characters.");
  if ([p.background, p.text_color, p.button].some((c) => c && !COLOR.test(c))) out.push("Colours look like #1a2b3c.");
  if (!int(p.delay_seconds) || p.delay_seconds < 0 || p.delay_seconds > 600) out.push("The delay is 0 to 600 seconds.");
  if (!int(p.scroll_percent) || p.scroll_percent < 0 || p.scroll_percent > 100) out.push("Scrolling is 0 to 100%.");
  if (!int(p.page_views) || p.page_views < 0 || p.page_views > 50) out.push("Pages seen is 0 to 50.");
  if (!int(p.repeat_days) || p.repeat_days < 0 || p.repeat_days > 365) out.push("Ask again after 0 to 365 days.");
  const show = p.show_on ?? [], hide = p.hide_on ?? [];
  if (show.length > MAX_RULES || hide.length > MAX_RULES) out.push(`At most ${MAX_RULES} page rules of each kind.`);
  if ([...show, ...hide].some((u) => !u.trim() || bytes(u) > 300)) out.push("Page rules are 1 to 300 characters.");
  if (s.site_url && !siteURLOk(s.site_url)) out.push("The site address must look like https://www.example.com/");
  if (s.service_worker && (!SW.test(s.service_worker) || s.service_worker.includes(".."))) out.push("The service worker path must look like /pulse-sw.js");
  return out;
}

function siteURLOk(v: string): boolean {
  try {
    const u = new URL(v);
    return (u.protocol === "https:" || u.protocol === "http:") && !!u.host && !u.username && !u.password;
  } catch {
    return false;
  }
}

/** siteWarning flags a site address the check (and web push) can't use, beyond the format. */
export function siteWarning(v: string | undefined): string {
  const s = (v ?? "").trim();
  if (!s || !siteURLOk(s)) return "";
  return s.startsWith("http:") ? "Web push only works on https sites, and the check only opens https addresses." : "";
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

function joinAnd(parts: string[]): string {
  if (parts.length <= 1) return parts.join("");
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

/** triggerSummary says when the prompt appears: "After 5 seconds, once they've scrolled 50% and seen 2 pages". */
export function triggerSummary(p: Pick<PromptConfig, "delay_seconds" | "scroll_percent" | "page_views">): string {
  const when = p.delay_seconds > 0 ? `After ${plural(p.delay_seconds, "second")}` : "Straight away";
  const once: string[] = [];
  if (p.scroll_percent > 0) once.push(`scrolled ${p.scroll_percent}%`);
  if (p.page_views > 1) once.push(`seen ${plural(p.page_views, "page")}`);
  return once.length ? `${when}, once they've ${joinAnd(once)}` : when;
}

/** frequencySummary says what happens after "Not now". */
export function frequencySummary(repeatDays: number): string {
  if (!repeatDays) return "After “Not now”, the prompt isn't shown again in that browser.";
  return `After “Not now”, the prompt is shown again after ${plural(repeatDays, "day")}.`;
}

/** pagesSummary says where the prompt can appear. */
export function pagesSummary(p: Pick<PromptConfig, "show_on" | "hide_on">): string {
  const show = normaliseList(p.show_on), hide = normaliseList(p.hide_on);
  const q = (l: string[]) => joinAnd(l.map((s) => `“${s}”`)).replace(/ and (“[^”]*”)$/, " or $1");
  const where = show.length ? `On pages whose address contains ${q(show)}` : "On every page";
  return hide.length ? `${where}, except where it contains ${q(hide)}.` : `${where}.`;
}

/** swSnippet is the one line the site's service worker file holds. */
export function swSnippet(pulseUrl: string): string {
  const base = (pulseUrl || "").trim().replace(/\/+$/, "");
  return `importScripts("${base}/sdk/pulse-sw.js");`;
}

/** previewDoc is a small page for an iframe preview of the prompt, with the bell's message open. */
export function previewDoc(p: PromptConfig): string {
  const html = promptHTML(p).replace('<div class="pop" hidden>', '<div class="pop">');
  return `<!doctype html><html><head><meta charset="utf-8"><style>
html,body{margin:0;height:100%;background:#f4f6f9;font:13px system-ui,sans-serif;color:#8a94a6;overflow:hidden}
.fake{padding:22px 26px}.fake i{display:block;height:10px;border-radius:5px;background:#e1e6ee;margin:0 0 12px}
</style></head><body><div class="fake"><i style="width:40%;height:16px"></i><i style="width:92%"></i><i style="width:85%"></i><i style="width:70%"></i><i style="width:88%"></i><i style="width:60%"></i></div>${html}</body></html>`;
}

// ---- install check ----

export type Tone = "ok" | "warn" | "bad" | "";
export interface CheckView { icon: string; tone: Tone; word: string; label: string; detail: string; next: string }

const ICON: Record<CheckStatus, { icon: string; tone: Tone; word: string }> = {
  ok: { icon: "✓", tone: "ok", word: "OK" },
  warn: { icon: "!", tone: "warn", word: "Check" },
  fail: { icon: "✕", tone: "bad", word: "Problem" },
  skip: { icon: "–", tone: "", word: "Not checked" },
};

const NEXT: Record<string, string> = {
  https: "Serve the site over https; browsers only allow notifications there.",
  page: "Check the site address; it must open in a browser for anyone, without signing in.",
  snippet: "Add the website snippet (Settings → Sign-in & keys) to every page, just before </head>.",
  site_key: "Copy the snippet again from Settings → Sign-in & keys; its site key belongs to this workspace.",
  service_worker: "Ask your web team to put the service worker file at this address on the site (the one line is below).",
  sw_imports: "The service worker file must contain the one line below. Replace its contents, or add the line at the top.",
  vapid: "Ask whoever runs Pulse to set the server's secret key (PULSE_SECRET_KEY).",
};

/** checkItemView turns an install check result into what the page shows, with a next step for
 * anything that isn't fine. */
export function checkItemView(it: CheckItem): CheckView {
  const s = ICON[it.status] ?? ICON.skip;
  const next = it.status === "warn" || it.status === "fail" ? NEXT[it.key] ?? "" : "";
  return { ...s, label: it.label, detail: it.detail ?? "", next };
}

/** checkSummary is the headline of a report. */
export function checkSummary(r: Pick<CheckReport, "ok" | "items">): { tone: Tone; text: string } {
  const fails = r.items.filter((i) => i.status === "fail").length;
  const warns = r.items.filter((i) => i.status === "warn").length;
  if (fails) return { tone: "bad", text: `${plural(fails, "problem")} to fix before web push can work on this site.` };
  if (!r.ok) return { tone: "bad", text: "Web push isn't ready on this site yet." };
  if (warns) return { tone: "warn", text: `Ready, with ${plural(warns, "thing")} worth a look.` };
  return { tone: "ok", text: "Everything Pulse can check from outside looks right." };
}

/** needsSnippet says whether the service worker line is worth showing (missing or wrong file). */
export function needsSnippet(r: Pick<CheckReport, "items"> | null | undefined): boolean {
  return !!r?.items.some((i) => (i.key === "service_worker" || i.key === "sw_imports") && (i.status === "fail" || i.status === "warn"));
}

// ---- browser check ----

const PERMISSION: Record<string, string> = {
  granted: "Allowed",
  denied: "Blocked",
  default: "Not asked yet",
  unsupported: "Not available",
};

/** browserFacts lists what the browser test found, in plain words, each ok or not. */
export function browserFacts(d: BrowserReport): { label: string; value: string; tone: Tone }[] {
  const yes = (b: boolean, y: string, n: string, bad: Tone = "bad") => ({ value: b ? y : n, tone: b ? ("ok" as Tone) : bad });
  return [
    { label: "Secure page (https)", ...yes(d.https, "Yes", "No") },
    { label: "This browser supports web push", ...yes(d.push_supported, "Yes", "No") },
    { label: "Site key matches this workspace", ...yes(d.site_key_matches, "Yes", "No") },
    { label: "Notifications for this site", value: PERMISSION[d.permission] ?? d.permission, tone: d.permission === "denied" ? "bad" : d.permission === "granted" ? "ok" : "" },
    { label: `Service worker (${d.service_worker || DEFAULT_SERVICE_WORKER})`, ...yes(d.worker_found && d.worker_pulse, "Found, loads Pulse", d.worker_found ? "Found, but doesn't load Pulse" : "Not found") },
    { label: "Opt-in prompt", ...yes(d.prompt_enabled, "On", "Off", "warn") },
    { label: "This browser is subscribed", ...yes(d.subscribed, "Yes", "Not yet", "") },
    { label: "Visitor is signed in (identified)", ...yes(d.identified, "Yes", "No, anonymous", "") },
  ];
}

/** secondsLeft until an ISO time (0 when past or unreadable). */
export function secondsLeft(iso: string | undefined, now: number): number {
  const t = iso ? new Date(iso).getTime() : NaN;
  return Number.isFinite(t) ? Math.max(0, Math.round((t - now) / 1000)) : 0;
}

/** sameSettings compares settings as they'd be saved. */
export function sameSettings(a: WebPushSettings, b: WebPushSettings): boolean {
  return JSON.stringify(normalise(a)) === JSON.stringify(normalise(b));
}
