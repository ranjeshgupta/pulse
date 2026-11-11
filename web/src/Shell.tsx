import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { can, type Area, type Level } from "./api";
import { useMe, useSignOut } from "./auth";
import { roleName, useRoles } from "./pages/Roles";
import { useSettingsNav } from "./SettingsLayout";

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
  building: <><rect x="2.5" y="2" width="8" height="13.5" rx="1" /><path d="M10.5 7h4v8.5h-4M5 5h3M5 8h3M5 11h3" /></>,
  stairs: <path d="M1.5 15h4v-4h4V7h4V3h2" />,
  page: <><rect x="1.5" y="1.5" width="14" height="14" rx="1.5" /><path d="M1.5 5h14" /><rect x="4" y="7.5" width="9" height="3" rx=".6" /><path d="M4 13h5" /></>,
  file: <><path d="M4 1.5h6l3.5 3.5v10.5h-9.5z" /><path d="M8.5 7v5.5M6.3 10.3l2.2 2.2 2.2-2.2" /></>,
  grid: <><rect x="1.5" y="1.5" width="4" height="4" rx=".6" /><rect x="6.5" y="1.5" width="4" height="4" rx=".6" /><rect x="11.5" y="1.5" width="4" height="4" rx=".6" /><rect x="1.5" y="6.5" width="4" height="4" rx=".6" /><rect x="6.5" y="6.5" width="4" height="4" rx=".6" /><rect x="1.5" y="11.5" width="4" height="4" rx=".6" /></>,
  coin: <><circle cx="8.5" cy="8.5" r="6.5" /><path d="M10.6 6.2c-.4-.8-1.2-1.2-2.1-1.2-1.2 0-2.1.7-2.1 1.7 0 2.4 4.4 1.2 4.4 3.6 0 1-1 1.8-2.3 1.8-1 0-1.9-.5-2.3-1.3M8.5 3.8V5M8.5 12.1v1.2" /></>,
  plug: <><path d="M6 1.5v4M11 1.5v4" /><path d="M3.5 5.5h10v3a5 5 0 0 1-10 0z" /><path d="M8.5 13.5v2" /></>,
  gear: <><circle cx="8.5" cy="8.5" r="2.4" /><path d="M8.5 1.5v2M8.5 13.5v2M1.5 8.5h2M13.5 8.5h2M3.5 3.5l1.4 1.4M12.1 12.1l1.4 1.4M3.5 13.5l1.4-1.4M12.1 4.9l1.4-1.4" /></>,
  bolt: <path d="M9.5 1.5 3 9.5h5l-1 6 6.5-8h-5z" />,
  chart: <><path d="M2 15h13" /><rect x="3" y="8" width="2.6" height="5" rx=".5" /><rect x="7.2" y="4" width="2.6" height="9" rx=".5" /><rect x="11.4" y="6.5" width="2.6" height="6.5" rx=".5" /></>,
};

function Icon({ d }: { d: React.ReactNode }) {
  return (
    <svg width="17" height="17" viewBox="0 0 17 17" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      {d}
    </svg>
  );
}

type NavItem = { to: string; label: string; icon: React.ReactNode; need?: [Area, Exclude<Level, "none">]; orNeed?: [Area, Exclude<Level, "none">] };

// Each item shows only when the user's role reaches `need`. Groups follow the usual marketing-tool
// layout: who (Audience), what you send and show (Campaigns), what it's built from (Content), how
// it did (Analytics), then one Settings entry with its own menu (SettingsLayout.tsx).
const NAV: { title?: string; items: NavItem[] }[] = [
  { items: [{ to: "/", label: "Overview", icon: I.pulse }] },
  {
    title: "Audience",
    items: [
      { to: "/contacts", label: "Contacts", icon: I.people, need: ["contacts", "view"] },
      { to: "/companies", label: "Companies", icon: I.building, need: ["contacts", "view"] },
      { to: "/segments", label: "Segments", icon: I.funnel, need: ["segments", "view"] },
      { to: "/events", label: "Events", icon: I.bolt, need: ["contacts", "view"] },
    ],
  },
  {
    title: "Campaigns",
    items: [
      { to: "/campaigns", label: "All campaigns", icon: I.send, need: ["broadcasts", "view"], orNeed: ["journeys", "view"] },
      { to: "/journeys", label: "Journeys", icon: I.flow, need: ["journeys", "view"] },
      { to: "/broadcasts", label: "Broadcasts", icon: I.megaphone, need: ["broadcasts", "view"] },
      { to: "/on-site", label: "On-site & in-app", icon: I.window, need: ["content", "view"] },
    ],
  },
  {
    title: "Content",
    items: [
      { to: "/templates", label: "Message templates", icon: I.doc, need: ["content", "view"] },
      { to: "/landing-pages", label: "Landing pages", icon: I.page, need: ["content", "view"] },
      { to: "/forms", label: "Forms", icon: I.form, need: ["content", "view"] },
      { to: "/assets", label: "Assets", icon: I.file, need: ["content", "view"] },
    ],
  },
  {
    title: "Analytics",
    items: [
      { to: "/reports", label: "Reports", icon: I.chart, need: ["reports", "view"] },
      { to: "/funnels", label: "Funnels", icon: I.steps, need: ["reports", "view"] },
      { to: "/cohorts", label: "Cohorts", icon: I.grid, need: ["reports", "view"] },
      { to: "/revenue", label: "Revenue", icon: I.coin, need: ["reports", "view"] },
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
  const settings = useSettingsNav();
  const [menuOpen, setMenuOpen] = useState(false);
  const loc = useLocation();
  useEffect(() => setMenuOpen(false), [loc.pathname]);
  const allowed = (it: NavItem) => !it.need || can(me?.permissions, it.need[0], it.need[1]) || (!!it.orNeed && can(me?.permissions, it.orNeed[0], it.orNeed[1]));
  return (
    <div className="shell">
      <nav className={`sidebar${menuOpen ? " open" : ""}`} aria-label="Main">
        <div className="brand">
          <Logo />
          <div>
            Pulse
            <div className="workspace-name">{me?.workspace.name}</div>
          </div>
          <button className="menu-toggle" aria-expanded={menuOpen} aria-controls="main-nav" onClick={() => setMenuOpen(!menuOpen)}>{menuOpen ? "Close" : "Menu"}</button>
        </div>
        <div className="sidebar-nav" id="main-nav">
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
          {settings.length > 0 && (
            <div className="nav-group nav">
              <NavLink to="/settings"><Icon d={I.gear} />Settings</NavLink>
            </div>
          )}
          <div className="sidebar-foot">
            <div>{me?.user?.name || me?.user?.email}</div>
            <div className="xs" style={{ color: "#8e9bb3", margin: "2px 0 8px" }}>{roleName(me?.role, roles.data)}</div>
            <button onClick={signOut}>Sign out</button>
          </div>
        </div>
      </nav>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
