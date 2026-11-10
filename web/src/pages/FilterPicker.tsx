// FilterPicker: the segment builder's "Add filter" list. One search box over every filter, grouped
// (Contact, Company, Location, Device, Consent, Behaviour, Points & stage, Segments & tags) with a
// group rail, recently used filters first, icons and a one-line hint each. Keyboard: the search box
// is a combobox; arrows move, Enter picks, Esc closes.
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { GROUPS, groupItems, searchCatalog, typeWord, type CatalogItem, type GroupId, type IconName } from "../segmentCatalog";

const PATHS: Record<IconName, string> = {
  user: "M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zm-7 8a7 7 0 0 1 14 0",
  building: "M4 21V5l8-3v19M12 9h8v12M7 8h2M7 12h2M7 16h2M15 13h2M15 17h2M2 21h20",
  pin: "M12 21s-7-6.2-7-11.5A7 7 0 0 1 19 9.5C19 14.8 12 21 12 21zm0-9a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z",
  device: "M7 2h10a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zM11 18h2",
  shield: "M12 3l8 3v6c0 4.5-3.4 8-8 9-4.6-1-8-4.5-8-9V6l8-3zm-3 9l2 2 4-4",
  mail: "M3 5h18v14H3zM3 6l9 7 9-7",
  chat: "M4 4h16v12H8l-4 4zM8 9h8M8 12h5",
  bell: "M6 16V11a6 6 0 1 1 12 0v5l2 2H4zM10 20a2 2 0 0 0 4 0",
  globe: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM3 12h18M12 3c2.5 2.5 3.5 5.5 3.5 9s-1 6.5-3.5 9c-2.5-2.5-3.5-5.5-3.5-9s1-6.5 3.5-9z",
  form: "M5 3h14v18H5zM8 7h8M8 11h8M8 15h5",
  download: "M12 3v12M7 10l5 5 5-5M4 20h16",
  route: "M6 19a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM18 9a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM6 15V9a4 4 0 0 1 4-4h6M18 9v6a4 4 0 0 1-4 4H8",
  star: "M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9z",
  tag: "M3 12V4h8l10 10-8 8zM7.5 7.5h.01",
  layers: "M12 3l9 5-9 5-9-5zM3 13l9 5 9-5",
  calendar: "M4 5h16v16H4zM4 10h16M8 3v4M16 3v4",
  hash: "M5 9h14M5 15h14M10 3L8 21M16 3l-2 18",
  toggle: "M8 7h8a5 5 0 0 1 0 10H8A5 5 0 0 1 8 7zm8 7.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z",
  text: "M5 6V4h14v2M12 4v16M9 20h6",
  cursor: "M5 3l14 7-6 2-2 6zM13 13l5 5",
  bolt: "M13 2L4 14h7l-1 8 9-12h-7z",
  clock: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2",
  search: "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM21 21l-5-5",
};

export function Icon({ name, size = 16 }: { name: IconName; size?: number }) {
  return (
    <svg className="fp-icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
      <path d={PATHS[name]} />
    </svg>
  );
}

