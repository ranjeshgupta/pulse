// Nothing lost by accident: a styled confirm dialog (ask / useConfirm) used for every delete and
// every "discard changes?", change tracking for dialogs, drawers and editor pages, a leave guard
// (route changes and closing the tab), and Ctrl/⌘+S to save on editor pages.
import { createContext, useCallback, useContext, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { UNSAFE_DataRouterContext, useBlocker, type Blocker, type BlockerFunction } from "react-router-dom";

export type AskOptions = {
  title: string;
  body?: ReactNode;
  /** The confirming button's text ("Delete", "Discard"…). */
  confirmText?: string;
  cancelText?: string;
  /** danger: a red confirming button (deletes, discards). */
  danger?: boolean;
  /** typeToConfirm: the confirming button stays off until this text is typed (big deletes). */
  typeToConfirm?: string;
};

type Pending = AskOptions & { resolve: (ok: boolean) => void };

const AskCtx = createContext<(o: AskOptions) => Promise<boolean>>((o) => Promise.resolve(window.confirm(o.title)));
let host: ((o: AskOptions) => Promise<boolean>) | null = null;

/** ask shows the confirm dialog from anywhere (falls back to the browser's confirm before the app mounts). */
export function ask(o: AskOptions | string): Promise<boolean> {
  const opts = typeof o === "string" ? { title: o } : o;
  return host ? host(opts) : Promise.resolve(window.confirm(opts.title));
}

/** askDelete: the standard delete question. what is "this segment", "Spring sale"…; then says what happens. */
export function askDelete(what: string, then?: ReactNode, typeToConfirm?: string): Promise<boolean> {
  return ask({ title: `Delete ${what}?`, body: then ?? "This can't be undone.", confirmText: "Delete", danger: true, typeToConfirm });
}

export const DISCARD: AskOptions = {
  title: "Discard your changes?",
  body: "You have changes that aren't saved. If you leave now they're lost.",
  confirmText: "Discard changes",
  cancelText: "Keep editing",
  danger: true,
};

export const useConfirm = () => useContext(AskCtx);

/** ConfirmProvider renders the confirm dialog; mount it once around the app. */
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [queue, setQueue] = useState<Pending[]>([]);
  const open = useCallback((o: AskOptions) => new Promise<boolean>((resolve) => setQueue((q) => [...q, { ...o, resolve }])), []);
  useEffect(() => {
    host = open;
    return () => { if (host === open) host = null; };
  }, [open]);
  const cur = queue[0];
  const done = (ok: boolean) => {
    cur?.resolve(ok);
    setQueue((q) => q.slice(1));
  };
  return (
    <AskCtx.Provider value={open}>
      {children}
      {cur && <ConfirmDialog key={queue.length} o={cur} onDone={done} />}
    </AskCtx.Provider>
  );
}

function ConfirmDialog({ o, onDone }: { o: AskOptions; onDone: (ok: boolean) => void }) {
  const [typed, setTyped] = useState("");
  const okRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const can = !o.typeToConfirm || typed.trim() === o.typeToConfirm;
  useEffect(() => {
    // Capture phase, first: Esc / Enter answer this dialog and never reach the dialog behind it.
    const h = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); e.stopImmediatePropagation(); onDone(false); }
      // Enter answers with the focused button; a dangerous question starts on the safe answer.
      if (e.key === "Enter" && !o.danger && can && !(e.target instanceof HTMLTextAreaElement)) { e.preventDefault(); e.stopImmediatePropagation(); onDone(true); }
    };
    window.addEventListener("keydown", h, true);
    return () => window.removeEventListener("keydown", h, true);
  }, [onDone, can, o.danger]);
  useEffect(() => { if (!o.typeToConfirm) (o.danger ? cancelRef : okRef).current?.focus(); }, [o.typeToConfirm, o.danger]);
  return (
    <>
      <div className="modal-backdrop confirm-backdrop" onClick={() => onDone(false)} />
      <div className="modal confirm-dialog" role="alertdialog" aria-modal="true" aria-label={o.title}>
        <div className="panel-body">
          <h3 style={{ marginTop: 0 }}>{o.title}</h3>
          {o.body && <div className="small muted">{o.body}</div>}
          {o.typeToConfirm && (
            <label className="field" style={{ marginTop: 12 }}>
              <span className="small">Type <strong>{o.typeToConfirm}</strong> to confirm</span>
              <input className="input" autoFocus value={typed} onChange={(e) => setTyped(e.target.value)} data-no-dirty />
            </label>
          )}
        </div>
        <div className="modal-foot">
          <button ref={cancelRef} className="btn" onClick={() => onDone(false)}>{o.cancelText ?? "Cancel"}</button>
          <button ref={okRef} className={`btn ${o.danger ? "danger" : "primary"}`} disabled={!can} onClick={() => onDone(true)}>{o.confirmText ?? "OK"}</button>
        </div>
      </div>
    </>
  );
}

