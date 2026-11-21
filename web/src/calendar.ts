// Marketing calendar (O6): pure helpers for the month grid.
export interface CalendarItem {
  kind: "broadcast" | "journey"; id: number; name: string; channel?: string; status: string; tags?: string[];
  sends?: string[]; start?: string; end?: string | null;
}

const pad = (n: number) => String(n).padStart(2, "0");
export const ymdLocal = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

/** monthGrid is the weeks (Monday first) covering a month: 5 or 6 rows of 7 days. */
export function monthGrid(year: number, month: number): Date[][] {
  const first = new Date(year, month, 1);
  const offset = (first.getDay() + 6) % 7; // Monday = 0
  const start = new Date(year, month, 1 - offset);
  const weeks: Date[][] = [];
  for (let w = 0; w < 6; w++) {
    const row = Array.from({ length: 7 }, (_, i) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + w * 7 + i));
    if (w >= 4 && row[0].getMonth() !== month) break;
    weeks.push(row);
  }
  return weeks;
}

/** sendsByDay groups one-time campaign sends by local day. */
export function sendsByDay(items: CalendarItem[]): Map<string, { item: CalendarItem; at: Date }[]> {
  const out = new Map<string, { item: CalendarItem; at: Date }[]>();
  for (const it of items) {
    for (const s of it.sends ?? []) {
      const at = new Date(s);
      const k = ymdLocal(at);
      out.set(k, [...(out.get(k) ?? []), { item: it, at }]);
    }
  }
  for (const list of out.values()) list.sort((a, b) => a.at.getTime() - b.at.getTime());
  return out;
}

/** BUSY: sends in one day that may be too many for the same people. */
export const BUSY = 3;
