import { describe, expect, it } from "vitest";
import { feedProblem, sinceLabel } from "./FeedFields";

describe("feed fields", () => {
  it("checks the feed address", () => {
    expect(feedProblem("", "email")).toBeNull();
    expect(feedProblem("https://blog.test/feed", "email")).toBeNull();
    expect(feedProblem("blog.test/feed", "email")).toMatch(/https/);
    expect(feedProblem("https://blog.test/feed", "sms")).toMatch(/email/);
  });
  it("says how far back the first send looks", () => {
    expect(sinceLabel("weekly")).toBe("the last week");
    expect(sinceLabel("none")).toMatch(/10 newest/);
  });
});
