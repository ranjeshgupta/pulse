import { describe, expect, it } from "vitest";
import { knownChannel } from "./pages/Templates";

// UI crawl: /templates?channel=__proto__ crashed the list (CHANNEL_NAMES["__proto__"] is an object).
describe("knownChannel", () => {
  it("keeps a real channel", () => expect(knownChannel("sms")).toBe("sms"));
  it("drops anything else", () => {
    for (const v of [null, "", "xxx", "__proto__", "constructor", "toString", "EMAIL"]) expect(knownChannel(v)).toBe("");
  });
});
