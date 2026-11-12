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

describe("audit action words (M7)", () => {
  it("names known actions and turns other requests into a sentence", async () => {
    const { actionLabel } = await import("./Audit");
    expect(actionLabel("POST /v1/broadcasts/{id}/schedule")).toBe("Scheduled a one-time campaign");
    expect(actionLabel("PUT /v1/settings/automation")).toBe("Changed: settings (automation)");
    expect(actionLabel("POST /v1/dwc/overlap")).toBe("Created or ran: web content (overlap)");
    expect(actionLabel("sign-in")).toBe("Signed in");
    expect(actionLabel("weird")).toBe("weird");
  });
});
