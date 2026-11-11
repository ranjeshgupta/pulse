import { describe, expect, it } from "vitest";
import type { JourneyGraph, JourneyNode } from "./api";
import {
  addNode, overlapping, childrenOf, COL, connect, cycleAt, decisionText, defaultInsertPoint, deleteStep, disconnect, edgeLabel, emptyGraph, formatWait,
  insertProblem, insertStep, issues, NODE_TYPES, OUTCOMES, outputs, positions, problems, removeBranch, removeNode, ROW, sendSteps,
  STEP_GROUPS, stepLabel, stepNumbers, targetAt, tidy, toFlow, trackedSendsAfter, TRIGGER_ID, upstream, winnerProblem, wouldCycle,
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

/** The problem texts about one step, without the "Step N · Label: " prefix. */
const about = (graph: JourneyGraph, step: string, opts = {}) =>
  issues(graph, opts).filter((p) => p.step === step).map((p) => p.text.replace(/^Step \d+ · [^:]+: /, ""));
const withNode = (graph: JourneyGraph, id: string, patch: Partial<JourneyNode>): JourneyGraph =>
  ({ ...graph, nodes: { ...graph.nodes, [id]: { ...graph.nodes[id], ...patch } } });

describe("journey graph <-> canvas", () => {
  it("derives edges from next / yes / no and the entry, labelled Yes and No", () => {
    const { nodes, edges } = toFlow(g);
    expect(nodes).toHaveLength(5); // 4 steps + the trigger
    expect(edges.map((e) => e.id).sort()).toEqual([`${TRIGGER_ID}:next`, "check1:no", "check1:yes", "wa1:next", "wait1:next"]);
    expect(edges.find((e) => e.id === "check1:yes")?.label).toBe("Yes");
    expect(edges.find((e) => e.id === "check1:no")?.data).toMatchObject({ kind: "no", source: "check1", handle: "no" });
  });

  it("labels a decision's No with its time limit", () => {
    const d: JourneyNode = { type: "on_form", seconds: 2 * 86400 };
    expect(edgeLabel(d, "yes")).toBe("Yes");
    expect(edgeLabel(d, "no")).toBe("No — after 2 days");
    expect(edgeLabel({ type: "if_dnc" }, "no")).toBe("No");
  });

  it("connects by handle and from the trigger", () => {
    let x = connect(g, "check1", "no", "wa1");
    expect(x.nodes.check1.no).toBe("wa1");
    x = connect(x, TRIGGER_ID, "next", "check1");
    expect(x.entry).toBe("check1");
    expect(connect(g, "wa1", "next", "wa1")).toBe(g); // no self-loops
    expect(connect(g, "wa1", "next", TRIGGER_ID)).toBe(g); // nothing points at the trigger
  });

  it("refuses connections that would loop", () => {
    expect(wouldCycle(g, "wa1", "wait1")).toBe(true);
    expect(wouldCycle(g, "wait1", "exit")).toBe(false);
    expect(cycleAt(g)).toBeUndefined();
    expect(cycleAt(connect(g, "wa1", "next", "wait1"))).toBeDefined();
    expect(problems(connect(g, "wa1", "next", "wait1")).some((p) => p.includes("loops back"))).toBe(true);
  });

  it("disconnects edges and removes nodes with every reference to them", () => {
    expect(disconnect(g, "check1:yes").nodes.check1.yes).toBeUndefined();
    expect(disconnect(g, `${TRIGGER_ID}:next`).entry).toBe("");
    expect(disconnect(g, "jump:x")).toBe(g);
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
    expect(about(broken, "sms1")).toEqual(expect.arrayContaining(["choose a template.", "connect it to the next step."]));
    expect(problems(broken)).toContain("Step 4 · Send SMS: choose a template.");
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
    expect(edges.find((e) => e.id === "s:v0")?.label).toBe("A · 30%");
    expect(edges.find((e) => e.id === "a:yes")?.label).toBe("Yes");
    expect(edges.find((e) => e.id === "a:no")?.label).toBe("No — after 1 hour");
  });

  it("connects, disconnects and cleans up split paths", () => {
    let g = connect(split, "s", "v1", "x");
    expect(g.nodes.s.variants?.[1].next).toBe("x");
    g = disconnect(g, "s:v0");
    expect(g.nodes.s.variants?.[0].next).toBeUndefined();
    expect(removeNode(g, "x").nodes.s.variants?.[1].next).toBeUndefined();
  });

  it("reports unconnected paths and missing settings", () => {
    const sp = { ...split, goal: { event: "bad name" }, trigger: { type: "event" as const, event: "signup", reentry: "after_days" as const } };
    expect(about(sp, "s")).toContain('connect the "B" path.');
    expect(problems(sp)).toEqual(expect.arrayContaining(["Enter the goal event, or remove the goal.", "Set after how many days (1 to 3650) a contact can enter again."]));
    const hook = addNode(emptyGraph(), "webhook", 0, 0);
    expect(about(hook.graph, hook.id)).toContain("enter the webhook's https:// address.");
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

  it("puts every step type in a group", () => {
    const groups = new Set(STEP_GROUPS.map((g) => g.group));
    expect(STEP_GROUPS.map((g) => g.label)).toEqual(["Action", "Decision", "Condition", "Wait", "Split"]);
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

  it("describes and names the decision on the canvas", () => {
    expect(decisionText(md, md.nodes.decision1, (id) => (id === 7 ? "Welcome" : undefined))).toBe("Email ‘Welcome’ opened within 2 days?");
    expect(decisionText(md, { ...md.nodes.decision1, outcome: "read" })).toBe("Email ‘email1’ … within 2 days?");
    expect(decisionText(md, { ...md.nodes.decision1, outcome: "clicked", url: "/pricing" })).toBe("Email ‘email1’ clicked (link contains ‘/pricing’) within 2 days?");
    expect(decisionText(md, { type: "message_decision" })).toBe("Choose a message");
    expect(stepLabel(md, md.nodes.decision1)).toBe("Opens email");
    expect(stepLabel(md, { ...md.nodes.decision1, outcome: "replied" })).toBe("Replies to email");
    expect(stepLabel(md, { ...md.nodes.decision1, step: "inbox1" })).toBe("Message outcome");
  });

  it("checks the step, the outcome for its channel and the time limit like the server", () => {
    const with_ = (patch: Partial<JourneyNode>) => about(withNode(md, "decision1", patch), "decision1");
    expect(with_({ step: "x" })).toContain("choose the message it waits on.");
    expect(with_({ step: undefined })).toContain("choose the message it waits on.");
    expect(with_({ outcome: "read" })).toContain("choose what should happen to the Email message.");
    expect(with_({ outcome: "replied" })).toEqual([]);
    expect(with_({ outcome: "opened", url: "/x" })).toContain('a link only applies to "a link is clicked".');
    expect(with_({ outcome: "clicked", url: "/x" })).toEqual([]);
    expect(with_({ step: "inbox1", outcome: "clicked" })).toContain("the message it waits on must be sent earlier on this path.");
    expect(with_({ seconds: 30 })).toContain("wait between 1 minute and 365 days.");
    expect(with_({ seconds: 60 })).toEqual([]);
    expect(OUTCOMES.sms).toEqual(["delivered"]);
    expect(OUTCOMES.whatsapp).toEqual(["delivered", "read"]);
    expect(OUTCOMES.email).toContain("replied");
    expect(OUTCOMES.sms).not.toContain("replied");
    expect(OUTCOMES.rcs).toContain("replied");
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
    expect(about(addNode(md, "send_inbox", 0, 0).graph, "inbox2")).toContain("choose a template.");
  });
});

describe("change stage step", () => {
  it("is an action that needs a stage", () => {
    expect(NODE_TYPES.find((n) => n.type === "change_stage")?.group).toBe("action");
    const { graph, id } = addNode(emptyGraph(), "change_stage", 0, 0);
    expect(id).toBe("stage1");
    expect(outputs(graph.nodes[id])).toEqual([["next", undefined]]);
    expect(about(graph, id)).toContain("choose the stage to move the contact to.");
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

// ---- insert at a handle ----

describe("inserting a step at a + button", () => {
  const line: JourneyGraph = {
    trigger: { type: "event", event: "signup" },
    entry: "email1",
    nodes: {
      email1: { type: "send_email", template_id: 1, next: "x", x: 0, y: ROW },
      x: { type: "exit", x: 0, y: 2 * ROW },
    },
  };

  it("puts an action between the two ends of an edge", () => {
    const r = insertStep(line, { source: "email1", handle: "next" }, { type: "wait", seconds: 60 })!;
    expect(r.id).toBe("wait1");
    expect(r.graph.nodes.email1.next).toBe("wait1");
    expect(r.graph.nodes.wait1.next).toBe("x");
    // It takes the old target's place and the rest moves down a row.
    expect(positions(r.graph).wait1).toEqual({ x: 0, y: 2 * ROW });
    expect(positions(r.graph).x).toEqual({ x: 0, y: 3 * ROW });
  });

  it("keeps the continuation on a decision's Yes and leaves No free with its own +", () => {
    const r = insertStep(line, { source: "email1", handle: "next" }, { type: "on_form", seconds: 3600 })!;
    const d = r.graph.nodes[r.id];
    expect(d.yes).toBe("x");
    expect(d.no).toBeUndefined();
    expect(targetAt(r.graph, { source: r.id, handle: "no" })).toBeUndefined();
    expect(about(r.graph, r.id)).toContain('connect the "No" path.');
    // Each branch takes its own step.
    const yes = insertStep(r.graph, { source: r.id, handle: "yes" }, { type: "send_sms", template_id: 2 })!;
    const no = insertStep(yes.graph, { source: r.id, handle: "no" }, { type: "exit" })!;
    expect(no.graph.nodes[r.id].yes).toBe(yes.id);
    expect(no.graph.nodes[yes.id].next).toBe("x");
    expect(no.graph.nodes[r.id].no).toBe(no.id);
    expect(problems(no.graph)).toEqual([]);
  });

  it("continues a split on its first path", () => {
    const r = insertStep(line, { source: TRIGGER_ID, handle: "next" }, { type: "split", variants: [{ weight: 70, label: "Path 1" }, { weight: 30, label: "Path 2" }] })!;
    expect(r.graph.entry).toBe(r.id);
    expect(r.graph.nodes[r.id].variants?.[0].next).toBe("email1");
    expect(r.graph.nodes[r.id].variants?.[1].next).toBeUndefined();
    expect(toFlow(r.graph).edges.find((e) => e.id === `${r.id}:v0`)?.label).toBe("Path 1 · 70%");
  });

  it("just connects on a free handle, below its source", () => {
    const open = disconnect(line, "email1:next");
    const r = insertStep(open, { source: "email1", handle: "next" }, { type: "update_tags", add_tags: ["vip"] })!;
    expect(r.graph.nodes.email1.next).toBe(r.id);
    expect(r.graph.nodes[r.id].next).toBeUndefined();
    expect(positions(r.graph)[r.id]).toEqual({ x: 0, y: 2 * ROW });
    // Yes and No stubs sit either side under a branching step.
    const d = insertStep(open, { source: "email1", handle: "next" }, { type: "if_dnc", channel: "any" })!;
    const no = insertStep(d.graph, { source: d.id, handle: "no" }, { type: "exit" })!;
    expect(positions(no.graph)[no.id]).toEqual({ x: COL / 2, y: 3 * ROW });
  });

  it("only ends a path where it ends already", () => {
    const twoStep = insertStep(line, { source: "email1", handle: "next" }, { type: "wait", seconds: 60 })!.graph;
    expect(insertProblem(twoStep, { source: "email1", handle: "next" }, "exit")).toBe("Ends the path: add it at the end of a path");
    expect(insertStep(twoStep, { source: "email1", handle: "next" }, { type: "jump" })).toBeNull();
    // Replacing an Exit is fine, and the old Exit goes.
    const r = insertStep(line, { source: "email1", handle: "next" }, { type: "delete_contact" })!;
    expect(r.graph.nodes.email1.next).toBe(r.id);
    expect(r.graph.nodes.x).toBeUndefined();
    expect(outputs(r.graph.nodes[r.id])).toEqual([]);
  });

  it("finds where the palette adds a step", () => {
    expect(defaultInsertPoint(line, "email1")).toEqual({ source: "email1", handle: "next" });
    const d = insertStep(line, { source: "email1", handle: "next" }, { type: "if_valid_email" })!;
    expect(defaultInsertPoint(d.graph, d.id)).toEqual({ source: d.id, handle: "no" });
    expect(defaultInsertPoint(d.graph, null)).toEqual({ source: d.id, handle: "no" });
    expect(defaultInsertPoint(emptyGraph(), null)).toEqual({ source: TRIGGER_ID, handle: "next" });
  });
});

describe("deleting steps", () => {
  const chain: JourneyGraph = {
    trigger: { type: "event", event: "signup" },
    entry: "a",
    nodes: {
      a: { type: "wait", seconds: 60, next: "b" },
      b: { type: "send_email", template_id: 1, next: "c" },
      c: { type: "if_dnc", channel: "email", yes: "x", no: "d" },
      d: { type: "send_sms", template_id: 2, next: "x" },
      x: { type: "exit" },
    },
  };
  it("heals over an action", () => {
    expect(deleteStep(chain, "b").nodes.a.next).toBe("c");
    expect(deleteStep(chain, "a").entry).toBe("b");
  });
  it("leaves a branching step's paths free, or removes the branch", () => {
    const r = deleteStep(chain, "c");
    expect(r.nodes.b.next).toBeUndefined();
    expect(r.nodes.d).toBeDefined();
    const br = removeBranch(chain, "c");
    expect(Object.keys(br.nodes).sort()).toEqual(["a", "b"]);
  });
  it("keeps steps other paths still lead to", () => {
    const shared = connect(chain, "a", "next", "c");
    const br = removeBranch(shared, "b");
    expect(Object.keys(br.nodes).sort()).toEqual(["a", "c", "d", "x"]);
  });
});

// ---- numbering and tidy layout ----

describe("step numbers and tidy layout", () => {
  const tree: JourneyGraph = {
    trigger: { type: "event", event: "signup" },
    entry: "e",
    nodes: {
      orphan: { type: "send_sms", template_id: 1, x: 900, y: 900 },
      x2: { type: "exit", x: 5, y: 5 },
      e: { type: "send_email", template_id: 1, next: "d", x: 50, y: 50 },
      d: { type: "on_form", seconds: 3600, yes: "s", no: "x2", x: 0, y: 0 },
      s: { type: "split", variants: [{ weight: 1, next: "a1" }, { weight: 1, next: "a2" }, { weight: 1, next: "x1" }], x: 0, y: 0 },
      a1: { type: "wait", seconds: 60, next: "x1", x: 0, y: 0 },
      a2: { type: "update_tags", add_tags: ["t"], next: "x1", x: 0, y: 0 },
      x1: { type: "exit", x: 0, y: 0 },
    },
  };

  it("numbers steps breadth first from Start, handles in order, then unreached steps", () => {
    expect(stepNumbers(tree)).toEqual({ e: 1, d: 2, s: 3, x2: 4, a1: 5, a2: 6, x1: 7, orphan: 8 });
    expect(stepNumbers(emptyGraph())).toEqual({ exit: 1 });
  });

  it("lays out top-down without overlaps, children under parents in handle order", () => {
    const t = tidy(tree);
    expect(tidy(tree)).toEqual(t); // deterministic
    const p = positions(t);
    const ids = Object.keys(t.nodes);
    // No two cards share a spot: same row means at least a column apart.
    for (const a of ids) for (const b of ids) {
      if (a < b && p[a].y === p[b].y) expect(Math.abs(p[a].x - p[b].x)).toBeGreaterThanOrEqual(COL);
      if (a < b) expect(p[a].x === p[b].x && p[a].y === p[b].y).toBe(false);
    }
    // Every child is below every parent.
    for (const id of ids) for (const c of childrenOf(t, id)) expect(p[c].y).toBeGreaterThan(p[id].y);
    // Yes is left of No; split paths left to right.
    expect(p.s.x).toBeLessThan(p.x2.x);
    expect(p.a1.x).toBeLessThan(p.a2.x);
    // A merge sits below its lowest parent.
    expect(p.x1.y).toBe(p.a1.y + ROW);
    // Start sits a row above the first step, which is at the top.
    expect(p[TRIGGER_ID]).toEqual({ x: p.e.x, y: 0 });
    expect(p.e.y).toBe(ROW);
    // The unreached step goes to the right.
    expect(p.orphan.x).toBeGreaterThan(Math.max(...ids.filter((i) => i !== "orphan").map((i) => p[i].x)));
  });

  it("lays out a long chain in one column", () => {
    let c = emptyGraph();
    for (let i = 0; i < 5; i++) c = insertStep(c, { source: TRIGGER_ID, handle: "next" }, { type: "wait", seconds: 60 })!.graph;
    const p = positions(tidy(c));
    expect(new Set(Object.keys(c.nodes).map((id) => p[id].x)).size).toBe(1);
    expect(new Set(Object.keys(c.nodes).map((id) => p[id].y)).size).toBe(6);
  });
});

// ---- jump steps ----

describe("jump to step", () => {
  const j: JourneyGraph = {
    trigger: { type: "event", event: "signup" },
    entry: "e",
    nodes: {
      e: { type: "send_email", template_id: 1, next: "d" },
      d: { type: "message_decision", step: "e", outcome: "opened", seconds: 86400, yes: "x", no: "j" },
      j: { type: "jump", target: "e" },
      x: { type: "exit" },
    },
  };
  it("draws a dashed edge that isn't a real path, so loops through it are fine", () => {
    const edge = toFlow(j).edges.find((e) => e.id === "jump:j");
    expect(edge).toMatchObject({ type: "jump", target: "e", deletable: false });
    expect(cycleAt(j)).toBeUndefined();
    expect(problems(j)).toEqual([]);
    expect(stepNumbers(j)).toEqual({ e: 1, d: 2, x: 3, j: 4 });
  });
  it("needs a target that exists, isn't itself and isn't another jump", () => {
    expect(about(withNode(j, "j", { target: undefined }), "j")).toEqual(["choose the step to jump to."]);
    expect(about(withNode(j, "j", { target: "nope" }), "j")).toEqual(["choose the step to jump to."]);
    expect(about(withNode(j, "j", { target: "j" }), "j")).toEqual(["a step can't jump to itself."]);
    const two = { ...j, nodes: { ...j.nodes, j2: { type: "jump", target: "e" } } };
    expect(about(withNode(two, "j", { target: "j2" }), "j")).toEqual(["jump to a step that isn't another jump."]);
    expect(removeNode(j, "e").nodes.j.target).toBeUndefined();
  });
});

// ---- the validation mirror for the new steps ----

describe("validation mirror", () => {
  const one = (node: JourneyNode, opts = {}) => {
    const graph: JourneyGraph = { trigger: { type: "event", event: "signup" }, entry: "n", nodes: { n: node, x: { type: "exit" } } };
    for (const [h] of outputs(node)) graph.nodes.n = { ...connect(graph, "n", h, "x").nodes.n };
    return about(graph, "n", opts);
  };

  it("checks decisions' time limits and their own settings", () => {
    expect(one({ type: "on_form", seconds: 3600 })).toEqual([]);
    expect(one({ type: "on_asset", seconds: 10 })).toEqual(["wait between 1 minute and 365 days."]);
    expect(one({ type: "on_page", seconds: 3600, url: "/x", page_id: 3 })).toEqual(["choose a URL or a landing page, not both."]);
    expect(one({ type: "on_page", seconds: 3600 })).toEqual([]);
    expect(one({ type: "on_device", seconds: 3600 })).toEqual(["choose a device type or an operating system."]);
    expect(one({ type: "on_device", seconds: 3600, os: "iOS" })).toEqual([]);
    expect(one({ type: "on_device", seconds: 3600, device_type: "phone" })).toEqual(["the device type is desktop, mobile or tablet."]);
    expect(one({ type: "on_dwc", seconds: 3600, slot: "Hero Banner" })).toEqual(["choose the dynamic content slot (lowercase letters, digits, - and _)."]);
    expect(one({ type: "on_dwc", seconds: 3600, slot: "hero-banner" })).toEqual([]);
    expect(one({ type: "wait_for_event", seconds: 31536001, event: "purchase" })).toEqual(["wait between 1 second and 365 days."]);
    expect(one({ type: "wait_for_event", seconds: 4, event: "purchase" })).toEqual([]); // as the server allows
  });

  it("checks conditions", () => {
    expect(one({ type: "if_journey", journey_ids: [], journey_state: "active" })).toEqual(["choose at least one journey."]);
    expect(one({ type: "if_journey", journey_ids: [7], journey_state: "active" }, { journeyId: 7 })).toEqual(["check other journeys, not this one."]);
    expect(one({ type: "if_journey", journey_ids: [8], journey_state: "completed" }, { journeyId: 7 })).toEqual([]);
    expect(one({ type: "if_form_field", field: "city", op: "eq", value: "" })).toEqual(["enter the value to compare with."]);
    expect(one({ type: "if_form_field", field: "city", op: "exists" })).toEqual([]);
    expect(one({ type: "if_form_field", field: "", op: "like", value: "x" })).toEqual(["choose the form field to check.", "choose how to compare the field."]);
    expect(one({ type: "if_notification", notify: "any" })).toEqual([]);
    expect(one({ type: "if_valid_email", reject_disposable: true })).toEqual([]);
    expect(one({ type: "if_dnc", channel: "fax" })).toEqual(["choose the channel to check."]);
    expect(one({ type: "if_dnc", channel: "any" })).toEqual([]);
    expect(one({ type: "if_before" })).toEqual(["choose the date after which contacts take No."]);
    expect(one({ type: "if_before", until: "2026-12-01T09:00:00Z" })).toEqual([]);
    expect(one({ type: "if_visited", days: 400 })).toEqual(["choose how far back to look (0 for ever, or 1 to 365 days)."]);
    expect(one({ type: "if_visited", days: 0, url: "" })).toEqual([]);
    expect(one({ type: "check", preset: "tags" })).toEqual(['describe who should take the "Yes" path.']);
  });

  it("checks the new actions", () => {
    expect(one({ type: "notify_user" })).toEqual(["choose the email template.", "choose who gets the email."]);
    expect(one({ type: "notify_user", template_id: 1, emails: ["nope"] })).toEqual(["check the email addresses."]);
    expect(one({ type: "notify_user", template_id: 1, to_owner: true })).toEqual([]);
    expect(one({ type: "send_message", channels: [{ channel: "email", template_id: 1 }, { channel: "email", template_id: 2 }] })).toEqual(["use each channel once."]);
    expect(one({ type: "send_message", channels: [{ channel: "push" }, { channel: "email", template_id: 2 }] })).toEqual(["choose a template for every channel."]);
    expect(one({ type: "send_message", channels: [] })).toEqual(["choose 1 to 6 channels."]);
    expect(one({ type: "show_focus" })).toEqual(["choose the focus item to show."]);
    expect(one({ type: "push_dwc", slot: "hero", html: " " })).toEqual(["enter the content to show."]);
    expect(one({ type: "push_dwc", slot: "hero", html: "x".repeat(65 * 1024) })).toEqual(["the content can be at most 64 KB."]);
    expect(one({ type: "push_dwc", slot: "hero", html: "<b>Hi</b>", days: 30 })).toEqual([]);
    expect(one({ type: "update_owner" })).toEqual(["choose the new owner, or to unassign."]);
    expect(one({ type: "update_owner", owner_id: 0 })).toEqual([]);
    expect(one({ type: "update_company", company_attrs: { name: "x" } })).toEqual(["company fields use letters, digits and _, and can't be id or name."]);
    expect(one({ type: "update_company", company_attrs: { tier: "gold", seats: 5, paid: true } })).toEqual([]);
    expect(one({ type: "add_company" })).toEqual(["choose the company."]);
    expect(one({ type: "company_points", points: 0 })).toEqual(["enter a whole number of points other than 0 (up to 100,000 either way)."]);
    expect(one({ type: "company_points", points: -5 })).toEqual([]);
    expect(one({ type: "update_list", add_lists: [1], remove_lists: [1] })).toEqual(["a static segment can't be both added to and removed from."]);
    expect(one({ type: "update_list", add_lists: [] })).toEqual(["choose at least one static segment to add to or remove from."]);
    expect(one({ type: "update_list", list_id: 3, list_action: "add" })).toEqual([]); // older steps
    expect(one({ type: "change_journey", add_journeys: [2], remove_journeys: [2] })).toEqual(["a journey can't be both added and removed."]);
    expect(one({ type: "change_journey", add_journeys: [7] }, { journeyId: 7 })).toEqual(["choose other journeys, not this one."]);
    expect(one({ type: "dnc_add", channel: "all", reason: "asked" })).toEqual([]);
    expect(one({ type: "dnc_remove", channel: "" })).toEqual(["choose the channel."]);
    expect(one({ type: "delete_contact" })).toEqual(["type DELETE to confirm it permanently deletes contacts."]);
    expect(one({ type: "delete_contact", confirm: "delete" })).toEqual(["type DELETE to confirm it permanently deletes contacts."]);
    expect(one({ type: "delete_contact", confirm: "DELETE" })).toEqual([]);
  });
});

describe("overlapping", () => {
  it("spots cards that cover each other, and tidy separates them", () => {
    const g: JourneyGraph = {
      trigger: { type: "event", event: "signup" }, entry: "a",
      nodes: { a: { type: "wait", seconds: 60, next: "b", x: 0, y: 210 }, b: { type: "exit", x: 100, y: 240 } },
    };
    expect(overlapping(g)).toBe(true);
    expect(overlapping(tidy(g))).toBe(false);
  });
});
