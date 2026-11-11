import { describe, expect, it } from "vitest";
import type { Integration, IntegrationKind } from "./api";
import { mappingProblems, normalizeCatalog, normalizeIntegration, normalizeKind, reservedAttr } from "./integrations";

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

describe("integration catalog", () => {
  it("treats null config and secrets as empty lists", () => {
    const raw = {
      kinds: [{ id: "rest_hooks", name: "Zapier / Make", category: "automation", description: "", config: null, secrets: null, pull: false, push: false, webhooks: false, audiences: false, hooks: true }],
      shopify_topics: null,
    } as unknown as Parameters<typeof normalizeCatalog>[0];
    const c = normalizeCatalog(raw);
    expect(c.kinds[0].config).toEqual([]);
    expect(c.kinds[0].secrets).toEqual([]);
    expect(c.kinds[0].config.filter((f) => f.required)).toEqual([]);
    expect(c.shopify_topics).toEqual([]);
    expect(c.hook_events).toEqual([]);
    expect(normalizeCatalog(null).kinds).toEqual([]);
  });
  it("keeps the lists a kind does send", () => {
    const k = normalizeKind({ id: "hubspot", name: "HubSpot", category: "crm", description: "", config: [], secrets: [{ key: "token", label: "Token", required: true }], pull: true, push: true, webhooks: false, audiences: false, hooks: false } as IntegrationKind);
    expect(k.secrets.map((f) => f.key)).toEqual(["token"]);
  });
  it("fills a saved integration's maps", () => {
    const i = normalizeIntegration({ id: 1, kind: "rest_hooks", name: "Zap", config: null, secrets: null, mapping: null } as unknown as Integration);
    expect(i.config).toEqual({});
    expect(i.secrets).toEqual({});
    expect(i.mapping).toEqual([]);
  });
});
