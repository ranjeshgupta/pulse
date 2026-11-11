// "Why is this contact in this segment": turns the API's explanation tree into rows the contact
// page lists, one per group and filter, with what the contact has.
import type { Explanation } from "./api";
import { describe, type RefNames } from "./segmentRules";

export type ExplainRow = { depth: number; text: string; match: boolean; detail?: string; group?: boolean; sweep?: boolean };

const GROUP_WORDS = { all: "All of these", any: "Any of these", none: "None of these" } as const;

/** explainRows flattens an explanation, depth first. */
export function explainRows(e: Explanation, names: Record<number, string> = {}, refs: RefNames = {}, depth = 0): ExplainRow[] {
  if (e.kind === "all" || e.kind === "any" || e.kind === "none") {
    const kids = e.children ?? [];
    const matched = kids.filter((k) => k.match).length;
    const detail = e.kind === "none" ? `${matched} of ${kids.length} match` : `${matched} of ${kids.length} match`;
    return [
      { depth, text: GROUP_WORDS[e.kind], match: e.match, detail, group: true },
      ...kids.flatMap((k) => explainRows(k, names, refs, depth + 1)),
    ];
  }
  const text = e.rule ? describe(e.rule, names, refs) : e.kind;
  let detail: string | undefined;
  if (e.kind === "attr") detail = e.value ? `their value: ${e.value}` : "they have no value";
  if (e.kind === "event" && e.count !== undefined) detail = `${e.count} matching event${e.count === 1 ? "" : "s"}`;
  if (e.sweep) detail = "older than a year: checked by the periodic sweep";
  return [{ depth, text, match: e.match, detail, sweep: e.sweep }];
}

/** verdict words the overall answer, given the stored membership and the live evaluation. */
export function verdict(member: boolean, match: boolean | undefined, daily: boolean): string {
  if (match === undefined) return member ? "In this segment." : "Not in this segment.";
  if (member && match) return "In this segment: they match its rule now.";
  if (!member && !match) return "Not in this segment: they don't match its rule now.";
  if (daily) return member ? "In this segment since its last daily refresh; they no longer match, so they leave at the next one." : "Not in this segment yet: they match now and join at its next daily refresh.";
  return member ? "In this segment, though the rule no longer matches right now: it updates within moments (or at the next sweep)." : "They match the rule now and join in moments (or at the next sweep).";
}
