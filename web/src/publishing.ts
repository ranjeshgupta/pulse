// Publishing segments and campaigns, modelled on on-site content: published or not, and a window
// (start required, end optional). A segment outside it is frozen: its members stay as they were,
// Pulse stops recomputing it, and new sends can't use it. A live journey can be unpublished, either
// letting the people inside finish or holding them where they are.

export type SegmentState = "live" | "upcoming" | "ended" | "unpublished";

/** What the publish fields of a segment look like (the API's Segment has them). */
export interface Publishable { published?: boolean; start_at?: string; end_at?: string | null; state?: SegmentState }

const at = (s: string) => new Date(s).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" });

/** segmentState: the API's state, or worked out from the fields (older servers send none). */
export function segmentState(s: Publishable, now = Date.now()): SegmentState {
  if (s.state) return s.state;
  if (s.published === false) return "unpublished";
  if (s.start_at && new Date(s.start_at).getTime() > now) return "upcoming";
  if (s.end_at && new Date(s.end_at).getTime() <= now) return "ended";
  return "live";
}

/** The badge words: Live, Starts 3 Dec, 10:00 am, Ended, Unpublished. */
export function segmentStateText(s: Publishable, now = Date.now()): string {
  const st = segmentState(s, now);
  if (st === "upcoming" && s.start_at) return `Starts ${at(s.start_at)}`;
  return { live: "Live", upcoming: "Starts later", ended: "Ended", unpublished: "Unpublished" }[st];
}

export const SEGMENT_STATE_TONE: Record<SegmentState, string> = { live: "ok", upcoming: "info", ended: "warn", unpublished: "" };

/** segmentStateHint explains what the state means for the people in it and for sends. */
export function segmentStateHint(s: Publishable, now = Date.now()): string {
  switch (segmentState(s, now)) {
    case "unpublished": return "Unpublished: Pulse isn't updating who's in it (its contacts stay as they were) and new campaigns can't send to it. Publish it to bring it up to date.";
    case "ended": return `Its publish window ended${s.end_at ? ` ${at(s.end_at)}` : ""}: Pulse isn't updating who's in it and new campaigns can't send to it. Move the end later, or clear it, to bring it back.`;
    case "upcoming": return `It goes live ${s.start_at ? at(s.start_at) : "later"}: Pulse fills it then, from everything recorded so far.`;
    default: return s.end_at ? `Live until ${at(s.end_at)}.` : "Live: contacts join and leave as things happen.";
  }
}

/** pickerOption: how a segment shows in a picker for a new send. Unpublished and ended ones can't be
 * chosen (an already chosen one stays, flagged); an upcoming one can, it says when it starts. */
export function pickerOption(s: Publishable & { name: string }, now = Date.now()): { label: string; disabled: boolean; note?: string } {
  const st = segmentState(s, now);
  if (st === "live") return { label: s.name, disabled: false };
  if (st === "upcoming") return { label: `${s.name} (starts ${s.start_at ? at(s.start_at) : "later"})`, disabled: false, note: "starts later" };
  return { label: `${s.name} (${st})`, disabled: true, note: st };
}

/** chosenNote: "unpublished" or "ended" for a chosen segment that isn't live, so its chip says so. */
export function chosenNote(segments: (Publishable & { id: number; name: string })[], id: number, now = Date.now()): string | undefined {
  const sg = segments.find((s) => s.id === id);
  if (!sg) return undefined;
  const o = pickerOption(sg, now);
  return o.disabled ? o.note : undefined;
}

/** Where a journey stands: its status, with the window for a live one. */
export type JourneyState = "draft" | "paused" | "scheduled" | "active" | "ended";

export function journeyState(j: { status: string; start_at?: string; end_at?: string | null }, now = Date.now()): JourneyState {
  if (j.status !== "active") return j.status === "paused" ? "paused" : "draft";
  if (j.start_at && new Date(j.start_at).getTime() > now) return "scheduled";
  if (j.end_at && new Date(j.end_at).getTime() <= now) return "ended";
  return "active";
}

/** The status words for a journey (the Status badge's keys: active says Live). */
export const JOURNEY_STATE_TEXT: Record<JourneyState, string> = {
  draft: "Draft", paused: "Paused", scheduled: "Scheduled", active: "Live", ended: "Ended",
};

/** What uses a segment now (GET /v1/segments/{id}/usage). */
export interface SegmentUse { kind: "segment" | "journey" | "broadcast" | "focus" | "content" | "inapp"; id: number; name: string; status?: string }

const USE_KIND: Record<SegmentUse["kind"], string> = {
  segment: "Segment", journey: "Campaign (journey)", broadcast: "One-time campaign", focus: "Website message", content: "Content slot", inapp: "In-app message",
};

export const usageHref = (u: SegmentUse) => ({
  segment: `/segments/${u.id}`, journey: `/journeys/${u.id}`, broadcast: `/broadcasts/${u.id}`,
  focus: `/on-site/focus/${u.id}`, content: `/on-site/content/${u.id}`, inapp: `/on-site/in-app/${u.id}`,
})[u.kind];

/** usageLines: "Campaign (journey): Welcome series" for each use, for the Unpublish prompt. */
export const usageLines = (uses: SegmentUse[]) => uses.map((u) => `${USE_KIND[u.kind]}: ${u.name}`);

/** windowProblem: the start is required and the end, if any, comes after it. */
export function windowProblem(start?: string, end?: string | null): string | null {
  if (!start) return "Choose when it starts.";
  if (end && new Date(end) <= new Date(start)) return "The end must be after the start.";
  return null;
}

/** sameWindow: whether two windows are the same, to tell an edit from what's saved. */
export const sameWindow = (a: { start_at?: string; end_at?: string | null }, b: { start_at?: string; end_at?: string | null }) =>
  (a.start_at ? new Date(a.start_at).getTime() : 0) === (b.start_at ? new Date(b.start_at).getTime() : 0)
  && (a.end_at ? new Date(a.end_at).getTime() : 0) === (b.end_at ? new Date(b.end_at).getTime() : 0);
