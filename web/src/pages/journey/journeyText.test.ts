import { describe, expect, it } from "vitest";
import type { JourneyGraph, JourneyNode } from "../../api";
import { record, redo, startHistory, undo, HISTORY_CAP } from "./history";
import { summary } from "./nodeText";
import { deviceRule, ownerRule, parseDevice, parseOwner, parsePoints, parseStage, parseTags, pointsRule, stageRule, tagsRule } from "./presets";

const graphOf = (nodes: Record<string, JourneyNode>): JourneyGraph => ({ trigger: { type: "event", event: "signup" }, entry: Object.keys(nodes)[0], nodes });
const one = (n: JourneyNode, names = {}) => summary(graphOf({ n }), "n", names);

describe("canvas summaries", () => {
  it("words decisions", () => {
    expect(one({ type: "on_form", form_id: 3, seconds: 3 * 86400 }, { forms: { 3: "Newsletter" } })).toBe("Submits form ‘Newsletter’ within 3 days?");
    expect(one({ type: "on_form", seconds: 3600 })).toBe("Submits any form within 1 hour?");
    expect(one({ type: "on_page", url: "/pricing", seconds: 86400 })).toBe("Visits a page containing ‘/pricing’ within 1 day?");
    expect(one({ type: "on_device", device_type: "mobile", os: "iOS", seconds: 86400 })).toBe("Visits from mobile on iOS within 1 day?");
    expect(one({ type: "on_dwc", slot: "hero", seconds: 86400 }, { slots: { hero: "Hero banner" } })).toBe("Page requests ‘Hero banner’ within 1 day?");
  });

  it("words actions", () => {
    expect(one({ type: "send_email", template_id: 1 }, { templates: { 1: "Welcome" } })).toBe("Send email ‘Welcome’");
    expect(one({ type: "send_sms", template_id: 1 }, { templates: { 1: "OTP" } })).toBe("Send SMS ‘OTP’");
    expect(one({ type: "update_list", add_lists: [1], remove_lists: [2] }, { segments: { 1: "VIP", 2: "Trial" } })).toBe("Add to list ‘VIP’, remove from ‘Trial’");
    expect(one({ type: "update_list", list_id: 1, list_action: "remove" }, { segments: { 1: "VIP" } })).toBe("Remove from ‘VIP’");
    expect(one({ type: "delete_contact", confirm: "DELETE" })).toBe("Delete contact (permanent)");
    expect(one({ type: "send_message", channels: [{ channel: "push" }, { channel: "email" }] })).toBe("Send by Push, then Email");
    expect(one({ type: "notify_user", template_id: 1, to_owner: true, emails: ["a@x.io"] }, { templates: { 1: "Alert" } })).toBe("Email ‘Alert’ to the owner and a@x.io");
    expect(one({ type: "update_owner", owner_id: 0 })).toBe("Remove the owner");
    expect(one({ type: "dnc_add", channel: "all" })).toBe("Do Not Contact: every channel");
    expect(one({ type: "update_tags", add_tags: ["vip"], remove_tags: ["trial"] })).toBe("Add vip; remove trial");
  });

  it("words jumps with the target's step number", () => {
    const g = graphOf({ a: { type: "wait", seconds: 60, next: "b" }, b: { type: "send_sms", template_id: 1, next: "c" }, c: { type: "jump", target: "b" } });
    expect(summary(g, "c")).toBe("Jump to step 2 (max 25 times)");
  });

  it("words condition presets", () => {
    expect(one({ type: "check", preset: "stage", rule: stageRule(["MQL", "SQL"]) })).toBe("Stage is MQL or SQL?");
    expect(one({ type: "check", preset: "tags", rule: tagsRule({ match: "all", tags: ["vip", "paid"] }) })).toBe("Has tags vip and paid?");
    expect(one({ type: "check", preset: "points", rule: pointsRule({ op: "gte", value: 50 }) })).toBe("Points at least 50?");
    expect(one({ type: "check", preset: "owner", rule: ownerRule({ mode: "not_exists", ids: [] }) })).toBe("Has no owner?");
    expect(one({ type: "check", preset: "device", rule: deviceRule({ types: ["mobile"], os: ["iOS"] }) })).toBe("Device is mobile and OS is iOS?");
    expect(one({ type: "if_dnc", channel: "email" })).toBe("Do Not Contact for Email?");
  });
});

describe("condition presets", () => {
  it("build the rules the spec names and read them back", () => {
    expect(tagsRule({ match: "any", tags: ["vip", "lead"] })).toEqual({ any: [{ tag: "vip", op: "has" }, { tag: "lead", op: "has" }] });
    expect(tagsRule({ match: "any", tags: [] })).toBeUndefined();
    expect(parseTags(tagsRule({ match: "all", tags: ["a"] }))).toEqual({ match: "all", tags: ["a"] });
    expect(stageRule(["MQL"])).toEqual({ attr: "stage", op: "in", value: ["MQL"] });
    expect(parseStage({ attr: "stage", op: "eq", value: "SQL" })).toEqual(["SQL"]);
    expect(pointsRule({ op: "lt", value: 10 })).toEqual({ attr: "points", op: "lt", value: 10 });
    expect(parsePoints({ attr: "points", op: "between", value: [1, 2] })).toBeNull();
    expect(ownerRule({ mode: "in", ids: ["3", "4"] })).toEqual({ attr: "owner_id", op: "in", value: ["3", "4"] });
    expect(ownerRule({ mode: "exists", ids: [] })).toEqual({ attr: "owner_id", op: "exists" });
    expect(parseOwner({ attr: "owner_id", op: "eq", value: "3" })).toEqual({ mode: "in", ids: ["3"] });
    expect(deviceRule({ types: ["desktop"], os: [] })).toEqual({ attr: "device_type", op: "in", value: ["desktop"] });
    expect(deviceRule({ types: ["mobile"], os: ["Android"] })).toEqual({ all: [{ attr: "device_type", op: "in", value: ["mobile"] }, { attr: "device_os", op: "in", value: ["Android"] }] });
    expect(parseDevice(deviceRule({ types: ["mobile"], os: ["Android"] }))).toEqual({ types: ["mobile"], os: ["Android"] });
    expect(parseDevice({ attr: "city", op: "eq", value: "Pune" })).toBeNull();
  });
});

describe("undo and redo", () => {
  it("steps back and forward, and a new edit clears redo", () => {
    let h = startHistory(1);
    h = record(h, 2, undefined, 0);
    h = record(h, 3, undefined, 5000);
    h = undo(h);
    expect(h.present).toBe(2);
    h = undo(undo(h));
    expect(h.present).toBe(1);
    h = redo(h);
    expect(h.present).toBe(2);
    h = record(h, 9, undefined, 9000);
    expect(h.future).toEqual([]);
    expect(redo(h)).toBe(h);
  });
  it("merges quick edits with the same key and caps the history", () => {
    let h = startHistory(0);
    h = record(h, 1, "name", 0);
    h = record(h, 2, "name", 500);
    expect(h.past).toEqual([0]);
    h = record(h, 3, "name", 2000);
    expect(h.past).toEqual([0, 2]);
    for (let i = 0; i < 150; i++) h = record(h, 100 + i, undefined, 10000 + i);
    expect(h.past).toHaveLength(HISTORY_CAP);
  });
});
