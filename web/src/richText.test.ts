// @vitest-environment happy-dom
// The rich text editor's HTML handling: merge tags and template links survive the editor, unsafe
// links don't, HTML it can't keep is detected, and the editor-mode helpers.
import { Editor } from "@tiptap/core";
import { afterEach, describe, expect, it } from "vitest";
import { initialEmailMode, initialPageMode, switchWarning } from "./editorModes";
import { richTextExtensions, roundTrip } from "./richTextExtensions";
import {
  attrTag, EMAIL_STYLE, isSafeUrl, mergeTags, normalizeLink, PAGE_STYLE, richTextLoss, templateTags, unwrapRichText, wrapRichText,
} from "./richTextHtml";

const editors: Editor[] = [];
function editor(content: string) {
  const e = new Editor({ element: document.createElement("div"), extensions: richTextExtensions(), content });
  editors.push(e);
  return e;
}
afterEach(() => { while (editors.length) editors.pop()!.destroy(); });

describe("merge tags", () => {
  it("survive a round trip through the editor unchanged", () => {
    const html = `<p>Hi {{.Attrs.first_name}}, your city is {{.Attrs.city}}. {{if .Attrs.vip}}You're a VIP.{{else}}Join us.{{end}}</p>`
      + `<p><a href="{{.UnsubscribeURL}}">Unsubscribe</a> or <a href="{{.PreferencesURL}}">manage preferences</a></p>`;
    expect(roundTrip(html)).toBe(html);
    expect(editor(html).getHTML()).toBe(html);
  });

  it("keep template links in href and image src", () => {
    const out = roundTrip(`<p><a href="https://shop.example.com/c?id={{.Contact.ExternalID}}">Shop</a></p><img src="https://cdn.example.com/{{.Attrs.tier}}.png" alt="Tier">`);
    expect(out).toContain(`href="https://shop.example.com/c?id={{.Contact.ExternalID}}"`);
    expect(out).toContain(`src="https://cdn.example.com/{{.Attrs.tier}}.png"`);
  });

  it("are inserted as text and kept while editing", () => {
    const e = editor("<p>Hello</p>");
    e.chain().focus("end").insertContent({ type: "text", text: " {{.Attrs.first_name}}" }).run();
    e.chain().focus("end").insertContent({ type: "text", text: "!" }).run();
    expect(e.getHTML()).toBe("<p>Hello {{.Attrs.first_name}}!</p>");
    expect(templateTags(e.getHTML())).toEqual(["{{.Attrs.first_name}}"]);
  });

  it("are listed: common fields first, then the workspace's attributes", () => {
    const tags = mergeTags([{ key: "first_name", label: "First name" }, { key: "plan", label: "Plan" }, { key: "favourite-colour" }]);
    expect(tags[0].tag).toBe("{{.Attrs.first_name}}");
    expect(tags.filter((t) => t.tag === "{{.Attrs.first_name}}")).toHaveLength(1);
    expect(tags.map((t) => t.tag)).toContain("{{.Attrs.plan}}");
    expect(attrTag("favourite-colour")).toBe(`{{index .Attrs "favourite-colour"}}`);
  });
});

describe("links", () => {
  it("reject javascript: and other script URLs, however written", () => {
    for (const bad of ["javascript:alert(1)", " JaVaScRiPt:alert(1)", "java\tscript:alert(1)", "&#106;avascript:alert(1)", "&#x6A;avascript:alert(1)", "vbscript:msgbox(1)", "data:text/html,<script>alert(1)</script>"]) {
      expect(isSafeUrl(bad), bad).toBe(false);
      expect("problem" in normalizeLink(bad), bad).toBe(true);
    }
  });

  it("accept web, mail, phone, anchor and template links", () => {
    expect(normalizeLink("https://example.com/a?b=c")).toEqual({ url: "https://example.com/a?b=c" });
    expect(normalizeLink("example.com/offer")).toEqual({ url: "https://example.com/offer" });
    expect(normalizeLink("mailto:help@example.com")).toEqual({ url: "mailto:help@example.com" });
    expect(normalizeLink("tel:+911234567890")).toEqual({ url: "tel:+911234567890" });
    expect(normalizeLink("#details")).toEqual({ url: "#details" });
    expect(normalizeLink("{{.UnsubscribeURL}}")).toEqual({ url: "{{.UnsubscribeURL}}" });
    expect("problem" in normalizeLink("not a link")).toBe(true);
    expect("problem" in normalizeLink("")).toBe(true);
  });

  it("are dropped by the editor when they would run script", () => {
    const out = roundTrip(`<p><a href="javascript:alert(1)">click</a></p>`);
    expect(out).toBe("<p>click</p>");
    const e = editor("<p>click</p>");
    e.chain().selectAll().setLink({ href: "javascript:alert(1)" }).run();
    expect(e.getHTML()).not.toContain("javascript:");
  });

  it("open in a new tab only when asked", () => {
    const e = editor("<p>shop</p>");
    e.chain().selectAll().setLink({ href: "https://example.com" }).run();
    expect(e.getHTML()).toBe(`<p><a href="https://example.com">shop</a></p>`);
    e.chain().selectAll().setLink({ href: "https://example.com", target: "_blank", rel: "noopener noreferrer" }).run();
    expect(e.getHTML()).toContain(`target="_blank"`);
    expect(e.getHTML()).toContain(`rel="noopener noreferrer"`);
  });
});

