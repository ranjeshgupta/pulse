import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, type Stage } from "../api";
import { describeThreshold, moveItem } from "../accounts";
import { ask } from "../guard";
import { Empty, ErrorNote, Field, Modal, PageHead, useToast } from "../ui";
import "./stages.css";
import { ContactCount } from "../ContactCount";

/** The workspace's lifecycle stages, in order, with how many contacts are in each. */
export function useStages(poll = 0) {
  return useQuery({
    queryKey: ["stages"],
    queryFn: () => api.get<{ stages: Stage[] }>("/v1/stages"),
    select: (d) => d.stages,
    staleTime: poll ? 0 : 30_000,
    refetchInterval: poll || false,
    retry: false,
  });
}

// Stages: a contact's lifecycle (Lead → MQL → SQL → Customer). A contact is in one stage at a time;
// it changes by hand, from a journey step, through the API, or when points reach a stage's threshold.
export function Stages() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useStages(4000);
  const stages = q.data ?? [];
  const [editing, setEditing] = useState<Stage | "new" | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["stages"] });
  const reorder = useMutation({
    mutationFn: (ids: number[]) => api.put("/v1/stages/order", { ids }),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: (s: Stage) => api.del(`/v1/stages/${s.id}`),
    onSuccess: (_, s) => { refresh(); toast({ text: `Removed ${s.name}` }); },
  });
  const move = (i: number, d: -1 | 1) => reorder.mutate(moveItem(stages.map((s) => s.id), i, d));
  return (
    <div className="page">
      <PageHead
        title="Stages"
        intro="The lifecycle a contact moves through, from first touch to customer. Each contact is in one stage. Move them by hand, with a campaign's “Change contact's stage” step, through the API, or automatically when their points reach a stage's threshold. Segments can select contacts by stage."
        actions={<button className="btn primary" onClick={() => setEditing("new")}>New stage</button>}
      />
      <ErrorNote error={q.error ?? reorder.error ?? remove.error} />
      <div className="panel">
        {stages.length === 0 && !q.isLoading ? (
          <Empty title="No stages yet" action={<button className="btn primary" onClick={() => setEditing("new")}>Add the first stage</button>}>
            A common start: Lead, Marketing qualified (at 50 points), Sales qualified (at 100 points), Customer.
          </Empty>
        ) : (
          <table className="table stages-table">
            <thead><tr><th className="stage-order">Order</th><th>Stage</th><th>Contacts move here</th><th className="num">Contacts</th><th /></tr></thead>
            <tbody>
              {stages.map((s, i) => (
                <tr key={s.id}>
                  <td className="stage-order">
                    <span className="stage-step">{i + 1}</span>
                    <button className="btn ghost sm" aria-label={`Move ${s.name} up`} disabled={i === 0 || reorder.isPending} onClick={() => move(i, -1)}>↑</button>
                    <button className="btn ghost sm" aria-label={`Move ${s.name} down`} disabled={i === stages.length - 1 || reorder.isPending} onClick={() => move(i, 1)}>↓</button>
                  </td>
                  <td>
                    <strong>{s.name}</strong>
                    {s.description && <div className="xs muted">{s.description}</div>}
                  </td>
                  <td className="small">{describeThreshold(s.min_points)}</td>
                  <td className="num"><ContactCount live value={s.contacts} filters={[["stage", s.name]]} /></td>
                  <td className="num">
                    <button className="btn ghost sm" onClick={() => setEditing(s)}>Edit</button>
                    <button className="btn ghost sm danger" disabled={remove.isPending} onClick={() => void ask({
                      title: `Remove the stage ${s.name}?`,
                      body: `Its ${s.contacts} ${s.contacts === 1 ? "contact keeps" : "contacts keep"} "${s.name}" as their stage until they move.`,
                      confirmText: "Remove stage", danger: true,
                    }).then((ok) => ok && remove.mutate(s))}>Remove</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      <p className="xs muted" style={{ marginTop: 10 }}>
        Points thresholds only move contacts forward: when a points change reaches a threshold, the contact moves to the furthest stage whose threshold they reach, never back to an earlier one.
      </p>
      {editing && <StageModal stage={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} onSaved={refresh} />}
    </div>
  );
}

function StageModal({ stage, onClose, onSaved }: { stage?: Stage; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [name, setName] = useState(stage?.name ?? "");
  const [description, setDescription] = useState(stage?.description ?? "");
  const [threshold, setThreshold] = useState(stage?.min_points != null ? String(stage.min_points) : "");
  const n = Number(threshold);
  const thresholdOK = threshold.trim() === "" || (Number.isInteger(n) && n >= 0);
  const ok = name.trim() !== "" && name.trim().length <= 64 && thresholdOK;
  const save = useMutation({
    mutationFn: () => {
      const body = { name: name.trim(), description: description.trim(), min_points: threshold.trim() === "" ? null : n };
      return stage ? api.put(`/v1/stages/${stage.id}`, body) : api.post("/v1/stages", body);
    },
    onSuccess: () => {
      onSaved();
      toast({ text: stage && stage.name !== name.trim() ? `Renamed to ${name.trim()}; its contacts follow within seconds` : `Saved ${name.trim()}` });
      onClose();
    },
  });
  return (
    <Modal title={stage ? `Edit ${stage.name}` : "New stage"} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={!ok || save.isPending} onClick={() => save.mutate()}>{stage ? "Save" : "Add stage"}</button>
    </>}>
      <Field label="Name" hint={stage ? "Renaming moves every contact in this stage to the new name." : "New stages go after the last one; reorder them in the list."}>
        <input className="input" autoFocus value={name} maxLength={64} onChange={(e) => setName(e.target.value)} placeholder="Marketing qualified" />
      </Field>
      <Field label="Description (optional)">
        <input className="input" value={description} maxLength={500} onChange={(e) => setDescription(e.target.value)} placeholder="Engaged enough for sales to call" />
      </Field>
      <Field label="Move contacts here at (points, optional)" hint="Leave empty to set this stage only by hand, from campaigns or through the API.">
        <input className="input" style={{ maxWidth: 160 }} inputMode="numeric" value={threshold} onChange={(e) => setThreshold(e.target.value)} placeholder="50" />
      </Field>
      {!thresholdOK && <p className="small" style={{ color: "var(--danger)" }}>Enter a whole number of points from 0.</p>}
      <ErrorNote error={save.error} />
    </Modal>
  );
}
