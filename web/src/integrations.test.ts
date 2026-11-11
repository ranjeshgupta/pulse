import { describe, expect, it } from "vitest";
import { mappingProblems, reservedAttr } from "./integrations";

describe("integration field mapping", () => {
  it("knows Pulse's own attributes", () => {
    for (const a of ["tags", "points", "stage", "optout_all", "company_name", "device_type", "date_added"]) expect(reservedAttr(a)).toBe(true);
    for (const a of ["email", "first_name", "city", "company"]) expect(reservedAttr(a)).toBe(false);
  });
  it("refuses pulling into reserved attributes, but allows pushing them", () => {
    expect(mappingProblems([{ remote: "score", attr: "points" }])[0]).toContain("kept by Pulse");
    expect(mappingProblems([{ remote: "score", attr: "points", dir: "pull" }])[0]).toContain("Out to CRM");
    expect(mappingProblems([{ remote: "lifecyclestage", attr: "stage", dir: "push" }])).toEqual([""]);
  });
  it("flags empty rows, bad names and an attribute filled twice", () => {
    const p = mappingProblems([{ remote: "", attr: "x" }, { remote: "a", attr: "first name" }, { remote: "f", attr: "city" }, { remote: "g", attr: "city" }]);
    expect(p[0]).toContain("Fill in");
    expect(p[1]).toContain("valid attribute");
    expect(p[2]).toBe("");
    expect(p[3]).toContain("row 3");
  });
});
