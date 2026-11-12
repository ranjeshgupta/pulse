// M7: a number of contacts that opens the Contacts list filtered to exactly those contacts.
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { useMe } from "./auth";
import { canListContacts, contactsHref, whyNoLink, type ContactFilter } from "./contactLinks";
import { LiveNumber } from "./ui";

/** ContactCount shows value as a link to the contacts behind it, or plain (with why) when the
 * role can't open that list. Zero isn't linked: there is nobody to show. */
export function ContactCount({ value, filters, live, children, label }: {
  value: number | null | undefined; filters: ContactFilter[] | null; live?: boolean; children?: ReactNode; label?: string;
}) {
  const perms = useMe().data?.permissions;
  if (value == null) return <>{children ?? "—"}</>;
  const shown = children ?? (live ? <LiveNumber value={value} /> : value.toLocaleString("en-IN"));
  if (!filters || value <= 0) return <>{shown}</>;
  if (!canListContacts(perms, filters)) return <span title={whyNoLink(perms, filters)}>{shown}</span>;
  return (
    <Link className="count-link" to={contactsHref(filters)} title={label ? `Show these ${label}` : "Show these contacts"} onClick={(e) => e.stopPropagation()}>
      {shown}
    </Link>
  );
}
