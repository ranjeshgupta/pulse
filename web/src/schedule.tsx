// Schedule fields shared by journeys, broadcasts and on-site content: start is required, end is
// optional, and an empty end means it runs until stopped.
import { Field } from "./ui";

// datetime-local inputs work in the browser's local time; the API speaks ISO 8601 (UTC).
export function toLocalInput(iso?: string | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function fromLocalInput(v: string): string | undefined {
  return v ? new Date(v).toISOString() : undefined;
}

export const nowISO = () => {
  const d = new Date();
  d.setSeconds(0, 0);
  return d.toISOString();
};

export function scheduleProblem(start?: string, end?: string | null): string | null {
  if (!start) return "Choose when it starts.";
  if (end && new Date(end) <= new Date(start)) return "The end must be after the start.";
  return null;
}

export function describeWindow(start?: string, end?: string | null): string {
  if (!start) return "";
  const fmt = (s: string) => new Date(s).toLocaleString("en-IN", { day: "numeric", month: "short", year: "numeric", hour: "numeric", minute: "2-digit" });
  const now = Date.now();
  const s = new Date(start).getTime();
  if (end && new Date(end).getTime() <= now) return `Ended ${fmt(end)}`;
  const from = s > now ? `Starts ${fmt(start)}` : `From ${fmt(start)}`;
  return end ? `${from}, ends ${fmt(end)}` : `${from}, runs until you stop it`;
}

/** describeSend: when a single send goes out. */
export function describeSend(start?: string): string {
  if (!start) return "";
  const at = new Date(start).toLocaleString("en-IN", { day: "numeric", month: "short", year: "numeric", hour: "numeric", minute: "2-digit" });
  return new Date(start).getTime() > Date.now() ? `Sends ${at}` : "The time has passed, so scheduling sends it right away.";
}

/** ScheduleFields: start and end side by side; stacked puts them one under the other (narrow panels
 * such as the journey inspector). */
export function ScheduleFields({ start, end, onChange, startLabel = "Starts", disabled, stacked, once }: {
  start?: string; end?: string | null; onChange: (v: { start_at?: string; end_at?: string }) => void; startLabel?: string; disabled?: boolean; stacked?: boolean;
  /** once: a single send, so there is no end to choose. */
  once?: boolean;
}) {
  const problem = scheduleProblem(start, end);
  return (
    <fieldset disabled={disabled} style={{ border: 0, padding: 0, margin: 0 }}>
      <div className={stacked || once ? undefined : "grid-2"}>
        <Field label={`${startLabel} (required)`}>
          <input className="input" type="datetime-local" required value={toLocalInput(start)} onChange={(e) => onChange({ start_at: fromLocalInput(e.target.value), end_at: end ?? undefined })} />
        </Field>
        {!once && <Field label="Ends (optional)" hint="Leave empty to keep it running until you stop it.">
          <div className="row" style={{ flexWrap: "nowrap" }}>
            <input className="input" style={{ minWidth: 0 }} type="datetime-local" value={toLocalInput(end)} onChange={(e) => onChange({ start_at: start, end_at: fromLocalInput(e.target.value) })} />
            {end && <button type="button" className="btn ghost sm" onClick={() => onChange({ start_at: start, end_at: undefined })}>Clear</button>}
          </div>
        </Field>}
      </div>
      <p className="xs" style={{ marginTop: -6, marginBottom: 12, color: problem ? "var(--danger)" : "var(--muted)" }}>{problem ?? (once ? describeSend(start) : describeWindow(start, end))}</p>
    </fieldset>
  );
}