export function FilterPicker({ items, recent, onPick, onClose, nestingBlocked, title = "Add a filter" }: {
  items: CatalogItem[]; recent: string[]; onPick: (it: CatalogItem) => void; onClose: () => void;
  /** nestingBlocked disables filters that insert a group (the rule is as deep as it can go). */
  nestingBlocked?: boolean; title?: string;
}) {
  const [q, setQ] = useState("");
  const [rail, setRail] = useState<GroupId | "all">("all");
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const uid = useId().replace(/:/g, "");

  useEffect(() => { input.current?.focus(); }, []);

  const groups = useMemo(() => {
    const found = searchCatalog(items, q);
    const inRail = rail === "all" ? found : found.filter((it) => it.group === rail);
    return groupItems(inRail, q || rail !== "all" ? [] : recent);
  }, [items, q, rail, recent]);
  const flat = useMemo(() => groups.flatMap((g) => g.items.map((it) => ({ it, gid: g.id }))), [groups]);
  const enabled = (it: CatalogItem) => !(nestingBlocked && it.nests);
  useEffect(() => { setActive(0); }, [q, rail]);
  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`#${uid}-o${active}`)?.scrollIntoView({ block: "nearest" });
  }, [active, uid]);

  const pick = (i: number) => {
    const x = flat[i];
    if (x && enabled(x.it)) onPick(x.it);
  };
  const move = (d: number) => setActive((a) => (flat.length ? (a + d + flat.length) % flat.length : 0));
  const onKey = (e: React.KeyboardEvent) => {
    switch (e.key) {
      case "ArrowDown": e.preventDefault(); move(1); break;
      case "ArrowUp": e.preventDefault(); move(-1); break;
      case "Home": if (!q) { e.preventDefault(); setActive(0); } break;
      case "End": if (!q) { e.preventDefault(); setActive(Math.max(0, flat.length - 1)); } break;
      case "Enter": e.preventDefault(); pick(active); break;
      case "Escape": e.preventDefault(); e.stopPropagation(); onClose(); break;
    }
  };
  const counts = useMemo(() => {
    const found = searchCatalog(items, q);
    return Object.fromEntries(GROUPS.map((g) => [g.id, found.filter((it) => it.group === g.id).length])) as Record<GroupId, number>;
  }, [items, q]);

  let n = -1;
  return (
    <div className="fp" role="dialog" aria-label={title} onKeyDown={(e) => e.key === "Escape" && (e.stopPropagation(), onClose())}>
      <div className="fp-search">
        <Icon name="search" />
        <input
          ref={input} className="fp-input" value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={onKey}
          placeholder="Search filters: city, opened email, device, points…"
          role="combobox" aria-expanded="true" aria-controls={`${uid}-list`} aria-autocomplete="list"
          aria-activedescendant={flat.length ? `${uid}-o${active}` : undefined} aria-label="Search filters"
        />
        <button type="button" className="btn ghost sm" onClick={onClose}>Close</button>
      </div>
      <div className="fp-body">
        <nav className="fp-rail" aria-label="Filter groups">
          <button type="button" className={rail === "all" ? "on" : ""} aria-pressed={rail === "all"} onClick={() => setRail("all")}>All filters</button>
          {GROUPS.map((g) => (
            <button type="button" key={g.id} className={rail === g.id ? "on" : ""} aria-pressed={rail === g.id} disabled={counts[g.id] === 0} onClick={() => setRail(g.id)}>
              <Icon name={g.icon} size={14} /> <span>{g.label}</span> <small>{counts[g.id]}</small>
            </button>
          ))}
        </nav>
        <div className="fp-list" ref={list} id={`${uid}-list`} role="listbox" aria-label="Filters">
          {flat.length === 0 && <p className="fp-empty">No filter matches “{q}”. Try another word, or choose “Did an event” for a custom event.</p>}
          {groups.map((g) => (
            <div role="group" aria-labelledby={`${uid}-g-${g.id}`} key={g.id} className="fp-group">
              <div className="fp-group-head" id={`${uid}-g-${g.id}`}>{g.label}</div>
              {g.items.map((it) => {
                n++;
                const i = n, on = enabled(it);
                return (
                  <div
                    key={`${g.id}-${it.id}`} id={`${uid}-o${i}`} role="option" aria-selected={i === active} aria-disabled={!on}
                    className={`fp-item${i === active ? " active" : ""}${on ? "" : " disabled"}`}
                    onMouseEnter={() => setActive(i)} onMouseDown={(e) => e.preventDefault()} onClick={() => pick(i)}
                  >
                    <span className="fp-item-icon"><Icon name={it.icon} /></span>
                    <span className="fp-item-text">
                      <span className="fp-item-label">
                        {it.label}
                        {it.custom && it.type && <span className="fp-type">{typeWord(it.type)}</span>}
                        {it.nests && <span className="fp-type">group</span>}
                      </span>
                      <small>{on ? it.hint : "Needs a group level, and this group is already as deep as a segment can go."}{it.key && it.custom ? ` · ${it.key}` : ""}</small>
                    </span>
                  </div>
                );
              })}
            </div>
          ))}
        </div>
      </div>
      <p className="fp-foot xs muted">↑ ↓ to move · Enter to add · Esc to close</p>
    </div>
  );
}
