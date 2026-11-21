import { describe, expect, it } from "vitest";
import { cardProblem, emptyCard } from "./pages/AppCardEditor";

describe("app cards", () => {
  it("accepts https and app links, refuses unsafe ones", () => {
    expect(cardProblem({ deep_link: "myapp://sale", image_url: "https://x.test/a.png" })).toBe("");
    expect(cardProblem({ deep_link: "javascript://x" })).toMatch(/link/);
    expect(cardProblem({ deep_link: "http://x.test" })).toMatch(/link/);
    expect(cardProblem({ image_url: "http://x.test/a.png" })).toMatch(/image/);
    expect(cardProblem({ data: { "a b": "x" } })).toMatch(/Custom value/);
  });
  it("knows an empty card", () => {
    expect(emptyCard(null)).toBe(true);
    expect(emptyCard({ title: "" })).toBe(true);
    expect(emptyCard({ data: { k: "v" } })).toBe(false);
  });
});
