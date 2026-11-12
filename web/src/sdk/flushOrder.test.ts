// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from "vitest";

describe("flush", () => {
  afterEach(() => { vi.unstubAllGlobals(); vi.resetModules(); });

  it("sends batches one after another, so identify reaches the server before a track flushed after it", async () => {
    const order: string[] = [];
    const pending: (() => void)[] = [];
    vi.stubGlobal("fetch", vi.fn((url: string, init?: RequestInit) => {
      if (!String(url).includes("/v1/web/events")) return Promise.resolve(new Response("{}"));
      const names = (JSON.parse(String(init?.body)).events as { name: string }[]).map((e) => e.name);
      order.push(names.join(","));
      return new Promise<Response>((done) => pending.push(() => done(new Response(null, { status: 202 }))));
    }));
    const Pulse = await import("./pulse");
    Pulse.init({ siteKey: "k", host: "https://pulse.test", autoPageview: false, onsite: false } as never);
    Pulse.identify("meera", { email: "meera@example.com" });
    Pulse.track("newsletter_signup");
    Pulse.flush();
    await Promise.resolve();
    expect(order.filter((o) => o.includes("identify") || o.includes("newsletter_signup"))).toHaveLength(1);
    while (pending.length) { pending.shift()!(); await new Promise((r) => setTimeout(r, 0)); }
    const sent = order.join("|");
    expect(sent.indexOf("identify")).toBeGreaterThanOrEqual(0);
    expect(sent.indexOf("identify")).toBeLessThan(sent.indexOf("newsletter_signup"));
  });
});
