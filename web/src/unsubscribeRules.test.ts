import { describe, expect, it } from "vitest";
import { topicKeyFrom, unsubscribeProblem } from "./unsubscribeRules";

const base = { link_mode: "confirm" as const, email_policy: "enforce" as const };

describe("unsubscribe settings", () => {
  it("makes topic keys from names", () => {
    expect(topicKeyFrom("Weekly Offers!")).toBe("weekly_offers");
    expect(topicKeyFrom("  Café news ")).toBe("cafe_news");
    expect(topicKeyFrom("!!!")).toBe("");
  });
  it("checks topics", () => {
    expect(unsubscribeProblem({ ...base, topics: [{ key: "offers", name: "Offers" }] })).toBe("");
    expect(unsubscribeProblem({ ...base, topics: [{ key: "", name: "" }] })).toMatch(/name/);
    expect(unsubscribeProblem({ ...base, topics: [{ key: "email", name: "Email" }] })).toMatch(/channel/);
    expect(unsubscribeProblem({ ...base, topics: [{ key: "a", name: "A" }, { key: "a", name: "a" }] })).toMatch(/same key/);
  });
});
