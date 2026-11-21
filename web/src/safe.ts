// Small guards for data the app doesn't control (API responses, saved settings, pasted JSON, what
// people type). Plain objects inherit keys such as "constructor" and "toString": a lookup by a
// user-typed name must only see the object's own keys, and a write must never reach a prototype.

/** own reads rec[key] only when key is rec's own property (never an inherited one). */
export function own<T>(rec: Readonly<Record<string, T>> | null | undefined, key: unknown): T | undefined {
  if (rec == null || typeof rec !== "object" || typeof key !== "string") return undefined;
  return Object.prototype.hasOwnProperty.call(rec, key) ? rec[key] : undefined;
}

/** hasOwn reports whether key is obj's own property. */
export const hasOwn = (obj: unknown, key: string): boolean =>
  obj != null && typeof obj === "object" && Object.prototype.hasOwnProperty.call(obj, key);

/** isUnsafeKey: keys that reach an object's prototype when written as obj[key]. */
export const isUnsafeKey = (k: string) => k === "__proto__" || k === "constructor" || k === "prototype";

/** finite is v as a finite number, else the fallback (NaN, Infinity, null, "abc" give the fallback). */
export function finite(v: unknown, fallback = 0): number {
  const n = typeof v === "number" ? v : typeof v === "string" && v.trim() !== "" ? Number(v) : NaN;
  return Number.isFinite(n) ? n : fallback;
}

/** list is v when it is an array, else an empty one (a missing or null list). */
export const list = <T>(v: readonly T[] | null | undefined): T[] => (Array.isArray(v) ? (v as T[]) : []);

/** str is v when it is a string, else "" (a missing or null name). */
export const str = (v: unknown): string => (typeof v === "string" ? v : v == null ? "" : typeof v === "number" || typeof v === "boolean" ? String(v) : "");

/** dict copies entries into an object without a prototype, so a lookup by any typed name (also
 * "constructor" or "__proto__") finds only these entries. Use it for maps keyed by names from data. */
export function dict<T>(entries: Record<string, T>): Record<string, T> {
  return Object.assign(Object.create(null) as Record<string, T>, entries);
}

/** setOwn sets obj[key] as obj's own property. Plain assignment of "__proto__" would replace the
 * prototype (or be dropped silently); a typed key such as an attribute name must stay a key. */
export function setOwn<T>(obj: Record<string, T>, key: string, value: T): void {
  if (isUnsafeKey(key)) Object.defineProperty(obj, key, { value, writable: true, enumerable: true, configurable: true });
  else obj[key] = value;
}
