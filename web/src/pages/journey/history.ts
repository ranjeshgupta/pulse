// Undo / redo for the journey builder: a capped history of graph states. Edits that share a key
// within a second (typing in one inspector field, one drag) merge into one step.
export const HISTORY_CAP = 100;

export type History<T> = { past: T[]; present: T; future: T[]; key?: string; at?: number };

export const startHistory = <T,>(present: T): History<T> => ({ past: [], present, future: [] });

/** record makes next the present state; the old present becomes undoable unless it merges. */
export function record<T>(h: History<T>, next: T, key?: string, now = Date.now()): History<T> {
  if (next === h.present) return h;
  if (key && h.key === key && h.at !== undefined && now - h.at < 1000) return { ...h, present: next, at: now };
  return { past: [...h.past, h.present].slice(-HISTORY_CAP), present: next, future: [], key, at: now };
}

export function undo<T>(h: History<T>): History<T> {
  if (h.past.length === 0) return h;
  return { past: h.past.slice(0, -1), present: h.past[h.past.length - 1], future: [h.present, ...h.future] };
}

export function redo<T>(h: History<T>): History<T> {
  if (h.future.length === 0) return h;
  return { past: [...h.past, h.present], present: h.future[0], future: h.future.slice(1) };
}
