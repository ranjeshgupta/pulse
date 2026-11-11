import { describe, expect, it } from "vitest";
import { auditExportParams } from "./Audit";

describe("auditExportParams", () => {
  it("carries the page's filters into the export", () => {
    expect(auditExportParams({ userId: "7", objectType: "segments", from: "2026-11-01", to: "2026-11-02", format: "json" }))
      .toBe("format=json&user_id=7&object_type=segments&from=2026-11-01&to=2026-11-02");
  });
  it("leaves out empty filters", () => {
    expect(auditExportParams({ userId: "", objectType: "", from: "", to: "", format: "csv" })).toBe("format=csv");
  });
});
