// Publish / unpublish pieces shared by the segment pages, the campaign hub and the journey editor
// (logic in publishing.ts).
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "./api";
import { ask } from "./guard";
import { SEGMENT_STATE_TONE, segmentState, segmentStateHint, segmentStateText, usageLines, type Publishable, type SegmentUse } from "./publishing";
import { ErrorNote, Modal, useToast } from "./ui";
import "./pages/segments.css"; // .seg-kind choice cards

/** SegmentStateBadge: Live, Starts 3 Dec…, Ended or Unpublished, with what that means on hover. */
export function SegmentStateBadge({ seg }: { seg: Publishable }) {
  return <span className={`badge ${SEGMENT_STATE_TONE[segmentState(seg)]}`} title={segmentStateHint(seg)}>{segmentStateText(seg)}</span>;
}

/** confirmUnpublishSegment asks before unpublishing, naming what uses the segment now. */
export async function confirmUnpublishSegment(id: number): Promise<boolean> {
  let uses: SegmentUse[] = [];
  try {
    uses = (await api.get<{ uses: SegmentUse[] }>(`/v1/segments/${id}/usage`)).uses;
  } catch {
    // The prompt still goes up; it just can't list what uses the segment.
  }
  const lines = usageLines(uses);
  return ask({
    title: "Unpublish this segment?",
    body: (
      <>
        <p>Pulse stops updating who's in it: its contacts stay as they are now, and new campaigns can't send to it. You can publish it again any time, and it catches up then.</p>
        {lines.length > 0 && <>
          <p style={{ marginTop: 10 }}><strong>In use now</strong> (these keep running with the contacts it has now):</p>
          <ul className="small">{lines.slice(0, 8).map((l) => <li key={l}>{l}</li>)}</ul>
          {lines.length > 8 && <p className="xs muted">…and {lines.length - 8} more</p>}
        </>}
      </>
    ),
    confirmText: "Unpublish",
  });
}

/** UnpublishJourneyModal: stop a live campaign, letting the people inside finish (nobody new enters)
 * or holding them where they are (its steps can be changed then). */
export function UnpublishJourneyModal({ id, name, inside, onClose, onDone }: {
  id: number; name: string; inside: number; onClose: () => void; onDone?: (people: "finish" | "hold") => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [people, setPeople] = useState<"finish" | "hold">("finish");
  const go = useMutation({
    mutationFn: () => api.post(`/v1/journeys/${id}/unpublish`, { people }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["journeys"] });
      qc.invalidateQueries({ queryKey: ["journey", String(id)] });
      qc.invalidateQueries({ queryKey: ["campaigns"] });
      toast({ text: people === "finish" ? `Unpublished ${name}: nobody new enters, people inside finish` : `Unpublished ${name}: everyone inside waits where they are` });
      onDone?.(people);
      onClose();
    },
  });
  const n = inside.toLocaleString("en-IN");
  return (
    <Modal title={`Unpublish ${name}?`} onClose={onClose} guard={false} footer={<>
      <button className="btn" onClick={onClose}>Keep it live</button>
      <button className="btn primary" disabled={go.isPending} onClick={() => go.mutate()}>Unpublish</button>
    </>}>
      <p className="small" style={{ marginBottom: 12 }}>Nobody new enters from now on. {inside > 0 ? `What happens to the ${n} ${inside === 1 ? "person" : "people"} in it now?` : "Nobody is in it right now."}</p>
      <div className="seg-kind" role="radiogroup" aria-label="People in it now">
        <button role="radio" aria-checked={people === "finish"} className={people === "finish" ? "on" : ""} onClick={() => setPeople("finish")}>
          <strong>Let them finish</strong>
          <span>They go on through the remaining steps and messages. Its publish window ends now; set a later end date to let people in again.</span>
        </button>
        <button role="radio" aria-checked={people === "hold"} className={people === "hold" ? "on" : ""} onClick={() => setPeople("hold")}>
          <strong>Hold them where they are</strong>
          <span>Nothing more is sent until you publish it again. Its steps can be changed meanwhile.</span>
        </button>
      </div>
      <ErrorNote error={go.error} />
    </Modal>
  );
}
