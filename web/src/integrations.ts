// Pure helpers for Settings → Integrations: which attributes a CRM may write into, and the
// field-mapping problems shown before saving (the server checks the same rules).
import type { FieldMap, IntegrationCategory } from "./api";

export const CATEGORY_LABELS: Record<IntegrationCategory, string> = {
  crm: "CRM", ecommerce: "E-commerce", automation: "Automation", ads: "Ads",
};
export const CATEGORY_ORDER: IntegrationCategory[] = ["crm", "ecommerce", "automation", "ads"];

const RESERVED = new Set([
  "tags", "points", "stage", "stage_changed_at", "owner_id", "owner_name", "merged_into", "email_bounced", "sms_bounced",
  "first_seen", "last_seen", "date_added", "date_identified", "date_modified", "email_domain", "browser", "app_version",
]);

/** reservedAttr: Pulse keeps it itself, so it changes only through its own events (never from a pull). */
export function reservedAttr(attr: string): boolean {
  return RESERVED.has(attr) || attr.startsWith("device_") || attr.startsWith("company_") || attr.startsWith("optout_");
}

export const RESERVED_NOTE = "kept by Pulse, changes only through its own events; use Out to CRM";

/** mappingProblems lists what's wrong with a mapping, row by row ("" for a fine row). */
export function mappingProblems(rows: FieldMap[]): string[] {
  const pulled = new Map<string, number>();
  return rows.map((m, i) => {
    const attr = m.attr.trim();
    if (!m.remote.trim() || !attr) return "Fill in both fields, or remove the row.";
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(attr)) return `“${attr}” isn't a valid attribute name (letters, digits and _).`;
    if (m.dir !== "push") {
      if (reservedAttr(attr)) return `“${attr}” is ${RESERVED_NOTE}.`;
      if (pulled.has(attr)) return `“${attr}” is already filled by row ${pulled.get(attr)! + 1}.`;
      pulled.set(attr, i);
    }
    return "";
  });
}

/** Pull schedule choices (minutes; 0 never pulls). */
export const PULL_EVERY: { value: number; label: string }[] = [
  { value: 0, label: "Never (push only)" }, { value: 15, label: "Every 15 minutes" }, { value: 30, label: "Every 30 minutes" },
  { value: 60, label: "Every hour" }, { value: 240, label: "Every 4 hours" }, { value: 1440, label: "Once a day" },
];
