// Pure helpers for the rich text editor (RichText.tsx): link safety, merge tags, the "the visual
// editor can't keep this HTML" check, and the wrapper rich-text emails and pages are saved in.
// They need a DOM parser (the browser's, or happy-dom in tests) but no editor.

// ---------- links ----------

/** Schemes that run code or smuggle documents: never allowed in an authored link or image. */
const UNSAFE_SCHEME = /^(?:javascript|vbscript|data|file):/i;

/** What browsers ignore when they read a URL's scheme: whitespace and control characters. */
// eslint-disable-next-line no-control-regex
const IGNORED_IN_SCHEME = /[\u0000-\u0020\u007f-\u009f\u00a0\u1680\u180e\u2000-\u200b\u2028\u2029\u202f\u205f\u3000\ufeff]/g;

/** Decodes the character references an HTML attribute could hide a scheme behind (&#106;avascript:). */
function decodeRefs(s: string): string {
  return s
    .replace(/&#x([0-9a-f]+);?/gi, (_, h) => String.fromCodePoint(parseInt(h, 16)))
    .replace(/&#(\d+);?/g, (_, d) => String.fromCodePoint(Number(d)))
    .replace(/&(tab|newline|colon);/gi, (_, n: string) => ({ tab: "\t", newline: "\n", colon: ":" })[n.toLowerCase()] ?? "");
}

/** isSafeUrl: false for javascript:, vbscript:, data: and file: URLs, however they're disguised. */
export function isSafeUrl(url: string): boolean {
  return !UNSAFE_SCHEME.test(decodeRefs(url).replace(IGNORED_IN_SCHEME, ""));
}

/** templateTags lists the Go template actions ({{…}}) in order. */
export function templateTags(s: string): string[] {
  return s.match(/\{\{[\s\S]*?\}\}/g) ?? [];
}

/**
 * normalizeLink checks a URL typed into the link dialog. It returns the URL to use, or a problem.
 * Template tags ({{.UnsubscribeURL}}), mailto:, tel:, #anchors and /paths are kept as typed; a bare
 * domain ("example.com/offer") gets https://.
 */
export function normalizeLink(input: string): { url: string } | { problem: string } {
  const url = input.trim();
  if (!url) return { problem: "Enter a link address." };
  if (!isSafeUrl(url)) return { problem: "That kind of link runs code in the reader's browser, so it isn't allowed. Use an https:// address." };
  if (url.startsWith("{{") || /^[a-z][a-z0-9+.-]*:/i.test(url) || /^[#/?]/.test(url)) return { url };
  if (/^[^\s/]+\.[a-z]{2,}(?:[/?#:]|$)/i.test(url)) return { url: `https://${url}` };
  return { problem: "Use a full address such as https://example.com, mailto:, tel:, or a personalization tag." };
}

// ---------- merge tags ----------

export type MergeTag = { label: string; tag: string };

/** Contact fields every template can use, before the workspace's own attributes. */
export const COMMON_TAGS: MergeTag[] = [
  { label: "First name", tag: "{{.Attrs.first_name}}" },
  { label: "Last name", tag: "{{.Attrs.last_name}}" },
  { label: "City", tag: "{{.Attrs.city}}" },
  { label: "Email address", tag: "{{.Contact.Email}}" },
  { label: "External ID", tag: "{{.Contact.ExternalID}}" },
];

/** Email-only links: the unsubscribe link and the preference center. */
export const EMAIL_LINK_TAGS: MergeTag[] = [
  { label: "Unsubscribe link", tag: "{{.UnsubscribeURL}}" },
  { label: "Preference center link", tag: "{{.PreferencesURL}}" },
];

/** attrTag is the merge tag for a contact attribute key. Keys that aren't Go identifiers use index. */
export function attrTag(key: string): string {
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(key) ? `{{.Attrs.${key}}}` : `{{index .Attrs ${JSON.stringify(key)}}}`;
}

/** mergeTags: the common tags, then the workspace's attributes not already listed. */
export function mergeTags(attrs: { key: string; label?: string }[]): MergeTag[] {
  const seen = new Set(COMMON_TAGS.map((t) => t.tag));
  const extra = attrs
    .map((a) => ({ label: a.label || a.key, tag: attrTag(a.key) }))
    .filter((t) => !seen.has(t.tag) && (seen.add(t.tag), true));
  return [...COMMON_TAGS, ...extra];
}

// ---------- what the visual editor keeps ----------

const HEADINGS = ["h1", "h2", "h3", "h4", "h5", "h6"];
const ALIGN = /^(left|center|right|justify)$/;

/** The style properties the editor writes and reads back, per element. Anything else is dropped. */
const STYLES: Record<string, (prop: string, value: string) => boolean> = {
  p: (p, v) => p === "text-align" && ALIGN.test(v),
  span: (p) => p === "color" || p === "background-color",
  img: (p, v) => (p === "max-width" && v === "100%") || (p === "height" && v === "auto"),
  table: (p) => p === "width" || p === "min-width",
  col: (p) => p === "width" || p === "min-width",
  td: (p, v) => (p === "text-align" && ALIGN.test(v)) || p === "width" || p === "min-width",
};
for (const h of HEADINGS) STYLES[h] = STYLES.p;
STYLES.th = STYLES.td;

/** The elements the editor keeps, with the attributes (besides style) it keeps on each. */
const ELEMENTS: Record<string, string[]> = {
  p: [], br: [], hr: [], div: [], strong: [], b: [], em: [], i: [], u: [], s: [], strike: [], del: [], code: [], pre: [],
  span: [], a: ["href", "target", "rel", "title", "class"], ul: [], ol: ["start", "type"], li: [], blockquote: [],
  img: ["src", "alt", "title", "width", "height"],
  table: [], colgroup: [], col: [], thead: [], tbody: [], tr: [], td: ["colspan", "rowspan", "colwidth"], th: ["colspan", "rowspan", "colwidth"],
};
for (const h of HEADINGS) ELEMENTS[h] = [];

/** Containers whose text must sit in a child: a template tag between table rows, say. */
const BARE_TEXT_NOT_ALLOWED = new Set(["table", "thead", "tbody", "tr", "ul", "ol", "colgroup"]);

/** Markup only a whole document or a <style> block has; the editor keeps only body content. */
const DOCUMENT_MARKUP = /<(?:!doctype|html|head|body|style|script|meta|link|title|base)\b/i;

function parseStyle(style: string): [string, string][] {
  return style.split(";").map((d) => d.trim()).filter(Boolean).map((d) => {
    const i = d.indexOf(":");
    return [d.slice(0, i).trim().toLowerCase(), d.slice(i + 1).trim().toLowerCase()] as [string, string];
  });
}

/** visibleText: the text a reader sees, whitespace collapsed. */
function visibleText(body: HTMLElement): string {
  return (body.textContent ?? "").replace(/\s+/g, " ").trim();
}

function parseBody(html: string): HTMLElement {
  return new DOMParser().parseFromString(`<!doctype html><html><body>${html}</body></html>`, "text/html").body;
}

/**
 * richTextLoss says why the visual editor can't hold this HTML without changing it, or null when it
 * can. It looks for markup the editor drops (whole documents, <style>, comments, unknown elements,
 * attributes and inline styles it doesn't keep). Given the editor's own parse-and-serialise
 * (roundTrip), it also checks that the text and the template tags come back unchanged: the HTML
 * parser moves {{range}} out of a <table>, for example.
 */
export function richTextLoss(html: string, roundTrip?: (html: string) => string): string | null {
  if (!html.trim()) return null;
  const doc = DOCUMENT_MARKUP.exec(html);
  if (doc) return `a full HTML document or <${doc[0].slice(1).toLowerCase()}> element`;
  if (/<!--/.test(html)) return "HTML comments (often Outlook-only markup)";
  const body = parseBody(html);
  const walk = (el: Element): string | null => {
    const tag = el.tagName.toLowerCase();
    const allowed = ELEMENTS[tag];
    if (!allowed) return `<${tag}> elements`;
    for (const a of Array.from(el.attributes)) {
      const name = a.name.toLowerCase();
      if (name === "style") {
        const ok = STYLES[tag];
        for (const [p, v] of parseStyle(a.value)) if (!ok || !ok(p, v)) return `the inline style "${p}" on <${tag}>`;
      } else if (!allowed.includes(name)) {
        return `the ${name}="…" attribute on <${tag}>`;
      }
    }
    if ((tag === "a" || tag === "img") && !isSafeUrl(el.getAttribute(tag === "a" ? "href" : "src") ?? "")) return "a javascript: link";
    if (BARE_TEXT_NOT_ALLOWED.has(tag) && Array.from(el.childNodes).some((n) => n.nodeType === 3 && n.textContent!.trim()))
      return `text directly inside <${tag}> (outside a cell or list item)`;
    for (const c of Array.from(el.children)) {
      const r = walk(c);
      if (r) return r;
    }
    return null;
  };
  for (const c of Array.from(body.children)) {
    const r = walk(c);
    if (r) return r;
  }
  if (roundTrip) {
    let out: string;
    try {
      out = roundTrip(html);
    } catch {
      return "markup the editor couldn't read";
    }
    if (templateTags(out).join("\n") !== templateTags(html).join("\n")) return "template tags in places the editor would move them";
    if (visibleText(parseBody(out)) !== visibleText(body)) return "content the editor would drop";
  }
  return null;
}

// ---------- the saved wrapper ----------

/**
 * Rich-text emails and landing pages are saved inside one styled <div class="pulse-rt">, so they
 * read well without a stylesheet. unwrapRichText recognises it when the content is opened again.
 */
export const EMAIL_STYLE = "font-family:Arial,Helvetica,sans-serif;font-size:15px;line-height:1.6;color:#152238;max-width:600px;margin:0 auto;padding:24px";
export const PAGE_STYLE = "font-family:system-ui,-apple-system,Segoe UI,Roboto,Arial,sans-serif;font-size:17px;line-height:1.65;color:#152238;max-width:720px;margin:0 auto;padding:48px 24px";

const WRAPPED = /^<div class="pulse-rt" style="[^"<>]*">([\s\S]*)<\/div>$/;

export function wrapRichText(inner: string, style: string): string {
  if (!inner.trim()) return "";
  if (DOCUMENT_MARKUP.test(inner)) return inner; // a whole document edited as source: keep it as it is
  return `<div class="pulse-rt" style="${style}">${inner}</div>`;
}

/** unwrapRichText returns the content inside the rich-text wrapper, or null when there's none. */
export function unwrapRichText(html: string): string | null {
  const m = WRAPPED.exec(html.trim());
  return m ? m[1] : null;
}
