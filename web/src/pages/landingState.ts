// Pure helpers for the landing pages screen (no DOM, no CSS): slugs, the page's state from its
// status and schedule, text insertion and the snippets authors insert.

export const SLUG_MAX = 80;
export const TITLE_MAX = 200;
export const META_MAX = 320;

/** Same rule as the server: 1-80 lowercase letters, digits and dashes, no dash at either end. */
export const SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,78}[a-z0-9])?$/;

/** slugify turns a title into a slug the server accepts ("" when nothing usable is left). */
export function slugify(title: string): string {
  return title
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/&/g, " and ")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, SLUG_MAX)
    .replace(/-+$/g, "");
}

export function slugProblem(slug: string): string | null {
  if (!slug) return "Choose the page's address.";
  if (slug.length > SLUG_MAX) return `The address can be up to ${SLUG_MAX} characters.`;
  if (!SLUG_RE.test(slug)) return "Use lowercase letters, digits and dashes only, not starting or ending with a dash.";
  return null;
}

export type PageState = "live" | "scheduled" | "ended" | "draft";

export const STATE_LABEL: Record<PageState, string> = { live: "Live", scheduled: "Scheduled", ended: "Ended", draft: "Draft" };
export const STATE_TONE: Record<PageState, string> = { live: "ok", scheduled: "info", ended: "", draft: "" };

/** pageState: what visitors get. `live` (from the API) wins when given; otherwise it follows the schedule. */
export function pageState(
  p: { status: "draft" | "published"; start_at?: string; end_at?: string | null },
  live?: boolean,
  now = Date.now(),
): PageState {
  if (p.status !== "published") return "draft";
  if (live) return "live";
  if (p.end_at && new Date(p.end_at).getTime() <= now) return "ended";
  if (p.start_at && new Date(p.start_at).getTime() > now) return "scheduled";
  return live === false ? "scheduled" : "live"; // not live inside its window: the server clock is just behind
}

/** pageURL joins the workspace's page base (ending in "/") and a slug. */
export function pageURL(base: string, slug: string): string {
  if (!base) return slug;
  return (base.endsWith("/") ? base : base + "/") + slug;
}

/** insertAt replaces text[start, end) with snippet and returns the new text and caret position. */
export function insertAt(text: string, start: number, end: number, snippet: string): { text: string; caret: number } {
  const s = Math.max(0, Math.min(start, text.length));
  const e = Math.max(s, Math.min(end, text.length));
  return { text: text.slice(0, s) + snippet + text.slice(e), caret: s + snippet.length };
}

export const formToken = (key: string) => `{form:${key}}`;

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

export function assetURL(base: string, publicKey: string): string {
  return `${base.replace(/\/+$/, "")}/a/${publicKey}`;
}

export function assetLink(base: string, a: { public_key: string; title: string; filename?: string }): string {
  return `<a href="${esc(assetURL(base, a.public_key))}">Download ${esc(a.title || a.filename || "file")}</a>`;
}

/** conversion is submissions per unique visitor, as a percentage string ("–" without visitors). */
export function conversion(submissions: number, visitors: number): string {
  if (!visitors) return "–";
  const pct = (submissions / visitors) * 100;
  return `${pct >= 10 || pct === 0 ? Math.round(pct) : pct.toFixed(1)}%`;
}

/** lastDays lists the last n UTC days (YYYY-MM-DD), oldest first, ending today. */
export function lastDays(n: number, now = Date.now()): string[] {
  const out: string[] = [];
  for (let i = n - 1; i >= 0; i--) out.push(new Date(now - i * 86_400_000).toISOString().slice(0, 10));
  return out;
}
