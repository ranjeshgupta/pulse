import { describe, expect, it } from "vitest";
import type { JourneyGraph } from "./api";
import { branchIds, copySteps, parseClipboard, pasteSteps } from "./journeyClipboard";
import { childrenOf, numberedOrder, TRIGGER_ID } from "./journeyGraph";

const src: JourneyGraph = {
  trigger: { type: "event", event: "cart_abandoned" },
  entry: "wait1",
  nodes: {
    wait1: { type: "wait", seconds: 3600, next: "email1", x: 0, y: 200 },
    email1: { type: "send_email", template_id: 7, next: "opened1", x: 0, y: 400 },
    opened1: { type: "message_decision", step: "email1", outcome: "opened", seconds: 86400, yes: "exit", no: "sms1", x: 0, y: 600 },
    sms1: { type: "send_sms", template_id: 3, next: "jump1", x: 280, y: 800 },
    jump1: { type: "jump", target: "wait1", x: 280, y: 1000 },
    exit: { type: "exit", x: -280, y: 800 },
  },
};

const dest: JourneyGraph = {
  trigger: { type: "segment_entered", segment_id: 4 },
  entry: "email1",
  nodes: {
    email1: { type: "send_email", template_id: 2, next: "exit", x: 300, y: 200 },
    exit: { type: "exit", x: 300, y: 400 },
  },
};

describe("copySteps", () => {
  it("keeps links inside the selection and drops the rest", () => {
    const c = copySteps(src, ["email1", "opened1", "sms1", TRIGGER_ID], 12)!;
    expect(Object.keys(c.nodes).sort()).toEqual(["email1", "opened1", "sms1"]);
    expect(c.entry).toBe("email1");
    expect(c.from).toBe(12);
    expect(c.nodes.opened1.yes).toBeUndefined(); // exit wasn't copied
    expect(c.nodes.opened1.no).toBe("sms1");
    expect(c.nodes.sms1.next).toBeUndefined();
    expect(c.nodes.opened1.step).toBe("email1");
  });
  it("copies nothing without steps", () => {
    expect(copySteps(src, [TRIGGER_ID])).toBeNull();
  });
  it("branchIds follows every path after a step, not jumps", () => {
    expect(branchIds(src, "opened1").sort()).toEqual(["exit", "jump1", "opened1", "sms1"]);
  });
});

describe("parseClipboard", () => {
  it("round-trips a copy", () => {
    const c = copySteps(src, ["wait1", "email1"])!;
    expect(parseClipboard(JSON.stringify(c)).clip).toEqual(c);
  });
  it("refuses other text with a reason", () => {
    expect(parseClipboard("hello").error).toMatch(/doesn't hold journey steps/);
    expect(parseClipboard(JSON.stringify({ pulse: "segment-rules", version: 1 })).error).toMatch(/doesn't hold journey steps/);
    expect(parseClipboard(JSON.stringify({ pulse: "journey-steps", version: 2, entry: "a", nodes: { a: { type: "exit" } } })).error).toMatch(/newer/);
    expect(parseClipboard(JSON.stringify({ pulse: "journey-steps", version: 1, entry: "a", nodes: { a: { type: "teleport" } } })).error).toMatch(/doesn't know/);
    expect(parseClipboard(JSON.stringify({ pulse: "journey-steps", version: 1, entry: "b", nodes: { a: { type: "exit" } } })).error).toMatch(/first step/);
    const many = Object.fromEntries(Array.from({ length: 101 }, (_, i) => [`n${i}`, { type: "exit" }]));
    expect(parseClipboard(JSON.stringify({ pulse: "journey-steps", version: 1, entry: "n0", nodes: many })).error).toMatch(/1 to 100/);
  });
});

describe("pasteSteps", () => {
  it("inserts on an edge: new ids, internal links kept, the old path continues after", () => {
    const c = copySteps(src, ["email1", "opened1", "sms1"])!;
    const { graph, ids, entry } = pasteSteps(dest, { source: "email1", handle: "next" }, c);
    expect(ids).toHaveLength(3);
    expect(new Set(ids).size).toBe(3);
    expect(ids).not.toContain("email1"); // email1 already exists here
    expect(graph.nodes.email1.next).toBe(entry);
    const pasted = graph.nodes[entry];
    expect(pasted.type).toBe("send_email");
    const decision = graph.nodes[pasted.next!];
    expect(decision.type).toBe("message_decision");
    expect(decision.step).toBe(entry); // watches the pasted email, not the old one
    // The main path (first outputs: email → decision Yes) was free at Yes: the old exit follows there.
    expect(decision.yes).toBe("exit");
    expect(graph.nodes[decision.no!].type).toBe("send_sms");
    // Laid out below the insertion point, and the exit moved down to make room.
    expect(pasted.y).toBe(400);
    expect(graph.nodes.exit.y!).toBeGreaterThan(Math.max(...ids.map((id) => graph.nodes[id].y!)));
    expect(numberedOrder(graph)).toHaveLength(5);
  });
  it("pastes twice without clashing ids", () => {
    const c = copySteps(src, ["wait1"])!;
    const a = pasteSteps(dest, { source: TRIGGER_ID, handle: "next" }, c);
    const b = pasteSteps(a.graph, { source: TRIGGER_ID, handle: "next" }, c);
    expect(a.entry).not.toBe(b.entry);
    expect(b.graph.entry).toBe(b.entry);
    expect(childrenOf(b.graph, b.entry)).toEqual([a.entry]);
  });
  it("keeps a jump inside the pasted steps and drops one that pointed outside", () => {
    const inside = copySteps(src, ["wait1", "email1", "opened1", "sms1", "jump1"])!;
    const r = pasteSteps(dest, { source: "email1", handle: "next" }, inside);
    const jump = Object.values(r.graph.nodes).find((n) => n.type === "jump")!;
    expect(r.ids).toContain(jump.target);
    const outside = copySteps(src, ["jump1"])!;
    const r2 = pasteSteps({ ...dest, nodes: { ...dest.nodes, exit: { type: "exit" } } }, { source: "email1", handle: "next" }, outside);
    expect(r2.graph.nodes[r2.entry].target).toBeUndefined();
  });
});
