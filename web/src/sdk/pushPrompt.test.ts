import { describe, expect, it } from "vitest";
import { checkToken, pageAllowed, problems, promptBlocked, promptHTML, type PushPrompt } from "./pushPrompt";

const base: PushPrompt = {
  enabled: true, style: "bell", position: "bottom-right", title: "Get updates", text: "Offers & news", allow: "Allow", later: "Not now",
  delay_seconds: 5, scroll_percent: 0, page_views: 0, repeat_days: 7,
};
const state = { permission: "default", dismissedAt: null, pageViews: 1, url: "https://shop.example/products?x=1" };
const DAY = 86_400_000;

describe("opt-in prompt rules", () => {
  it("shows only when on, undecided and allowed", () => {
    expect(promptBlocked(base, state, 0)).toBe("");
    expect(promptBlocked({ ...base, enabled: false }, state, 0)).toBe("off");
    expect(promptBlocked(null, state, 0)).toBe("off");
    expect(promptBlocked(base, { ...state, permission: "granted" }, 0)).toBe("decided");
    expect(promptBlocked(base, { ...state, permission: "denied" }, 0)).toBe("decided");
    expect(promptBlocked(base, { ...state, permission: "unsupported" }, 0)).toBe("unsupported");
  });
  it("waits repeat_days after Not now (0: never again)", () => {
    const now = 100 * DAY;
    expect(promptBlocked(base, { ...state, dismissedAt: now - 6 * DAY }, now)).toBe("dismissed");
    expect(promptBlocked(base, { ...state, dismissedAt: now - 8 * DAY }, now)).toBe("");
    expect(promptBlocked({ ...base, repeat_days: 0 }, { ...state, dismissedAt: now - 300 * DAY }, now)).toBe("dismissed");
  });
  it("counts pages seen this visit", () => {
    expect(promptBlocked({ ...base, page_views: 3 }, { ...state, pageViews: 2 }, 0)).toBe("page_views");
    expect(promptBlocked({ ...base, page_views: 3 }, { ...state, pageViews: 3 }, 0)).toBe("");
  });
  it("applies page rules to the path and query only", () => {
    expect(pageAllowed({ show_on: ["/products"] }, state.url)).toBe(true);
    expect(pageAllowed({ show_on: ["/blog"] }, state.url)).toBe(false);
    expect(pageAllowed({ hide_on: ["x=1"] }, state.url)).toBe(false);
    expect(pageAllowed({ show_on: ["shop"] }, "https://shop.example/about")).toBe(false);
    expect(promptBlocked({ ...base, hide_on: ["/products"] }, state, 0)).toBe("page");
  });
});

describe("prompt markup", () => {
  it("escapes the workspace's text and colours", () => {
    const h = promptHTML({ ...base, title: "<img src=x onerror=alert(1)>", text: `"quoted"`, button: "red;}" });
    expect(h).not.toContain("<img");
    expect(h).toContain("&lt;img");
    expect(h).toContain("&quot;quoted&quot;");
    expect(h).not.toContain("red;}");
    expect(h).toContain('class="bell"');
  });
  it("renders each style", () => {
    expect(promptHTML({ ...base, style: "bar", position: "bottom" })).toContain("bottom:0");
    expect(promptHTML({ ...base, style: "modal" })).toContain('class="bd"');
    expect(promptHTML({ ...base, position: "bottom-left" })).toContain("left:16px");
  });
});

describe("browser self-check", () => {
  it("reads only well-formed check tokens", () => {
    expect(checkToken("https://s.example/?pulse_check=pc0123456789abcdef0123456789ab")).toBe("pc0123456789abcdef0123456789ab");
    expect(checkToken("https://s.example/?pulse_check=<script>")).toBeNull();
    expect(checkToken("https://s.example/")).toBeNull();
  });
  it("explains problems in plain words", () => {
    const ok = { sdk_version: "6", url: "", https: true, push_supported: true, permission: "default", service_worker: "/pulse-sw.js",
      worker_found: true, worker_pulse: true, subscribed: false, prompt_enabled: true, identified: false };
    expect(problems(ok)).toEqual([]);
    const bad = problems({ ...ok, https: false, permission: "denied", worker_pulse: false, prompt_enabled: false });
    expect(bad).toHaveLength(4);
    expect(bad[0]).toContain("https");
  });
});
