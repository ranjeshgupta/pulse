// Server-renders every inspector, canvas card and the chooser (no DOM needed) to catch render-time errors.
import { describe, expect, it } from "vitest";
import { renderToString } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { ReactFlowProvider, type NodeProps } from "@xyflow/react";
import { JOURNEY_CATALOG } from "../../journeyCatalog";
import { insertStep, TRIGGER_ID, stepNumbers, emptyGraph } from "../../journeyGraph";
import { Inspector } from "./Inspector";
import { StepChooser } from "./StepChooser";
import { CanvasCtx, PulseNode, triggerText } from "./Canvas";
import { summary } from "./nodeText";

const wrap = (el: React.ReactNode) => renderToString(
  <QueryClientProvider client={new QueryClient()}><MemoryRouter><ReactFlowProvider>{el}</ReactFlowProvider></MemoryRouter></QueryClientProvider>);
const pickers = { lists: { forms: [], assets: [], pages: [], users: [{ id: 1, name: "Asha" }], companies: [], journeys: [{ id: 2, name: "J" }], templates: [], focus: [], slots: [], stages: ["MQL"], segments: [], staticLists: [], integrations: [] }, names: {} } as never;

describe("smoke", () => {
  it("renders every inspector and node card", () => {
    let g = emptyGraph();
    g = insertStep(g, { source: TRIGGER_ID, handle: "next" }, { type: "send_email", template_id: 1 })!.graph;
    for (const it of JOURNEY_CATALOG) {
      const r = insertStep(g, { source: "email1", handle: "next" }, it.make(g, { source: "email1", handle: "next" })) ?? insertStep(g, { source: "email1", handle: "next" }, { type: "wait", seconds: 60 })!;
      const id = r.id;
      const numbers = stepNumbers(r.graph);
      const html = wrap(<Inspector graph={r.graph} setGraph={() => {}} win={{}} setWin={() => {}} selected={id} locked={false} decisions={{}} pickers={pickers} templates={[]} numbers={numbers} canEditContacts onDelete={() => {}} onDeleteBranch={() => {}} />);
      expect(html.length).toBeGreaterThan(50);
      const card = wrap(<CanvasCtx.Provider value={{ graph: r.graph, names: {}, numbers, locked: false, counts: {}, showStats: true, onAdd: () => {}, triggerText: "x" }}>
        <PulseNode {...({ id, data: { id, node: r.graph.nodes[id] }, selected: false } as unknown as NodeProps)} />
      </CanvasCtx.Provider>);
      expect(card).toContain("jnode");
      expect(summary(r.graph, id).length).toBeGreaterThan(3);
    }
    expect(wrap(<Inspector graph={g} setGraph={() => {}} win={{}} setWin={() => {}} selected={TRIGGER_ID} locked={false} decisions={{}} pickers={pickers} templates={[]} numbers={{}} canEditContacts onDelete={() => {}} onDeleteBranch={() => {}} />)).toContain("Start");
  });
  it("renders the chooser", () => {
    const html = wrap(<StepChooser graph={emptyGraph()} at={{ source: TRIGGER_ID, handle: "next" }} where="After Start" ctx={{ canEditContacts: true }} onPick={() => {}} onClose={() => {}} />);
    for (const l of ["Action", "Decision", "Condition", "Wait", "Split"]) expect(html).toContain(`aria-label="${l}"`);
  });
  it("draws nothing for a card whose step is no longer in the graph", () => {
    // The placeholder graph's Exit card renders once more after a journey without an Exit loads.
    const g = { trigger: { type: "event" as const, event: "x" }, entry: "d", nodes: { d: { type: "delete_contact", confirm: "DELETE" } } };
    const card = wrap(<CanvasCtx.Provider value={{ graph: g, names: {}, numbers: stepNumbers(g), locked: false, counts: {}, showStats: true, onAdd: () => {}, triggerText: "x" }}>
      <PulseNode {...({ id: "exit", data: { id: "exit", node: { type: "exit" } }, selected: false } as unknown as NodeProps)} />
    </CanvasCtx.Provider>);
    expect(card).not.toContain("jnode");
  });
});

describe("form trigger", () => {
  it("describes the trigger on the Start card", () => {
    const g = emptyGraph();
    expect(triggerText({ ...g, trigger: { type: "form_submitted" } }, {})).toBe("Contact submits any form");
    expect(triggerText({ ...g, trigger: { type: "form_submitted", form_id: 3 } }, {}, { 3: "Newsletter" })).toBe("Contact submits ‘Newsletter’");
  });
});
