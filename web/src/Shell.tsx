import { NavLink, Outlet } from "react-router-dom";
import { can, type Area, type Level } from "./api";
import { useMe, useSignOut } from "./auth";
import { roleName, useRoles } from "./pages/Roles";

const I = {
  pulse: <path d="M2 9h3l2-5 3 10 2-5h4" />,
  people: <><circle cx="6" cy="6" r="2.6" /><path d="M1.5 15c.6-3 2.3-4.5 4.5-4.5s3.9 1.5 4.5 4.5" /><circle cx="12" cy="7" r="2" /><path d="M11 10.6c2 .1 3.3 1.4 3.8 3.6" /></>,
  funnel: <path d="M2 3h13l-5 6v5l-3 1.5V9z" />,
  flow: <><rect x="1.5" y="2" width="5" height="4" rx="1" /><rect x="10.5" y="11" width="5" height="4" rx="1" /><path d="M4 6v3h9v2" /></>,
  doc: <><path d="M4 1.5h6l3.5 3.5v10.5h-9.5z" /><path d="M9.5 1.5V5h3.5M6.5 9h5M6.5 12h5" /></>,
  window: <><rect x="1.5" y="2.5" width="14" height="12" rx="1.5" /><path d="M1.5 6h14" /><rect x="5" y="8.5" width="7" height="3.5" rx=".8" /></>,
  phone: <><rect x="4" y="1.5" width="9" height="14" rx="1.6" /><path d="M7.5 13h2" /><rect x="5.8" y="5" width="5.4" height="4" rx=".6" /></>,
  slot: <><rect x="1.5" y="2.5" width="14" height="12" rx="1.5" /><path d="M4.5 6h8M4.5 9h8M4.5 12h5" /></>,
  send: <path d="M1.5 8 15.5 1.5 11 15.5 8 9z" />,
  megaphone: <><path d="M2 6.5v4h2.5l6 3.5V3L4.5 6.5z" /><path d="M13 6a3 3 0 0 1 0 5" /></>,
  inbox: <><path d="M1.5 9.5 4 3h9l2.5 6.5v5h-14z" /><path d="M1.5 9.5h4l1 2h4l1-2h4" /></>,
  team: <><circle cx="8.5" cy="5" r="3" /><path d="M2.5 15.5c.8-3.4 3-5 6-5s5.2 1.6 6 5" /></>,
  key: <><circle cx="5.5" cy="10.5" r="3.5" /><path d="m8 8 6.5-6.5M12 4l2 2" /></>,
  list: <><path d="M6 4h9M6 8.5h9M6 13h9" /><circle cx="2.5" cy="4" r=".9" /><circle cx="2.5" cy="8.5" r=".9" /><circle cx="2.5" cy="13" r=".9" /></>,
  tag: <><path d="M1.5 2.5v5.2l7.3 7.3 6.2-6.2-7.3-7.3H2.5z" /><circle cx="5" cy="5.5" r="1" /></>,
  star: <path d="m8.5 1.8 2 4.4 4.7.5-3.5 3.2 1 4.7-4.2-2.4-4.2 2.4 1-4.7L1.8 6.7l4.7-.5z" />,
  form: <><rect x="2" y="1.5" width="13" height="14" rx="1.5" /><path d="M5 5h7M5 8.5h7" /><rect x="5" y="11" width="4" height="2" rx=".5" /></>,
  steps: <><path d="M2 3h13M4 7.5h9M6 12h5" /><path d="M8.5 13.5v2" /></>,
  shield: <><path d="M8.5 1.5 14.5 4v4.5c0 3.4-2.6 6-6 7-3.4-1-6-3.6-6-7V4z" /><path d="m6 8.5 1.8 1.8L11.2 7" /></>,
  log: <><circle cx="8.5" cy="8.5" r="6.5" /><path d="M8.5 4.5v4l2.8 1.8" /></>,
};

function Icon({ d }: { d: React.ReactNode }) {
  return (
    <svg width="17" height="17" viewBox="0 0 17 17" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      {d}
    </svg>
  );
}

type NavItem = { to: string; label: string; icon: React.ReactNode; need?: [Area, Exclude<Level, "none">] };

