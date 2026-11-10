import { describe, expect, it } from "vitest";
import { assetLink, conversion, insertAt, lastDays, pageState, pageURL, slugify, slugProblem, SLUG_RE } from "./landingState";

describe("landing page helpers", () => {
  it("slugifies titles into valid slugs", () => {
    expect(slugify("Diwali Sale 2026!")).toBe("diwali-sale-2026");
    expect(slugify("  Café & Crème  ")).toBe("cafe-and-creme");
    expect(slugify("---")).toBe("");
    const long = slugify("a ".repeat(60) + "end");
    expect(long.length).toBeLessThanOrEqual(80);
    expect(SLUG_RE.test(long)).toBe(true);
  });

  it("checks slugs like the server", () => {
    expect(slugProblem("spring-offer")).toBeNull();
    expect(slugProblem("a")).toBeNull();
    expect(slugProblem("")).not.toBeNull();
    expect(slugProblem("-x")).not.toBeNull();
    expect(slugProblem("x-")).not.toBeNull();
    expect(slugProblem("Upper")).not.toBeNull();
    expect(slugProblem("x".repeat(81))).not.toBeNull();
  });

  it("derives the state from status, schedule and the live flag", () => {
    const now = Date.parse("2026-11-11T12:00:00Z");
    const past = "2026-11-01T00:00:00Z", future = "2026-12-01T00:00:00Z";
    expect(pageState({ status: "draft", start_at: past }, false, now)).toBe("draft");
    expect(pageState({ status: "published", start_at: past }, true, now)).toBe("live");
    expect(pageState({ status: "published", start_at: future }, false, now)).toBe("scheduled");
    expect(pageState({ status: "published", start_at: past, end_at: "2026-11-10T00:00:00Z" }, false, now)).toBe("ended");
    expect(pageState({ status: "published", start_at: past, end_at: future }, undefined, now)).toBe("live");
  });

  it("builds URLs and snippets", () => {
    expect(pageURL("http://h/p/acme/", "offer")).toBe("http://h/p/acme/offer");
    expect(pageURL("http://h/p/acme", "offer")).toBe("http://h/p/acme/offer");
    expect(assetLink("http://h/", { public_key: "k1", title: 'Guide "A" & B' })).toBe('<a href="http://h/a/k1">Download Guide &quot;A&quot; &amp; B</a>');
  });

  it("inserts at the cursor", () => {
    expect(insertAt("<p>ab</p>", 4, 4, "X")).toEqual({ text: "<p>aXb</p>", caret: 5 });
    expect(insertAt("abc", 1, 2, "{form:k}")).toEqual({ text: "a{form:k}c", caret: 9 });
    expect(insertAt("abc", 99, 99, "!")).toEqual({ text: "abc!", caret: 4 });
  });

  it("computes conversion and day lists", () => {
    expect(conversion(0, 0)).toBe("–");
    expect(conversion(1, 4)).toBe("25%");
    expect(conversion(1, 30)).toBe("3.3%");
    const days = lastDays(30, Date.parse("2026-11-11T05:00:00Z"));
    expect(days).toHaveLength(30);
    expect(days[29]).toBe("2026-11-11");
    expect(days[0]).toBe("2026-10-13");
  });
});
