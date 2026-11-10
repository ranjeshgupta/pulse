import { describe, expect, it } from "vitest";
import type { JourneyGraph } from "./api";
import { addNode, connect, disconnect, emptyGraph, formatWait, problems, removeNode, toFlow, TRIGGER_ID } from "./journeyGraph";

const g: JourneyGraph = {
  trigger: { type: "segment_entered", segment_id: 4 },
  entry: "wait1",
  nodes: {
    wait1: { type: "wait", seconds: 3600, next: "check1" },
    check1: { type: "condition", segment_id: 9, yes: "wa1", no: "exit" },
    wa1: { type: "send_whatsapp", template_id: 2, fallback_template_id: 3, next: "exit" },
    exit: { type: "exit" },
  },
};

describe("journey graph <-> canvas", () => {
  it("derives edges from next / yes / no and the entry", () => {
    const { nodes, edges } = toFlow(g);
    expect(nodes).toHaveLength(5); // 4 steps + the trigger
    expect(edges.map((e) => e.id).sort()).toEqual([`${TRIGGER_ID}:next`, "check1:no", "check1:yes", "wa1:next", "wait1:next"]);
    expect(edges.find((e) => e.id === "check1:yes")?.label).toBe("in segment");
  });

  it("connects by handle and from the trigger", () => {
    let x = connect(g, "check1", "no", "wa1");
    expect(x.nodes.check1.no).toBe("wa1");
    x = connect(x, TRIGGER_ID, "next", "check1");
    expect(x.entry).toBe("check1");
    expect(connect(g, "wa1", "next", "wa1")).toBe(g); // no self-loops
    expect(connect(g, "wa1", "next", TRIGGER_ID)).toBe(g); // nothing points at the trigger
  });

  it("disconnects edges and removes nodes with every reference to them", () => {
    expect(disconnect(g, "check1:yes").nodes.check1.yes).toBeUndefined();
    expect(disconnect(g, `${TRIGGER_ID}:next`).entry).toBe("");
    const r = removeNode(g, "exit");
    expect(r.nodes.exit).toBeUndefined();
    expect(r.nodes.check1.no).toBeUndefined();
    expect(r.nodes.wa1.next).toBeUndefined();
  });

  it("names new nodes uniquely and gives waits a default", () => {
    const a = addNode(g, "send_whatsapp", 0, 0);
    expect(a.id).toBe("whatsapp1");
    expect(addNode(a.graph, "send_whatsapp", 0, 0).id).toBe("whatsapp2");
    expect(addNode(g, "wait", 0, 0).graph.nodes.wait2.seconds).toBe(86400);
  });

  it("lists what blocks going live", () => {
    expect(problems(g)).toEqual([]);
    const p = problems(emptyGraph());
    expect(p).toContain("Choose the segment that starts this journey.");
    const broken = removeNode(addNode(g, "send_sms", 0, 0).graph, "exit");
    expect(problems(broken)).toEqual(expect.arrayContaining(['"sms1": choose a template.', '"sms1": connect it to the next step.']));
  });

  it("formats waits", () => {
    expect([formatWait(86400), formatWait(7200), formatWait(300), formatWait(45)]).toEqual(["1 day", "2 hours", "5 min", "45 s"]);
  });
});

describe("new step types", () => {
  const split: JourneyGraph = {
    trigger: { type: "event", event: "signup" },
    entry: "s",
    nodes: {
      s: { type: "split", variants: [{ weight: 30, label: "A", next: "a" }, { weight: 70, label: "B" }] },
      a: { type: "wait_for_event", event: "purchase", seconds: 3600, yes: "x", no: "x" },
      x: { type: "exit" },
    },
  };

  it("gives each split path its own handle, labelled with its share", () => {
    const edges = toFlow(split).edges;
    expect(edges.find((e) => e.id === "s:v0")?.label).toBe("A 30%");
    expect(edges.find((e) => e.id === "a:yes")?.label).toBe("did it");
    expect(edges.find((e) => e.id === "a:no")?.label).toBe("timed out");
  });

  it("connects, disconnects and cleans up split paths", () => {
    let g = connect(split, "s", "v1", "x");
    expect(g.nodes.s.variants?.[1].next).toBe("x");
    g = disconnect(g, "s:v0");
    expect(g.nodes.s.variants?.[0].next).toBeUndefined();
    expect(removeNode(g, "x").nodes.s.variants?.[1].next).toBeUndefined();
  });

  it("reports unconnected paths and missing settings", () => {
    const p = problems({ ...split, goal: { event: "bad name" }, trigger: { type: "event", event: "signup", reentry: "after_days" } });
    expect(p).toEqual(expect.arrayContaining([
      '"s": connect the "B" path.',
      "Enter the goal event, or remove the goal.",
      "Set after how many days a contact can enter again.",
    ]));
    const hook = addNode(emptyGraph(), "webhook", 0, 0);
    expect(problems(hook.graph)).toContain(`"${hook.id}": enter the webhook's https:// address.`);
  });
});
