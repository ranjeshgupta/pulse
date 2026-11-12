import { describe as suite, expect, it } from "vitest";
import {
  browserFacts, checkItemView, checkSummary, frequencySummary, needsSnippet, normalise, normaliseList, pagesSummary,
  positionsFor, previewDoc, sameSettings, secondsLeft, siteWarning, swSnippet, triggerSummary, validate, withStyle,
  type BrowserReport, type WebPushSettings,
} from "./webPushSettings";

const base = (): WebPushSettings => ({
  prompt: {
    enabled: true, style: "bell", position: "bottom-right", title: "Get updates", text: "News and offers.", allow: "Allow", later: "Not now",
    background: "#ffffff", text_color: "#152238", button: "#2747d6", delay_seconds: 5, scroll_percent: 0, page_views: 0, repeat_days: 7,
    show_on: [], hide_on: [],
  },
  site_url: "https://www.example.com/",
  service_worker: "/pulse-sw.js",
});
const withPrompt = (p: Partial<WebPushSettings["prompt"]>, s: Partial<WebPushSettings> = {}): WebPushSettings => {
  const b = base();
  return { ...b, ...s, prompt: { ...b.prompt, ...p } };
};

suite("prompt config", () => {
  it("accepts good settings", () => {
    expect(validate(base())).toEqual([]);
    expect(validate(withPrompt({ enabled: false, text: "", allow: "" }))).toEqual([]);
    expect(validate(withPrompt({ style: "modal", position: "" }))).toEqual([]);
    expect(validate(withPrompt({}, { site_url: "", service_worker: "" }))).toEqual([]);
  });

  it("mirrors the server's rules", () => {
    expect(validate(withPrompt({ text: "  " }))).toContain("Write the message and the Allow button's label.");
    expect(validate(withPrompt({ allow: "" }))).toContain("Write the message and the Allow button's label.");
    expect(validate(withPrompt({ style: "bar", position: "bottom-left" }))).toContain("The bar sits at the top or the bottom.");
    expect(validate(withPrompt({ position: "top" }))).toContain("The bell sits bottom right or bottom left.");
    expect(validate(withPrompt({ style: "toast" as never }))).toContain("Choose a bell, a bar or a pop-up.");
    expect(validate(withPrompt({ title: "x".repeat(61) }))).toContain("The title is longer than 60 characters.");
    expect(validate(withPrompt({ title: "é".repeat(31) }))).toContain("The title is longer than 60 characters."); // bytes, like the server
    expect(validate(withPrompt({ text: "x".repeat(301) }))).toContain("The message is longer than 300 characters.");
    expect(validate(withPrompt({ button: "blue" }))).toContain("Colours look like #1a2b3c.");
    expect(validate(withPrompt({ delay_seconds: 601 }))).toContain("The delay is 0 to 600 seconds.");
    expect(validate(withPrompt({ scroll_percent: -1 }))).toContain("Scrolling is 0 to 100%.");
    expect(validate(withPrompt({ page_views: 51 }))).toContain("Pages seen is 0 to 50.");
    expect(validate(withPrompt({ repeat_days: 366 }))).toContain("Ask again after 0 to 365 days.");
    expect(validate(withPrompt({ show_on: Array.from({ length: 21 }, (_, i) => `/p${i}`) }))).toContain("At most 20 page rules of each kind.");
    expect(validate(withPrompt({ hide_on: [" "] }))).toContain("Page rules are 1 to 300 characters.");
    expect(validate(withPrompt({}, { site_url: "www.example.com" }))).toContain("The site address must look like https://www.example.com/");
    expect(validate(withPrompt({}, { site_url: "https://a:b@example.com/" }))).toContain("The site address must look like https://www.example.com/");
    expect(validate(withPrompt({}, { service_worker: "pulse-sw.js" }))).toContain("The service worker path must look like /pulse-sw.js");
    expect(validate(withPrompt({}, { service_worker: "/../sw.js" }))).toContain("The service worker path must look like /pulse-sw.js");
  });

  it("warns about http sites", () => {
    expect(siteWarning("http://example.com/")).toMatch(/https/);
    expect(siteWarning("https://example.com/")).toBe("");
    expect(siteWarning("")).toBe("");
  });

  it("normalises page rules and paths", () => {
    expect(normaliseList([" /shop ", "", "/shop", "/blog"])).toEqual(["/shop", "/blog"]);
    expect(normaliseList(undefined)).toEqual([]);
    expect(normaliseList(Array.from({ length: 30 }, (_, i) => `/p${i}`))).toHaveLength(20);
    const n = normalise(withPrompt({ show_on: [" /a", ""] }, { site_url: " https://x.com/ ", service_worker: " " }));
    expect(n.prompt.show_on).toEqual(["/a"]);
    expect(n.site_url).toBe("https://x.com/");
    expect(n.service_worker).toBe("/pulse-sw.js");
    expect(sameSettings(withPrompt({ show_on: ["/a "] }), withPrompt({ show_on: ["/a"] }))).toBe(true);
    expect(sameSettings(base(), withPrompt({ delay_seconds: 6 }))).toBe(false);
  });

  it("keeps positions valid when the style changes", () => {
    expect(positionsFor("modal")).toEqual([]);
    expect(withStyle(base().prompt, "bar").position).toBe("top");
    expect(withStyle({ ...base().prompt, position: "bottom-left" }, "bell").position).toBe("bottom-left");
    expect(withStyle(base().prompt, "modal").position).toBe("");
  });

  it("summarises the triggers", () => {
    expect(triggerSummary({ delay_seconds: 5, scroll_percent: 50, page_views: 2 })).toBe("After 5 seconds, once they've scrolled 50% and seen 2 pages");
    expect(triggerSummary({ delay_seconds: 1, scroll_percent: 0, page_views: 0 })).toBe("After 1 second");
    expect(triggerSummary({ delay_seconds: 0, scroll_percent: 0, page_views: 3 })).toBe("Straight away, once they've seen 3 pages");
    expect(triggerSummary({ delay_seconds: 0, scroll_percent: 0, page_views: 1 })).toBe("Straight away");
    expect(frequencySummary(0)).toMatch(/isn't shown again/);
    expect(frequencySummary(1)).toMatch(/after 1 day\./);
    expect(pagesSummary({ show_on: [], hide_on: [] })).toBe("On every page.");
    expect(pagesSummary({ show_on: ["/shop", "/sale"], hide_on: ["/checkout"] }))
      .toBe("On pages whose address contains “/shop” or “/sale”, except where it contains “/checkout”.");
  });

  it("builds the service worker line and the preview", () => {
    expect(swSnippet("https://pulse.example.com/")).toBe('importScripts("https://pulse.example.com/sdk/pulse-sw.js");');
    const doc = previewDoc(withPrompt({ title: "<b>Hi</b>" }).prompt);
    expect(doc).toContain('<div class="pop">');
    expect(doc).not.toContain("hidden>");
    expect(doc).toContain("&lt;b&gt;Hi&lt;/b&gt;");
  });

  it("maps install check results", () => {
    const fail = checkItemView({ key: "service_worker", label: "The service worker file is there", status: "fail", detail: "HTTP 404" });
    expect(fail).toMatchObject({ tone: "bad", word: "Problem", detail: "HTTP 404" });
    expect(fail.next).toMatch(/service worker file/);
    expect(checkItemView({ key: "https", label: "https", status: "ok" })).toMatchObject({ tone: "ok", next: "", detail: "" });
    expect(checkItemView({ key: "snippet", label: "s", status: "skip" }).tone).toBe("");
    expect(checkItemView({ key: "unknown", label: "u", status: "warn" }).next).toBe("");
    const items = [{ key: "https", label: "", status: "ok" as const }, { key: "sw_imports", label: "", status: "fail" as const }];
    expect(checkSummary({ ok: false, items }).text).toMatch(/1 problem to fix/);
    expect(checkSummary({ ok: true, items: [{ key: "x", label: "", status: "warn" }] }).tone).toBe("warn");
    expect(checkSummary({ ok: true, items: [{ key: "x", label: "", status: "ok" }] }).tone).toBe("ok");
    expect(needsSnippet({ items })).toBe(true);
    expect(needsSnippet({ items: items.slice(0, 1) })).toBe(false);
    expect(needsSnippet(null)).toBe(false);
  });

  it("describes the browser check", () => {
    const d: BrowserReport = {
      sdk_version: "1", url: "https://x.com/", https: true, push_supported: true, permission: "denied", service_worker: "/pulse-sw.js",
      worker_found: true, worker_pulse: false, subscribed: false, prompt_enabled: true, identified: false, problems: [], reported_at: "", site_key_matches: true,
    };
    const facts = browserFacts(d);
    expect(facts.find((f) => f.label.startsWith("Notifications"))).toMatchObject({ value: "Blocked", tone: "bad" });
    expect(facts.find((f) => f.label.startsWith("Service worker"))).toMatchObject({ value: "Found, but doesn't load Pulse", tone: "bad" });
    expect(facts.find((f) => f.label.startsWith("This browser is subscribed"))?.tone).toBe("");
    expect(secondsLeft("2026-01-01T00:01:00Z", Date.parse("2026-01-01T00:00:00Z"))).toBe(60);
    expect(secondsLeft("2026-01-01T00:00:00Z", Date.parse("2026-01-01T00:01:00Z"))).toBe(0);
    expect(secondsLeft(undefined, 0)).toBe(0);
  });
});