describe("richTextLoss", () => {
  it("accepts content the editor keeps", () => {
    for (const ok of [
      "",
      "<p>Hello {{.Attrs.first_name}}</p>",
      "Plain text with {form:abc123}",
      `<h2 style="text-align: center">Title</h2><p><strong>b</strong> <em>i</em> <u>u</u> <s>s</s> <span style="color: #b4232f; background-color: #fff4d6">c</span></p>`,
      `<ul><li>a</li></ul><ol start="3"><li>b</li></ol><blockquote><p>q</p></blockquote><hr>`,
      `<table><tbody><tr><th>Plan</th><th>Price</th></tr><tr><td colspan="2">Free</td></tr></tbody></table>`,
      `<p><a href="{{.UnsubscribeURL}}" target="_blank" rel="noopener noreferrer">Unsubscribe</a></p><img src="https://x.test/a.png" alt="A">`,
    ]) expect(richTextLoss(ok, roundTrip), ok).toBeNull();
  });

  it("accepts its own output (opening a saved rich text field never falls back to source)", () => {
    const e = editor("<p>x</p>");
    e.chain().selectAll().setColor("#2747d6").setTextAlign("center").run();
    e.chain().focus("end").insertTable({ rows: 2, cols: 2, withHeaderRow: true }).run();
    e.chain().focus("end").setImage({ src: "https://x.test/b.png", alt: "B" }).run();
    const html = e.getHTML();
    expect(richTextLoss(html, roundTrip), html).toBeNull();
    expect(roundTrip(html)).toBe(html);
  });

  it("flags layout the editor would drop", () => {
    const cases: [string, RegExp][] = [
      [`<!doctype html><html><head><style>p{color:red}</style></head><body><p>x</p></body></html>`, /document/],
      [`<style>.a{color:red}</style><p>x</p>`, /style/],
      [`<table style="width:100%;background:#f4f6f8"><tr><td style="padding:24px">x</td></tr></table>`, /inline style "background"/],
      [`<table><tr><td bgcolor="#fff" align="center">x</td></tr></table>`, /bgcolor/],
      [`<p class="lead">x</p>`, /class/],
      [`<div data-pulse-form="abc"></div>`, /data-pulse-form/],
      [`<section><p>x</p></section>`, /<section>/],
      [`<p style="margin:0">x</p>`, /margin/],
      [`<!--[if mso]><table><![endif]--><p>x</p>`, /comments/],
      [`<table>{{range .Attrs.items}}<tr><td>{{.}}</td></tr>{{end}}</table>`, /template tags|text directly inside/],
      [`<p><a href="javascript:alert(1)">x</a></p>`, /javascript/],
      [`<p><font color="red">x</font></p>`, /<font>/],
    ];
    for (const [html, why] of cases) expect(richTextLoss(html, roundTrip), html).toMatch(why);
  });

  it("checks the editor's round trip when given one", () => {
    // A round trip that drops text is caught even when the markup looks fine.
    expect(richTextLoss("<p>a {{.X}}</p>", () => "<p>a</p>")).toMatch(/template tags/);
    expect(richTextLoss("<p>a b</p>", () => "<p>a</p>")).toMatch(/drop/);
    expect(richTextLoss("<p>a</p>", () => { throw new Error("bad"); })).toMatch(/couldn't read/);
  });
});

describe("rich text wrapper and editor modes", () => {
  it("wraps and unwraps exactly", () => {
    for (const inner of ["<p>Hi {{.Attrs.first_name}},</p><p></p>", "<div><p>nested</p></div><p>x</p>"]) {
      const w = wrapRichText(inner, EMAIL_STYLE);
      expect(w.startsWith(`<div class="pulse-rt"`)).toBe(true);
      expect(unwrapRichText(w)).toBe(inner);
    }
    expect(wrapRichText("  ", PAGE_STYLE)).toBe("");
    expect(unwrapRichText("<p>not wrapped</p>")).toBeNull();
    // A whole document edited as source stays a document.
    const doc = "<!doctype html><html><body><p>x</p></body></html>";
    expect(wrapRichText(doc, EMAIL_STYLE)).toBe(doc);
  });

  it("opens each email template in the editor it was made with", () => {
    expect(initialEmailMode({ html: "<table></table>", builder: { pages: [] } })).toBe("visual");
    expect(initialEmailMode({ html: wrapRichText("<p>x</p>", EMAIL_STYLE) })).toBe("richtext");
    expect(initialEmailMode({ html: "<p>hand-written</p>" })).toBe("html");
    expect(initialEmailMode({ html: "" })).toBe("visual");
    expect(initialPageMode({ html: "<p>x</p>", builder: {} })).toBe("builder");
    expect(initialPageMode({ html: wrapRichText("<p>x</p>", PAGE_STYLE), builder: null })).toBe("richtext");
    expect(initialPageMode({ html: "<p>x</p>", builder: null })).toBe("html");
  });

  it("warns before leaving the builder for an editor that replaces its layout", () => {
    expect(switchWarning("visual", "richtext", true)).toMatch(/Rich text/);
    expect(switchWarning("visual", "html", true)).toMatch(/HTML/);
    expect(switchWarning("text", "richtext", true)).not.toBeNull(); // the plain-text tab keeps the layout
    expect(switchWarning("builder", "richtext", true)).not.toBeNull();
    expect(switchWarning("visual", "text", true)).toBeNull();
    expect(switchWarning("richtext", "html", true)).toBeNull(); // already warned
    expect(switchWarning("html", "visual", true)).toBeNull();
    expect(switchWarning("visual", "richtext", false)).toBeNull();
  });
});
