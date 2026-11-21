// @vitest-environment happy-dom
import { describe, expect, it } from "vitest";
import { cleanHTML, codeURL } from "./sanitize";

describe("cleanHTML", () => {
  it("removes scripts, handlers and code URLs", () => {
    const out = cleanHTML(
      `<p onclick="x()">Hi <b>there</b></p><script>alert(1)</script><img src="x" onerror="alert(1)">` +
      `<a href=" javascript:alert(1)">l</a><iframe srcdoc="<script>1</script>"></iframe><object data="x"></object>` +
      `<svg><a><set attributeName="href" to="javascript:alert(1)"></set><animate values="javascript:alert(1)"></animate></a></svg>`,
    );
    for (const bad of ["<script", "onclick", "onerror", "javascript:", "srcdoc", "<object", "<set", "<animate"]) {
      expect(out.toLowerCase()).not.toContain(bad);
    }
    expect(out).toContain("<p>Hi <b>there</b></p>");
    expect(out).toContain('href="#"');
  });

  it("keeps ordinary content", () => {
    const html = `<h3 style="color:red">Sale</h3><a href="https://acme.test/x?a=1&amp;b=2">Shop</a><img src="data:image/png;base64,AAA" alt="">`;
    expect(cleanHTML(html)).toBe(html);
    expect(cleanHTML("")).toBe("");
  });

  it("recognises code URLs however they are written", () => {
    for (const u of ["javascript:x", "JaVaScRiPt:x", " java\nscript:x", "vbscript:x", "data:text/html,x", "data:image/svg+xml,x"]) {
      expect(codeURL(u)).toBe(true);
    }
    for (const u of ["https://a.test", "/x", "#top", "mailto:a@b.c", "data:image/png;base64,x"]) {
      expect(codeURL(u)).toBe(false);
    }
  });
});
