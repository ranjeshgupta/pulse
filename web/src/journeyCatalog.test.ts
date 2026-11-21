import { describe, expect, it } from "vitest";
import type { JourneyGraph } from "./api";
import { catalogItem, itemForNode, itemsFor, JOURNEY_CATALOG, reasonAt, searchItems } from "./journeyCatalog";
import { insertStep, NODE_TYPES, STEP_GROUPS, TRIGGER_ID, type InsertAt } from "./journeyGraph";

const labels = (cat: string) => JOURNEY_CATALOG.filter((it) => it.category === cat).map((it) => it.label);
const can = { canEditContacts: true };
const cannot = { canEditContacts: false };

const g: JourneyGraph = {
  trigger: { type: "event", event: "signup" },
  entry: "sms1",
  nodes: {
    sms1: { type: "send_sms", template_id: 1, next: "email1" },
    email1: { type: "send_email", template_id: 2, next: "wait1" },
    wait1: { type: "wait", seconds: 60, next: "x" },
    x: { type: "exit" },
  },
};
const afterStart: InsertAt = { source: TRIGGER_ID, handle: "next" };
const afterSms: InsertAt = { source: "sms1", handle: "next" };
const afterWait: InsertAt = { source: "wait1", handle: "next" };

describe("journey step catalog", () => {
  it("offers every decision step", () => {
    expect(labels("decision")).toEqual([
      "Opens email", "Clicks email", "Replies to email", "Downloads asset", "Submits form", "Visits a page",
      "Device visit", "Requests dynamic content", "Message outcome", "Does an event",
    ]);
  });

  it("offers every condition", () => {
    expect(labels("condition")).toEqual([
      "Contact field value", "Contact segments", "Contact tags", "Contact stages", "Contact points", "Contact owner",
      "Contact device", "Contact campaigns", "Form field value", "Has active notification", "Has valid email address",
      "Marked as Do Not Contact", "Prevent action after date", "Visited page",
    ]);
  });

  it("offers every action", () => {
    expect(labels("action")).toEqual([
      "Send email", "Send SMS", "Send WhatsApp", "Send RCS", "Send push", "Send to app inbox", "Send email to user",
      "Send marketing message", "Send a webhook", "Show focus item", "Push dynamic content", "Update contact",
      "Update contact owner", "Update company", "Add to company", "Update contact's primary company", "Add to company's score",
      "Adjust contact points", "Change contact's stage", "Modify contact's tags", "Modify contact's segments", "Change campaigns",
      "Jump to step", "Add Do Not Contact", "Remove Do Not Contact", "Delete contact", "Push contact to integration", "Exit journey",
    ]);
  });

  it("offers waits and splits", () => {
    expect(labels("wait")).toEqual(["Wait", "Wait until"]);
    expect(labels("split")).toEqual(["A/B split", "Percentage split"]);
    expect(STEP_GROUPS.every((grp) => itemsFor(grp.group).length > 0)).toBe(true);
  });

  it("gives every item a unique id and label, a description and a known step type", () => {
    expect(new Set(JOURNEY_CATALOG.map((it) => it.id)).size).toBe(JOURNEY_CATALOG.length);
    expect(new Set(JOURNEY_CATALOG.map((it) => it.label)).size).toBe(JOURNEY_CATALOG.length);
    for (const it of JOURNEY_CATALOG) {
      expect(it.description.length).toBeGreaterThan(10);
      expect(NODE_TYPES.some((t) => t.type === it.make().type)).toBe(true);
    }
    // Every stored step type can be made from the chooser.
    for (const t of NODE_TYPES) expect(JOURNEY_CATALOG.some((it) => it.type === t.type)).toBe(true);
  });

  it("makes steps with their preset", () => {
    expect(catalogItem("tags")!.make()).toMatchObject({ type: "check", preset: "tags" });
    expect(catalogItem("primary_company")!.make()).toEqual({ type: "add_company" });
    expect(catalogItem("percent_split")!.make().variants).toEqual([{ weight: 70, label: "Path 1" }, { weight: 30, label: "Path 2" }]);
    expect(catalogItem("ab_split")!.make().variants).toEqual([{ weight: 50, label: "A" }, { weight: 50, label: "B" }]);
    expect(catalogItem("wait")!.make()).toEqual({ type: "wait", seconds: 86400 });
    expect(catalogItem("device_visit")!.make()).toMatchObject({ type: "on_device", device_type: "mobile", seconds: 86400 });
  });

  it("watches the nearest earlier email", () => {
    expect(catalogItem("clicks_email")!.make(g, afterWait)).toEqual({ type: "message_decision", seconds: 172800, step: "email1", outcome: "clicked" });
    expect(catalogItem("message_outcome")!.make(g, afterSms)).toMatchObject({ step: "sms1", outcome: "delivered" });
    expect(catalogItem("message_outcome")!.make(g, afterWait)).toMatchObject({ step: "email1", outcome: "opened" });
  });

  it("needs a Send email step above an email decision", () => {
    const opens = catalogItem("opens_email")!;
    expect(reasonAt(opens, g, afterStart, can)).toBe("Opens email: add a Send email step above it first");
    expect(reasonAt(opens, g, afterSms, can)).toBe("Opens email: add a Send email step above it first");
    expect(reasonAt(opens, g, afterWait, can)).toBeNull();
    expect(reasonAt(catalogItem("replies_email")!, g, afterSms, can)).toBe("Replies to email: add a Send email step above it first");
    expect(reasonAt(catalogItem("message_outcome")!, g, afterStart, can)).toBe("Message outcome: add a send step above it first");
    expect(reasonAt(catalogItem("message_outcome")!, g, afterSms, can)).toBeNull();
    // The inserted decision passes validation.
    const r = insertStep(g, afterWait, opens.make(g, afterWait))!;
    expect(r.graph.nodes[r.id]).toMatchObject({ step: "email1", outcome: "opened", yes: "x" });
  });

  it("only allows steps that end a path at the end of one", () => {
    expect(reasonAt(catalogItem("exit")!, g, afterSms, can)).toBe("Ends the path: add it at the end of a path");
    expect(reasonAt(catalogItem("exit")!, g, afterWait, can)).toBeNull(); // replaces the Exit
    expect(reasonAt(catalogItem("jump")!, g, afterWait, can)).toBeNull();
    expect(reasonAt(catalogItem("wait")!, g, afterSms, can)).toBeNull();
  });

  it("needs contacts edit access to delete contacts", () => {
    const del = catalogItem("delete_contact")!;
    expect(reasonAt(del, g, afterWait, cannot)).toBe("Needs edit access to contacts");
    expect(reasonAt(del, g, afterWait, can)).toBeNull();
    expect(reasonAt(del, g, afterSms, can)).toBe("Ends the path: add it at the end of a path");
  });

  it("inserts Push contact to integration anywhere an action goes", () => {
    const it = catalogItem("push_integration")!;
    for (const at of [afterStart, afterSms, afterWait]) expect(reasonAt(it, g, at, can)).toBeNull();
    expect(it.make().type).toBe("push_integration");
  });

  it("refuses insertion points that no longer exist", () => {
    expect(reasonAt(catalogItem("wait")!, g, { source: "gone", handle: "next" }, can)).toBe("That step no longer exists.");
  });

  it("searches labels, descriptions and keywords", () => {
    expect(searchItems(JOURNEY_CATALOG, "form").map((it) => it.label)).toEqual(expect.arrayContaining(["Submits form", "Form field value"]));
    expect(searchItems(JOURNEY_CATALOG, "lifecycle").map((it) => it.label)).toEqual(["Contact stages", "Change contact's stage"]);
    expect(searchItems(itemsFor("decision"), "email click").map((it) => it.label)).toEqual(["Clicks email"]);
    expect(searchItems(JOURNEY_CATALOG, "")).toHaveLength(JOURNEY_CATALOG.length);
  });

  it("reads stored steps back as their catalog item", () => {
    expect(itemForNode(g, { type: "check", preset: "owner" })?.label).toBe("Contact owner");
    expect(itemForNode(g, { type: "check" })?.label).toBe("Contact field value");
    expect(itemForNode(g, { type: "message_decision", step: "email1", outcome: "clicked" })?.label).toBe("Clicks email");
    expect(itemForNode(g, { type: "split", variants: [{ weight: 7, label: "Path 1" }, { weight: 3, label: "Path 2" }] })?.label).toBe("Percentage split");
    expect(itemForNode(g, { type: "add_company" })?.label).toBe("Add to company");
  });
});
