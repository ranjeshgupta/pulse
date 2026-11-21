import { describe, expect, it } from "vitest";
import { chosenNote, journeyState, pickerOption, sameWindow, segmentState, segmentStateText, usageLines, usageHref, windowProblem } from "./publishing";

const now = Date.parse("2026-11-22T10:00:00Z");
const hour = 3600_000;
const iso = (ms: number) => new Date(ms).toISOString();

describe("segmentState", () => {
  it("trusts the server's state", () => {
    expect(segmentState({ state: "ended", published: true }, now)).toBe("ended");
  });
  it("works it out from the fields", () => {
    expect(segmentState({ published: true, start_at: iso(now - hour) }, now)).toBe("live");
    expect(segmentState({ published: false, start_at: iso(now - hour) }, now)).toBe("unpublished");
    expect(segmentState({ published: true, start_at: iso(now + hour) }, now)).toBe("upcoming");
    expect(segmentState({ published: true, start_at: iso(now - 2 * hour), end_at: iso(now - hour) }, now)).toBe("ended");
    expect(segmentState({ published: true, start_at: iso(now - 2 * hour), end_at: iso(now) }, now)).toBe("ended"); // the end itself is over
    expect(segmentState({}, now)).toBe("live"); // an older server: everything is live
  });
  it("says it in words", () => {
    expect(segmentStateText({ published: false }, now)).toBe("Unpublished");
    expect(segmentStateText({ published: true, start_at: iso(now - hour) }, now)).toBe("Live");
    expect(segmentStateText({ published: true, start_at: iso(now + hour) }, now)).toMatch(/^Starts /);
  });
});

describe("pickerOption", () => {
  const seg = (p: object) => ({ id: 1, name: "Buyers", start_at: iso(now - hour), published: true, ...p });
  it("offers live and upcoming segments, not unpublished or ended ones", () => {
    expect(pickerOption(seg({}), now)).toEqual({ label: "Buyers", disabled: false });
    expect(pickerOption(seg({ start_at: iso(now + hour) }), now).disabled).toBe(false);
    expect(pickerOption(seg({ start_at: iso(now + hour) }), now).label).toMatch(/^Buyers \(starts /);
    expect(pickerOption(seg({ published: false }), now)).toMatchObject({ label: "Buyers (unpublished)", disabled: true });
    expect(pickerOption(seg({ end_at: iso(now - 1) }), now)).toMatchObject({ label: "Buyers (ended)", disabled: true });
  });
  it("flags a chosen segment that isn't live", () => {
    const segs = [seg({}), { ...seg({ published: false }), id: 2 }];
    expect(chosenNote(segs, 1, now)).toBeUndefined();
    expect(chosenNote(segs, 2, now)).toBe("unpublished");
    expect(chosenNote(segs, 3, now)).toBeUndefined();
  });
});

describe("journeyState", () => {
  it("adds the window to a live journey's status", () => {
    expect(journeyState({ status: "draft" }, now)).toBe("draft");
    expect(journeyState({ status: "paused", end_at: iso(now - hour) }, now)).toBe("paused");
    expect(journeyState({ status: "active", start_at: iso(now - hour) }, now)).toBe("active");
    expect(journeyState({ status: "active", start_at: iso(now + hour) }, now)).toBe("scheduled");
    expect(journeyState({ status: "active", start_at: iso(now - 2 * hour), end_at: iso(now - hour) }, now)).toBe("ended");
  });
});

describe("windows and uses", () => {
  it("checks a window", () => {
    expect(windowProblem(undefined, null)).toBe("Choose when it starts.");
    expect(windowProblem(iso(now), iso(now))).toBe("The end must be after the start.");
    expect(windowProblem(iso(now), null)).toBeNull();
  });
  it("compares windows by time, not by spelling", () => {
    expect(sameWindow({ start_at: "2026-11-22T10:00:00Z", end_at: null }, { start_at: "2026-11-22T10:00:00.000Z" })).toBe(true);
    expect(sameWindow({ start_at: iso(now) }, { start_at: iso(now), end_at: iso(now + hour) })).toBe(false);
  });
  it("lists and links what uses a segment", () => {
    const uses = [{ kind: "journey" as const, id: 4, name: "Welcome" }, { kind: "inapp" as const, id: 2, name: "Sale banner" }];
    expect(usageLines(uses)).toEqual(["Campaign (journey): Welcome", "In-app message: Sale banner"]);
    expect(usageHref(uses[0])).toBe("/journeys/4");
    expect(usageHref(uses[1])).toBe("/on-site/in-app/2");
  });
});
