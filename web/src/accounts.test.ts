import { describe as suite, expect, it } from "vitest";
import { attrsFromRows, companyProblems, contactCompany, describeThreshold, isAccountAttr, mergePlan, moveItem, rowsFromAttrs } from "./accounts";

suite("company attributes editor", () => {
  it("keeps numbers and true/false typed and drops empty rows", () => {
    expect(attrsFromRows([{ key: "size", value: "120" }, { key: "public", value: "true" }, { key: "industry", value: "Retail" }, { key: " ", value: "x" }, { key: "zip", value: "0411x" }]))
      .toEqual({ size: 120, public: true, industry: "Retail", zip: "0411x" });
  });

  it("round-trips through rows, sorted by name", () => {
    const attrs = { size: 120, industry: "Retail", public: false };
    expect(rowsFromAttrs(attrs).map((r) => r.key)).toEqual(["industry", "public", "size"]);
    expect(attrsFromRows(rowsFromAttrs(attrs))).toEqual(attrs);
  });

  it("reports what the API refuses", () => {
    expect(companyProblems("Acme", "acme.com", [{ key: "industry", value: "Retail" }])).toEqual([]);
    expect(companyProblems("", "", [])).toEqual(["Enter the company's name."]);
    expect(companyProblems("Acme", "acme .com", [])).toEqual(["Enter a domain like example.com."]);
    const p = companyProblems("Acme", "", [{ key: "id", value: "1" }, { key: "bad key", value: "1" }, { key: "x", value: "1" }, { key: "x", value: "2" }, { key: "k".repeat(57), value: "1" }]);
    expect(p).toHaveLength(4);
    expect(p[0]).toContain("taken by the company");
    expect(p[2]).toContain("used twice");
  });
});

suite("contacts and merges", () => {
  it("reads the linked company from a profile", () => {
    expect(contactCompany({ attrs: { company_id: 7, company_name: "Acme" } })).toEqual({ id: 7, name: "Acme" });
    expect(contactCompany({ attrs: {} })).toBeNull();
    expect(contactCompany({ attrs: { company_id: "x" } })).toBeNull();
  });

  it("knows which attributes have their own drawer sections", () => {
    expect(["stage", "stage_changed_at", "company_id", "company_industry"].every(isAccountAttr)).toBe(true);
    expect(isAccountAttr("company")).toBe(false);
    expect(isAccountAttr("city")).toBe(false);
  });

  it("plans one merge per other contact of a group", () => {
    expect(mergePlan([{ external_id: "a" }, { external_id: "b" }, { external_id: "c" }], "b")).toEqual([
      { winner: "b", loser: "a" }, { winner: "b", loser: "c" },
    ]);
  });

  it("moves stages up and down, not past the ends", () => {
    expect(moveItem([1, 2, 3], 1, -1)).toEqual([2, 1, 3]);
    expect(moveItem([1, 2, 3], 2, 1)).toEqual([1, 2, 3]);
    expect(moveItem([1, 2, 3], 0, -1)).toEqual([1, 2, 3]);
  });

  it("words stage thresholds", () => {
    expect(describeThreshold(null)).toContain("by hand");
    expect(describeThreshold(1500)).toBe("at 1,500 points or more");
  });
});
