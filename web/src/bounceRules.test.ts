import { describe, expect, it } from "vitest";
import { ruleProblem, ruleSentence } from "./bounceRules";

describe("bounce rules in words", () => {
  it("reads like a sentence", () => {
    expect(ruleSentence("email", "soft", { count: 3, action: "stop" })).toBe("After 3 soft bounces, Email to that address stops.");
    expect(ruleSentence("sms", "hard", { count: 1, action: "stop" })).toBe("After the first hard bounce, SMS to that number stops.");
    expect(ruleSentence("whatsapp", "complaint", { count: 2, action: "do_not_contact" })).toBe("After 2 spam complaints, the contact is unsubscribed from every channel.");
  });
  it("checks counts and actions", () => {
    const ok = { hard: { count: 1, action: "stop" as const }, soft: { count: 3, action: "record" as const }, complaint: { count: 1, action: "unsubscribe" as const } };
    expect(ruleProblem(ok)).toBe("");
    expect(ruleProblem({ ...ok, soft: { count: 0, action: "record" } })).toMatch(/Soft bounce/);
    expect(ruleProblem({ ...ok, hard: { count: 2.5, action: "stop" } })).toMatch(/whole number/);
  });
});
