import { describe, expect, it } from "vitest";
import { monthGrid, sendsByDay, ymdLocal } from "./calendar";

describe("calendar", () => {
  it("lays out a month from Monday", () => {
    const w = monthGrid(2026, 10); // November 2026 starts on a Sunday
    expect(ymdLocal(w[0][0])).toBe("2026-10-26");
    expect(w[0][6].getDate()).toBe(1);
    expect(w.every((r) => r.length === 7)).toBe(true);
    expect(w.flat().some((d) => d.getMonth() === 10 && d.getDate() === 30)).toBe(true);
  });
  it("groups sends by day, in time order", () => {
    const m = sendsByDay([{ kind: "broadcast", id: 1, name: "a", status: "scheduled", sends: [new Date(2026, 10, 5, 18).toISOString(), new Date(2026, 10, 5, 9).toISOString()] }]);
    const day = m.get("2026-11-05")!;
    expect(day).toHaveLength(2);
    expect(day[0].at.getHours()).toBe(9);
  });
});
