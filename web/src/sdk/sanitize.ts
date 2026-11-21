// Defence in depth for on-site content (pop-ups, bars, slide-ins, content slots). The server
// already cleans it with the landing page sanitizer; the SDK cleans it again before inserting it
// into the customer's page, so a compromised or older server can't run script there.
//
// The HTML is parsed into an inert <template> (nothing runs or loads while it is there), then
// scripts and other code-carrying elements, on* attributes and javascript:/vbscript:/data: URLs
// are removed before the result is serialized.

const DROP = new Set(["script", "base", "object", "embed", "applet", "frame", "frameset", "meta", "link",
  "xmp", "noembed", "noframes", "plaintext", "noscript", "set", "animate", "animatetransform", "animatemotion", "discard"]);
const URL_ATTRS = new Set(["href", "src", "action", "formaction", "xlink:href", "poster", "data", "background", "cite", "ping"]);

/** codeURL reports whether a URL would run code, ignoring the whitespace browsers ignore. */
export function codeURL(v: string): boolean {
  // eslint-disable-next-line no-control-regex
  const u = v.replace(/[\u0000- \u007f]+/g, "").toLowerCase();
  return u.startsWith("javascript:") || u.startsWith("vbscript:") ||
    (u.startsWith("data:") && (!u.startsWith("data:image/") || u.startsWith("data:image/svg")));
}

function clean(root: ParentNode) {
  for (const el of Array.from(root.children)) {
    const name = el.localName.toLowerCase();
    if (DROP.has(name)) { el.remove(); continue; }
    for (const a of Array.from(el.attributes)) {
      const k = a.name.toLowerCase();
      if (k.startsWith("on") || k === "srcdoc" || ((k === "values" || k === "to" || k === "from" || k === "by") && a.value.split(";").some(codeURL))) {
        el.removeAttribute(a.name);
      } else if (URL_ATTRS.has(k) && codeURL(a.value)) {
        el.setAttribute(a.name, "#");
      }
    }
    if (el instanceof HTMLTemplateElement) clean(el.content);
    clean(el);
  }
}

/** cleanHTML returns html without scripts, event handlers or code URLs. */
export function cleanHTML(html: string, doc: Document = document): string {
  if (!html) return "";
  const t = doc.createElement("template");
  t.innerHTML = html;
  clean(t.content);
  return t.innerHTML;
}
