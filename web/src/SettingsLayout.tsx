import { Link, NavLink, Outlet } from "react-router-dom";
import { can, type Area, type Level } from "./api";
import { useMe } from "./auth";
import { PageHead } from "./ui";

// Settings: one entry in the sidebar, and a second-level menu grouped the way most marketing tools
// group theirs (workspace and people, contact data, delivery, connections).

export type SettingsItem = { to: string; label: string; hint: string; need: [Area, Exclude<Level, "none">] };

export const SETTINGS_NAV: { title: string; items: SettingsItem[] }[] = [
  {
    title: "Workspace",
    items: [
      { to: "/settings/team", label: "Team", hint: "Teammates and their roles", need: ["settings", "view"] },
      { to: "/settings/roles", label: "Roles", hint: "What each role may view, edit and publish", need: ["settings", "view"] },
      { to: "/settings/access", label: "Sign-in & keys", hint: "SSO, API keys, the website snippet", need: ["settings", "edit"] },
      { to: "/settings/audit", label: "Audit log", hint: "Who changed what, and when", need: ["settings", "edit"] },
      { to: "/settings/automation", label: "Automation & limits", hint: "Data retention, retries, schedules, default quotas", need: ["settings", "view"] },
    ],
  },
  {
    title: "Contact data",
    items: [
      { to: "/settings/attributes", label: "Contact fields", hint: "Names and types of profile attributes", need: ["segments", "view"] },
      { to: "/settings/scoring", label: "Scoring", hint: "Points for events", need: ["segments", "view"] },
      { to: "/settings/stages", label: "Stages", hint: "Lifecycle stages", need: ["segments", "view"] },
      { to: "/settings/data", label: "Data & privacy", hint: "Anonymous tracking and visitor location", need: ["settings", "view"] },
    ],
  },
  {
    title: "Delivery",
    items: [
      { to: "/settings/channels", label: "Channels", hint: "Providers, sending rules, bounces, reply tracking", need: ["channels", "view"] },
      { to: "/settings/test-inbox", label: "Test inbox", hint: "Messages sent through sandbox providers", need: ["channels", "view"] },
      { to: "/settings/web-push", label: "Web push", hint: "Opt-in prompt and install check", need: ["channels", "view"] },
    ],
  },
  {
    title: "Connections",
    items: [
      { to: "/settings/integrations", label: "Integrations", hint: "CRMs, Shopify, Zapier, ad audiences", need: ["settings", "view"] },
      { to: "/settings/webhooks", label: "Webhooks", hint: "Send events to your own systems", need: ["settings", "edit"] },
    ],
  },
];

export function useSettingsNav() {
  const perms = useMe().data?.permissions;
  return SETTINGS_NAV.map((g) => ({ ...g, items: g.items.filter((i) => can(perms, i.need[0], i.need[1])) })).filter((g) => g.items.length > 0);
}

/** SettingsLayout wraps every settings page with the settings menu. */
export function SettingsLayout() {
  const groups = useSettingsNav();
  return (
    <div className="settings-layout">
      <nav className="settings-nav" aria-label="Settings">
        {groups.map((g) => (
          <div key={g.title}>
            <div className="settings-nav-title">{g.title}</div>
            {g.items.map((i) => <NavLink key={i.to} to={i.to}>{i.label}</NavLink>)}
          </div>
        ))}
      </nav>
      <div className="settings-main"><Outlet /></div>
    </div>
  );
}

/** SettingsHome is /settings: every settings page with what it's for. */
export function SettingsHome() {
  const groups = useSettingsNav();
  return (
    <div className="page">
      <PageHead title="Settings" intro="Workspace, contact data, delivery and connections." />
      {groups.map((g) => (
        <section className="section" key={g.title}>
          <h3>{g.title}</h3>
          <div className="settings-cards">
            {g.items.map((i) => (
              <Link key={i.to} to={i.to} className="settings-card"><strong>{i.label}</strong><span>{i.hint}</span></Link>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}
