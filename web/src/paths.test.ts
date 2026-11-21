import { describe, expect, it } from "vitest";
import { layout, STOPPED } from "./paths";

describe("path layout", () => {
  const links = [
    { step: 0, from: "signup", to: "view", people: 8 }, { step: 0, from: "signup", to: "search", people: 2 },
    { step: 1, from: "view", to: "buy", people: 5 }, { step: 1, from: "view", to: "", people: 3 }, { step: 1, from: "search", to: "buy", people: 2 },
  ];
  it("puts the anchor first and sums each column", () => {
    const { nodes, bands } = layout(links, 2, "signup");
    const at = (c: number) => nodes.filter((n) => n.col === c).map((n) => `${n.name}:${n.value}`);
    expect(at(0)).toEqual(["signup:10"]);
    expect(at(1)).toEqual(["view:8", "search:2"]);
    expect(at(2)).toEqual(["buy:7", `${STOPPED}:3`]);
    expect(bands).toHaveLength(5);
    expect(nodes.every((n) => n.y >= 0 && n.y + n.h <= 421)).toBe(true);
  });
  it("folds small events into Other and mirrors backwards paths", () => {
    const many = Array.from({ length: 10 }, (_, i) => ({ step: 0, from: "a", to: `e${i}`, people: 10 - i }));
    const { nodes } = layout(many, 1, "a", 1000, 420, true);
    const col1 = nodes.filter((n) => n.col === 1);
    expect(col1).toHaveLength(8);
    expect(col1.at(-1)?.name).toBe("Other events");
    expect(col1.at(-1)?.value).toBe(3 + 2 + 1);
    expect(nodes.find((n) => n.col === 0)!.x).toBeGreaterThan(col1[0].x);
  });
});