/** tracksChange: does an input event on this element count as editing? (Search boxes and
 * elements marked data-no-dirty don't.) */
export function tracksChange(el: EventTarget | null): boolean {
  if (!(el instanceof Element)) return false;
  if (el.closest("[data-no-dirty]")) return false;
  if (el instanceof HTMLInputElement) return el.type !== "search";
  return el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement || (el as HTMLElement).isContentEditable;
}

/** isCloseButton: a dialog footer button that closes without saving. */
export function isCloseButton(text: string, marked: boolean): boolean {
  return marked || /^(cancel|close|not now)$/i.test(text.trim());
}

const idle: Blocker = { state: "unblocked", proceed: undefined, reset: undefined, location: undefined };

/** useLeaveBlocker is useBlocker where there is a data router (the app), and never blocks where
 * there isn't (tests rendering a page on its own). Whether there is one never changes while a
 * component is mounted, so the hook order stays the same. */
function useLeaveBlocker(fn: BlockerFunction): Blocker {
  const dataRouter = useContext(UNSAFE_DataRouterContext);
  // eslint-disable-next-line react-hooks/rules-of-hooks
  return dataRouter ? useBlocker(fn) : idle;
}

/** DialogCtx lets content inside a Modal / Drawer say its changes were saved without closing it. */
export const DialogCtx = createContext<{ saved: () => void }>({ saved: () => {} });

/** useDialogSaved: call the returned function after an inline save inside a dialog, so closing it
 * afterwards doesn't ask about changes that are already saved. */
export const useDialogSaved = () => useContext(DialogCtx).saved;

/** useDirtyRef tracks editing inside an element: spread track on it, read dirty.current, clear with reset(). */
export function useDirtyRef() {
  const dirty = useRef(false);
  const mark = useCallback((e: FormEvent) => { if (tracksChange(e.target)) dirty.current = true; }, []);
  const reset = useCallback(() => { dirty.current = false; }, []);
  return { dirty, reset, track: { onInputCapture: mark, onChangeCapture: mark } };
}

/** useEditGuard protects an editor page: it notices editing inside the element you spread track
 * on, asks before leaving the page (links, back button, closing the tab) while there are unsaved
 * changes, and saves with Ctrl/⌘+S when onSave is given. Call saved() when a save succeeds (before
 * navigating), and markDirty() for changes made without typing (drag and drop, a canvas). */
export function useEditGuard(onSave?: () => void) {
  const { dirty, reset, track } = useDirtyRef();
  const [, rerender] = useState(0);
  const markDirty = useCallback(() => { dirty.current = true; rerender((n) => n + 1); }, [dirty]);
  const blocker = useLeaveBlocker(({ currentLocation, nextLocation }) =>
    dirty.current && (currentLocation.pathname !== nextLocation.pathname || currentLocation.search !== nextLocation.search));
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    void ask(DISCARD).then((ok) => {
      if (ok) { reset(); blocker.proceed?.(); } else blocker.reset?.();
    });
  }, [blocker, reset]);
  useEffect(() => {
    const h = (e: BeforeUnloadEvent) => {
      if (!dirty.current) return;
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", h);
    return () => window.removeEventListener("beforeunload", h);
  }, [dirty]);
  const saveRef = useRef(onSave);
  saveRef.current = onSave;
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s" && saveRef.current) {
        e.preventDefault();
        saveRef.current();
      }
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, []);
  return { track, saved: reset, markDirty, isDirty: () => dirty.current };
}
