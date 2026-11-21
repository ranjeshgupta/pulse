// StepChooser: the "+" dialog. Step 1 asks what kind of step (Action, Decision, Condition, Wait,
// Split, each with its meaning); step 2 lists that kind's steps with a search box, descriptions and,
// for steps that can't go here, the reason. Typing on step 1 searches every step. Keyboard: Tab
// stays inside the dialog, ↑ ↓ move, Enter picks, Esc closes (the caller returns focus to the "+").
import { useEffect, useId, useMemo, useRef, useState } from "react";
import type { JourneyGraph } from "../../api";
import { itemsFor, JOURNEY_CATALOG, reasonAt, searchItems, type CatalogCtx, type JourneyItem } from "../../journeyCatalog";
import { STEP_GROUPS, type InsertAt, type StepGroup } from "../../journeyGraph";
import { JIcon } from "./icons";

const GROUP_ICON: Record<StepGroup, Parameters<typeof JIcon>[0]["name"]> = {
  action: "bolt", decision: "split", condition: "filter", wait: "hourglass", split: "percent",
};

export function StepChooser({ graph, at, where, ctx, onPick, onClose }: {
  graph: JourneyGraph; at: InsertAt; where: string; ctx: CatalogCtx;
  onPick: (it: JourneyItem) => void; onClose: () => void;
}) {
  const [cat, setCat] = useState<StepGroup | null>(null);
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const dialog = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const catButtons = useRef<(HTMLButtonElement | null)[]>([]);
  const uid = useId().replace(/:/g, "");

  const listing = cat !== null || q.trim() !== "";
  const items = useMemo(() => searchItems(cat ? itemsFor(cat) : JOURNEY_CATALOG, q), [cat, q]);
  const reasons = useMemo(() => new Map(items.map((it) => [it.id, reasonAt(it, graph, at, ctx)])), [items, graph, at, ctx]);

  useEffect(() => { input.current?.focus(); }, [cat]);
  useEffect(() => { setActive(0); }, [cat, q]);
  useEffect(() => {
    dialog.current?.querySelector<HTMLElement>(`#${uid}-o${active}`)?.scrollIntoView({ block: "nearest" });
  }, [active, uid]);

  const pick = (it?: JourneyItem) => { if (it && !reasons.get(it.id)) onPick(it); };
  const move = (d: number) => setActive((a) => (items.length ? (a + d + items.length) % items.length : 0));

  const onSearchKey = (e: React.KeyboardEvent) => {
    if (!listing) {
      if (e.key === "ArrowDown") { e.preventDefault(); catButtons.current[0]?.focus(); }
      return;
    }
    switch (e.key) {
      case "ArrowDown": e.preventDefault(); move(1); break;
      case "ArrowUp": e.preventDefault(); move(-1); break;
      case "Enter": e.preventDefault(); pick(items[active]); break;
    }
  };
  const onCatKey = (e: React.KeyboardEvent, i: number) => {
    const n = STEP_GROUPS.length;
    if (e.key === "ArrowDown") { e.preventDefault(); catButtons.current[(i + 1) % n]?.focus(); }
    if (e.key === "ArrowUp") { e.preventDefault(); if (i === 0) input.current?.focus(); else catButtons.current[i - 1]?.focus(); }
  };
  // Focus trap and Esc.
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault(); e.stopPropagation();
      onClose();
      return;
    }
    if (e.key !== "Tab" || !dialog.current) return;
    const f = [...dialog.current.querySelectorAll<HTMLElement>("button:not([disabled]), input, [tabindex='0']")];
    if (f.length === 0) return;
    const first = f[0], last = f[f.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  };

  const group = cat ? STEP_GROUPS.find((g) => g.group === cat)! : null;
  let lastCat: StepGroup | null = null;
  return (
    <>
      <div className="sc-backdrop" onMouseDown={onClose} />
      <div className="sc" role="dialog" aria-modal="true" aria-labelledby={`${uid}-t`} aria-describedby={`${uid}-w`} ref={dialog} onKeyDown={onKeyDown}>
        <div className="sc-head">
          <div>
            <h3 id={`${uid}-t`}>{group ? `Add ${group.label === "Action" ? "an" : "a"} ${group.label.toLowerCase()}` : "Add a step"}</h3>
            <p className="xs muted" id={`${uid}-w`}>{where}</p>
          </div>
          <button type="button" className="btn ghost sm" onClick={onClose} aria-label="Close">Close</button>
        </div>
        {cat && (
          <div className="sc-back">
            <button type="button" className="btn ghost sm" onClick={() => { setCat(null); setQ(""); }}>
              <span aria-hidden="true">←</span> Back
            </button>
            <span className="xs muted">{group!.hint}</span>
          </div>
        )}
        <div className="sc-search">
          <input
            ref={input} className="input" data-no-dirty value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={onSearchKey}
            placeholder={cat ? `Search ${group!.label.toLowerCase()} steps` : "Search every step: form, email, tag…"}
            aria-label={cat ? `Search ${group!.label.toLowerCase()} steps` : "Search steps"}
            role={listing ? "combobox" : undefined} aria-expanded={listing ? true : undefined}
            aria-controls={listing ? `${uid}-list` : undefined} aria-autocomplete={listing ? "list" : undefined}
            aria-activedescendant={listing && items.length ? `${uid}-o${active}` : undefined}
          />
        </div>
        {!listing ? (
          <div className="sc-cats" role="group" aria-label="Kinds of step">
            {STEP_GROUPS.map((g, i) => (
              <button
                key={g.group} type="button" className={`sc-cat c-${g.group}`} ref={(el) => { catButtons.current[i] = el; }}
                aria-label={g.label} aria-describedby={`${uid}-c-${g.group}`} onClick={() => setCat(g.group)} onKeyDown={(e) => onCatKey(e, i)}
              >
                <span className="sc-cat-icon"><JIcon name={GROUP_ICON[g.group]} size={18} /></span>
                <span className="sc-cat-text">
                  <strong>{g.label}</strong>
                  <small id={`${uid}-c-${g.group}`}>{g.hint}</small>
                </span>
                <span className="sc-cat-count" aria-hidden="true">{itemsFor(g.group).length}</span>
              </button>
            ))}
          </div>
        ) : (
          <div className="sc-list" id={`${uid}-list`} role="listbox" aria-label={group ? `${group.label} steps` : "Matching steps"}>
            {items.length === 0 && <p className="sc-empty small muted">No step matches “{q}”.</p>}
            {items.map((it, i) => {
              const reason = reasons.get(it.id);
              const head = !cat && it.category !== lastCat ? STEP_GROUPS.find((g) => g.group === it.category)!.label : null;
              lastCat = it.category;
              return (
                <div key={it.id}>
                  {head && <div className="sc-group-head" aria-hidden="true">{head}</div>}
                  <div
                    id={`${uid}-o${i}`} role="option" aria-selected={i === active} aria-disabled={reason ? true : undefined}
                    aria-label={it.label} aria-describedby={`${uid}-d${i}`}
                    className={`sc-item c-${it.category}${i === active ? " active" : ""}${reason ? " disabled" : ""}`}
                    onMouseEnter={() => setActive(i)} onMouseDown={(e) => e.preventDefault()} onClick={() => pick(it)}
                  >
                    <span className="sc-item-icon"><JIcon name={it.icon} /></span>
                    <span className="sc-item-text">
                      <span className="sc-item-label">{it.label}</span>
                      <small id={`${uid}-d${i}`}>{reason ?? it.description}</small>
                    </span>
                  </div>
                </div>
              );
            })}
          </div>
        )}
        <p className="sc-foot xs muted">{listing ? "↑ ↓ to move · Enter to add · Esc to close" : "Choose a kind of step · or type to search · Esc to close"}</p>
      </div>
    </>
  );
}
