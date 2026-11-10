import { describe, expect, it } from "vitest";
import type { JourneyGraph } from "./api";
import {
  addNode, connect, decisionText, disconnect, emptyGraph, formatWait, NODE_TYPES, OUTCOMES, outputs, problems, removeNode, sendSteps, STEP_GROUPS, toFlow, trackedSendsAfter, TRIGGER_ID, upstream, winnerProblem,
} from "./journeyGraph";

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

describe("app inbox and message decisions", () => {
  const md: JourneyGraph = {
    trigger: { type: "event", event: "signup" },
    entry: "email1",
    nodes: {
      email1: { type: "send_email", template_id: 7, next: "decision1" },
      decision1: { type: "message_decision", step: "email1", outcome: "opened", seconds: 2 * 86400, yes: "inbox1", no: "x" },
      inbox1: { type: "send_inbox", template_id: 9, next: "x" },
      x: { type: "exit" },
    },
  };

  it("puts every step type in a palette group", () => {
    const groups = new Set(STEP_GROUPS.map((g) => g.group));
    expect(NODE_TYPES.every((t) => groups.has(t.group) && t.hint.length > 0)).toBe(true);
    expect(NODE_TYPES.find((t) => t.type === "message_decision")?.group).toBe("decision");
    expect(NODE_TYPES.find((t) => t.type === "send_inbox")?.channel).toBe("inbox");
  });

  it("gives a message decision yes and no paths", () => {
    const edges = toFlow(md).edges;
    expect(edges.map((e) => e.id)).toEqual(expect.arrayContaining(["decision1:yes", "decision1:no", "inbox1:next"]));
    expect(connect(md, "decision1", "no", "inbox1").nodes.decision1.no).toBe("inbox1");
    expect(problems(md)).toEqual([]);
  });

  it("describes the decision on the canvas", () => {
    expect(decisionText(md, md.nodes.decision1, (id) => (id === 7 ? "Welcome" : undefined))).toBe("Email \u2018Welcome\u2019 opened within 2 days?");
    expect(decisionText(md, { ...md.nodes.decision1, outcome: "read" })).toBe("Email \u2018email1\u2019 \u2026 within 2 days?");
    expect(decisionText(md, { type: "message_decision" })).toBe("choose a message");
  });

  it("checks the step, the outcome for its channel and the time limit like the server", () => {
    const with_ = (patch: object) => problems({ ...md, nodes: { ...md.nodes, decision1: { ...md.nodes.decision1, ...patch } } });
    expect(with_({ step: "x" })).toContain('"decision1": choose the message it waits on.');
    expect(with_({ step: undefined })).toContain('"decision1": choose the message it waits on.');
    expect(with_({ outcome: "read" })).toContain('"decision1": choose what should happen to the Email message.');
    expect(with_({ step: "inbox1", outcome: "clicked" })).toEqual([]);
    expect(with_({ step: "inbox1", outcome: "delivered" })).toContain('"decision1": choose what should happen to the App inbox message.');
    expect(with_({ seconds: 30 })).toContain('"decision1": wait between 1 minute and 365 days.');
    expect(with_({ seconds: 60 })).toEqual([]);
    expect(OUTCOMES.sms).toEqual(["delivered"]);
    expect(OUTCOMES.whatsapp).toEqual(["delivered", "read"]);
  });

  it("forgets a removed send step and preselects the only one", () => {
    expect(removeNode(md, "email1").nodes.decision1.step).toBeUndefined();
    const a = addNode(md, "message_decision", 0, 0);
    expect(a.id).toBe("decision2");
    expect(a.graph.nodes.decision2).toMatchObject({ seconds: 172800 }); // two send steps: nothing chosen
    expect(a.graph.nodes.decision2.step).toBeUndefined();
    const one = addNode(removeNode(md, "inbox1"), "message_decision", 0, 0);
    expect(one.graph.nodes[one.id]).toMatchObject({ step: "email1", outcome: "opened" });
  });

  it("lists send steps and what comes before a step", () => {
    expect(sendSteps(md).map(([id, , ch]) => [id, ch])).toEqual([["email1", "email"], ["inbox1", "inbox"]]);
    expect([...upstream(md, "decision1")]).toEqual(["email1"]);
    expect([...upstream(md, "x")].sort()).toEqual(["decision1", "email1", "inbox1"]);
    expect(addNode(md, "send_inbox", 0, 0).id).toBe("inbox2");
    const p = problems(addNode(md, "send_inbox", 0, 0).graph);
    expect(p).toContain('"inbox2": choose a template.');
  });
});

describe("change stage step", () => {
  it("is an action that needs a stage", () => {
    expect(NODE_TYPES.find((n) => n.type === "change_stage")?.group).toBe("action");
    const { graph, id } = addNode(emptyGraph(), "change_stage", 0, 0);
    expect(id).toBe("stage1");
    expect(outputs(graph.nodes[id])).toEqual([["next", undefined]]);
    expect(problems(graph)).toContain(`"${id}": choose the stage to move the contact to.`);
    graph.nodes[id].stage = "MQL";
    expect(problems(graph).some((p) => p.includes("choose the stage"))).toBe(false);
  });
});

describe("A/B winners on splits", () => {
  const ab: JourneyGraph = {
    trigger: { type: "event", event: "signup" },
    entry: "ab",
    nodes: {
      ab: { type: "split", variants: [{ weight: 1, next: "ma", label: "A" }, { weight: 1, next: "s", label: "B" }], winner: { metric: "clicked", after_seconds: 3600, min_per_variant: 100 } },
      ma: { type: "send_email", template_id: 1, next: "x" },
      s: { type: "send_sms", template_id: 2, next: "w" },
      w: { type: "wait", seconds: 60, next: "mb" },
      mb: { type: "send_inbox", template_id: 3, next: "x" },
      x: { type: "exit" },
    },
  };
  it("measures opens and clicks on email and inbox steps after the split", () => {
    expect(trackedSendsAfter(ab, "ab")).toEqual(["ma", "mb"]);
    expect(winnerProblem(ab, "ab")).toBeNull();
  });
  it("explains settings the server refuses", () => {
    const conv = { ...ab, nodes: { ...ab.nodes, ab: { ...ab.nodes.ab, winner: { metric: "converted" as const, after_seconds: 60, min_per_variant: 1 } } } };
    expect(winnerProblem(conv, "ab")).toMatch(/goal/);
    expect(winnerProblem({ ...conv, goal: { event: "purchase" } }, "ab")).toBeNull();
    const smsOnly = { ...ab, nodes: { ab: { ...ab.nodes.ab, variants: [{ weight: 1, next: "s" }, { weight: 1, next: "x" }] }, s: { type: "send_sms", template_id: 2, next: "x" }, x: { type: "exit" } } };
    expect(winnerProblem(smsOnly, "ab")).toMatch(/email or app inbox/);
    const few = { ...ab, nodes: { ...ab.nodes, ab: { ...ab.nodes.ab, winner: { metric: "clicked" as const, after_seconds: 60, min_per_variant: 0 } } } };
    expect(problems(few).some((p) => p.includes("1 to 1,000,000"))).toBe(true);
  });
});
