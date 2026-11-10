// UTM attribution for the web SDK: the first and the latest campaign a visitor arrived from.
// A touch is a landing URL with utm_* parameters (plus the referring site's domain). The first
// touch is kept forever, the last touch is replaced by every new one, and the current session's
// touch rides on its page views, so revenue can be credited to the campaign that brought the visit.

export const UTM_KEYS = ["source", "medium", "campaign", "term", "content"] as const;
export type UtmKey = (typeof UTM_KEYS)[number];
/** A touch: utm values by key (source, medium, ...), and referrer, the referring site's host name. */
export type Touch = Partial<Record<UtmKey | "referrer", string>>;

const MAX_VALUE = 200;

/** touchFromUrl reads a touch from a landing URL, or null when it carries no utm_* parameters. */
export function touchFromUrl(href: string, referrer = ""): Touch | null {
  let u: URL;
  try { u = new URL(href); } catch { return null; }
  const t: Touch = {};
  for (const k of UTM_KEYS) {
    const v = u.searchParams.get(`utm_${k}`)?.trim();
    if (v) t[k] = v.slice(0, MAX_VALUE);
  }
  if (Object.keys(t).length === 0) return null;
  const host = referrerDomain(referrer, u.hostname);
  if (host) t.referrer = host;
  return t;
}

/** referrerDomain is the referring site's host name, or "" for none or the same site. */
export function referrerDomain(referrer: string, ownHost: string): string {
  try {
    const r = new URL(referrer);
    return r.hostname && r.hostname !== ownHost ? r.hostname.slice(0, MAX_VALUE) : "";
  } catch { return ""; }
}

/** profileAttrs turns the first and last touch into profile attributes: first_utm_source, ...,
 * first_referrer, last_utm_source, ..., last_referrer (Pulse keeps first_* once set). */
export function profileAttrs(first: Touch | null, last: Touch | null): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [prefix, t] of [["first", first], ["last", last]] as const) {
    if (!t) continue;
    for (const k of UTM_KEYS) if (t[k]) out[`${prefix}_utm_${k}`] = t[k]!;
    if (t.referrer) out[`${prefix}_referrer`] = t.referrer;
  }
  return out;
}

/** pageProps are a session touch as page_view properties: utm_source, ..., referrer_domain. */
export function pageProps(t: Touch | null): Record<string, string> {
  const out: Record<string, string> = {};
  if (!t) return out;
  for (const k of UTM_KEYS) if (t[k]) out[`utm_${k}`] = t[k]!;
  if (t.referrer) out.referrer_domain = t.referrer;
  return out;
}

/** parseTouch reads a stored touch, ignoring anything malformed. */
export function parseTouch(raw: string | null): Touch | null {
  if (!raw) return null;
  try {
    const v = JSON.parse(raw) as unknown;
    if (!v || typeof v !== "object") return null;
    const t: Touch = {};
    for (const k of [...UTM_KEYS, "referrer"] as const) {
      const s = (v as Record<string, unknown>)[k];
      if (typeof s === "string" && s) t[k] = s.slice(0, MAX_VALUE);
    }
    return Object.keys(t).length ? t : null;
  } catch { return null; }
}