// Each item shows only when the user's role reaches `need`.
const NAV: { title?: string; items: NavItem[] }[] = [
  { items: [{ to: "/", label: "Overview", icon: I.pulse }] },
  {
    title: "Audience",
    items: [
      { to: "/contacts", label: "Contacts", icon: I.people, need: ["contacts", "view"] },
      { to: "/segments", label: "Segments", icon: I.funnel, need: ["segments", "view"] },
      { to: "/lists", label: "Lists", icon: I.list, need: ["segments", "view"] },
      { to: "/forms", label: "Forms", icon: I.form, need: ["content", "view"] },
      { to: "/imports", label: "Imports", icon: I.list, need: ["contacts", "edit"] },
    ],
  },
  {
    title: "Engage",
    items: [
      { to: "/journeys", label: "Journeys", icon: I.flow, need: ["journeys", "view"] },
      { to: "/broadcasts", label: "Broadcasts", icon: I.megaphone, need: ["broadcasts", "view"] },
      { to: "/templates", label: "Templates", icon: I.doc, need: ["content", "view"] },
      { to: "/focus-items", label: "Focus items", icon: I.window, need: ["content", "view"] },
      { to: "/web-content", label: "Web content", icon: I.slot, need: ["content", "view"] },
      { to: "/in-app", label: "In-app messages", icon: I.phone, need: ["content", "view"] },
    ],
  },
  { title: "Analyze", items: [{ to: "/funnels", label: "Funnels", icon: I.steps, need: ["reports", "view"] }] },
  {
    title: "Delivery",
    items: [
      { to: "/channels", label: "Channels", icon: I.send, need: ["channels", "view"] },
      { to: "/test-inbox", label: "Test inbox", icon: I.inbox, need: ["channels", "view"] },
    ],
  },
  {
    title: "Workspace",
    items: [
      { to: "/settings/team", label: "Team", icon: I.team, need: ["settings", "view"] },
      { to: "/settings/roles", label: "Roles", icon: I.shield, need: ["settings", "view"] },
      { to: "/settings/attributes", label: "Attributes", icon: I.tag, need: ["segments", "view"] },
      { to: "/settings/scoring", label: "Scoring", icon: I.star, need: ["segments", "view"] },
      { to: "/settings/access", label: "Sign-in & keys", icon: I.key, need: ["settings", "edit"] },
      { to: "/settings/webhooks", label: "Webhooks", icon: I.send, need: ["settings", "edit"] },
      { to: "/settings/data", label: "Data & privacy", icon: I.key, need: ["settings", "view"] },
      { to: "/settings/audit", label: "Audit log", icon: I.log, need: ["settings", "edit"] },
    ],
  },
];

export function Logo() {
  return (
    <svg width="26" height="26" viewBox="0 0 32 32" aria-hidden>
      <rect width="32" height="32" rx="7" fill="#24324a" />
      <path d="M5 17h6l3-8 4 14 3-6h6" fill="none" stroke="#F0A202" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export default function Shell() {
  const me = useMe().data;
  const signOut = useSignOut();
  const roles = useRoles();
  const allowed = (it: NavItem) => !it.need || can(me?.permissions, it.need[0], it.need[1]);
  return (
    <div className="shell">
      <nav className="sidebar" aria-label="Main">
        <div className="brand">
          <Logo />
          <div>
            Pulse
            <div className="workspace-name">{me?.workspace.name}</div>
          </div>
        </div>
        {NAV.map((g) => ({ ...g, items: g.items.filter(allowed) })).filter((g) => g.items.length > 0).map((g, i) => (
          <div className="nav-group nav" key={g.title ?? i}>
            {g.title && <div className="nav-group-title">{g.title}</div>}
            {g.items.map((it) => (
              <NavLink key={it.to} to={it.to} end={it.to === "/"}>
                <Icon d={it.icon} />
                {it.label}
              </NavLink>
            ))}
          </div>
        ))}
        <div className="sidebar-foot">
          <div>{me?.user?.name || me?.user?.email}</div>
          <div className="xs" style={{ color: "#8e9bb3", margin: "2px 0 8px" }}>{roleName(me?.role, roles.data)}</div>
          <button onClick={signOut}>Sign out</button>
        </div>
      </nav>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
