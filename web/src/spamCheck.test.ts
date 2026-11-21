import { describe, expect, it } from "vitest";
import { meterPct } from "./pages/SpamCheck";

describe("spam meter", () => {
  it("places scores", () => {
    expect(meterPct(0)).toBe(2);
    expect(meterPct(5)).toBe(50);
    expect(meterPct(30)).toBe(100);
  });
});
