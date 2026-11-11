import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, type LiveEvent, type LiveFeed as Feed } from "../api";
import { useDebounced } from "../insights";
import {
  announcement, clockTime, contactHref, eventKey, isRateLimited, liveQuery, MAX_ROWS, mergeEvents, nameProblem, normalizedName, propsJSON,
  propsPreview, retryDelay, RATE_LIMIT_BACKOFF_MS,
} from "../liveFeed";
import { ErrorNote, PageHead } from "../ui";
import "./live.css";

/** How often, at most, the polite live region speaks. */
const ANNOUNCE_MS = 15_000;

function useTabVisible(): boolean {
  const [visible, setVisible] = useState(() => typeof document === "undefined" || document.visibilityState !== "hidden");
  useEffect(() => {
    const h = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", h);
    return () => document.removeEventListener("visibilitychange", h);
  }, []);
  return visible;
}

export function LiveFeed() {
  const [paused, setPaused] = useState(false);
  const [name, setName] = useState("");
  const [contact, setContact] = useState("");
  const [events, setEvents] = useState<LiveEvent[]>([]);
  const [maxPerSecond, setMaxPerSecond] = useState<number | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [limited, setLimited] = useState(false);
  const [started, setStarted] = useState(false);
  const [said, setSaid] = useState("");
  const visible = useTabVisible();

  const nameIssue = nameProblem(name);
  const filters = useDebounced(JSON.stringify({ name: nameIssue ? null : normalizedName(name), contact: contact.trim() }), 400);
  const cursor = useRef("");
  const filterKey = useRef(filters);
  const pending = useRef(0);
  const lastSaid = useRef(0);
  const list = useRef<LiveEvent[]>([]);
  const show = (next: LiveEvent[]) => { list.current = next; setEvents(next); };

  // Changing filters starts over from the latest events.
  useEffect(() => {
    if (filterKey.current === filters) return;
    filterKey.current = filters;
    cursor.current = "";
    pending.current = 0;
    show([]);
    setStarted(false);
  }, [filters]);

  const parsed = JSON.parse(filters) as { name: string | null; contact: string };
  const blocked = parsed.name === null;
  const running = !paused && visible && !blocked;

  useEffect(() => {
    if (!running) return;
    const ctl = new AbortController();
    let timer: number | undefined;
    const f = { name: parsed.name ?? "", contact: parsed.contact };
    const tick = async () => {
      let delay: number;
      try {
        const feed = await api.get<Feed>(`/v1/live/events?${liveQuery(cursor.current, f)}`);
        if (ctl.signal.aborted) return;
        const first = !cursor.current;
        if (feed.cursor) cursor.current = feed.cursor;
        setMaxPerSecond(feed.max_per_second);
        setError(null);
        setLimited(false);
        setStarted(true);
        const m = mergeEvents(list.current, feed.events ?? []);
        if (m.added > 0) show(m.events);
        if (!first) pending.current += m.added;
        const now = Date.now();
        if (pending.current > 0 && now - lastSaid.current >= ANNOUNCE_MS) {
          setSaid(announcement(pending.current));
          pending.current = 0;
          lastSaid.current = now;
        }
        delay = retryDelay(null);
      } catch (e) {
        if (ctl.signal.aborted) return;
        delay = retryDelay(e);
        if (isRateLimited(e)) { setLimited(true); setError(null); } else { setLimited(false); setError(e); }
      }
      if (!ctl.signal.aborted) timer = window.setTimeout(tick, delay);
    };
    tick();
    return () => { ctl.abort(); window.clearTimeout(timer); };
  }, [running, filters]); // parsed is derived from filters

  const status = blocked ? "Fix the event name filter to see events." : paused ? "Paused" : !visible ? "Paused while this tab is hidden" : limited ? "Slowing down" : "Live";

  return (
    <div className="page">
      <PageHead
        title="Live events"
        intro="Events as they arrive from your apps, website and integrations. Use it to check that tracking works."
        actions={
          <>
            <button type="button" className={`btn${paused ? " primary" : ""}`} aria-pressed={paused} onClick={() => setPaused(!paused)}>
              {paused ? "Resume" : "Pause"}
            </button>
            <button type="button" className="btn ghost" onClick={() => { show([]); pending.current = 0; }} disabled={events.length === 0}>Clear</button>
          </>
        }
      />

      <section className="panel live-controls">
        <div className="panel-body live-controls-row">
          <label className="field live-field">
            <span>Event name</span>
            <input
              className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="order_placed or order*"
              aria-invalid={!!nameIssue} aria-describedby="live-name-hint" spellCheck={false} autoComplete="off"
            />
            <small id="live-name-hint" className={nameIssue ? "live-problem" : undefined}>{nameIssue ?? "End with * to match every name that starts with it."}</small>
          </label>
          <label className="field live-field">
            <span>Contact</span>
            <input className="input" value={contact} onChange={(e) => setContact(e.target.value)} placeholder="External id contains…" spellCheck={false} autoComplete="off" />
            <small>Part of the contact's external id.</small>
          </label>
          <div className="live-status">
            <span className={`live-dot${running && !limited ? " on" : ""}`} aria-hidden="true" />
            <span className="small">{status}</span>
            <span className="xs muted">{events.length.toLocaleString("en-IN")} shown{events.length >= MAX_ROWS ? ` (latest ${MAX_ROWS})` : ""}</span>
          </div>
        </div>
      </section>

      <p className="xs muted live-note">
        Shows up to {maxPerSecond ?? "a limited number of"} events a second per workspace; personal properties are hidden.
      </p>
      {limited && <div className="alert info live-limited" role="status">Checking for events too often. Trying again in {RATE_LIMIT_BACKOFF_MS / 1000} seconds.</div>}
      <ErrorNote error={error} />
      <div className="live-sr" aria-live="polite" aria-atomic="true">{said}</div>

      <section className="panel live-panel">
        {events.length === 0 ? (
          <div className="empty">
            <h3>{!started && running ? "Waiting for events…" : "No events yet"}</h3>
            <p>
              {parsed.name || parsed.contact
                ? "Nothing matching these filters has arrived. New events show up here as they come in."
                : "New events show up here as they come in. Send one from your website or app with the Pulse SDK, or through the API."}
            </p>
          </div>
        ) : (
          <ol className="live-list" aria-label="Events, newest first">
            {events.map((e) => <EventRow key={eventKey(e)} e={e} />)}
          </ol>
        )}
      </section>
    </div>
  );
}

function EventRow({ e }: { e: LiveEvent }) {
  const [open, setOpen] = useState(false);
  const hasProps = !!e.props && Object.keys(e.props).length > 0;
  const id = `live-props-${eventKey(e)}`;
  return (
    <li className={`live-row${open ? " open" : ""}`}>
      <time className="live-time" dateTime={e.ts} title={new Date(e.ts).toLocaleString("en-IN")}>{clockTime(e.ts)}</time>
      <span className="badge info live-name">{e.name}</span>
      <span className="live-contact">
        {e.external_id ? <Link to={contactHref(e.external_id)}>{e.external_id}</Link> : <span className="muted">Anonymous</span>}
      </span>
      {hasProps ? (
        <button type="button" className="live-props" aria-expanded={open} aria-controls={id} onClick={() => setOpen(!open)} title={open ? "Hide properties" : "Show properties"}>
          {propsPreview(e.props, e.truncated)}
        </button>
      ) : (
        <span className="live-props none">{propsPreview(e.props, e.truncated)}</span>
      )}
      {open && hasProps && (
        <pre className="live-json" id={id}>{propsJSON(e.props)}{e.truncated ? "\n// cut short: the event was too large to send whole" : ""}</pre>
      )}
    </li>
  );
}
