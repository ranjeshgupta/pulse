import { describe, expect, it } from "vitest";
import { stepLines } from "./pages/CampaignTimeline";

describe("campaign timeline", () => {
  it("names a step's outputs", () => {
    const node = { type: "split", variants: [{ label: "Short" }, { label: "" }] } as never;
    expect(stepLines(["entered", "yes", "no"]).map((l) => l.label)).toEqual(["Reached the step", "Yes", "No"]);
    expect(stepLines(["v0", "v1"], node).map((l) => l.label)).toEqual(["Path Short", "Path B"]);
  });
});
