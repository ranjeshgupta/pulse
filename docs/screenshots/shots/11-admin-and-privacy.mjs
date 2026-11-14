// Screenshots for docs/tutorials/11-admin-and-privacy.md: a contact's consent, the unsubscribe page
// and preference center (opened, not submitted), Download data / Delete contact on the profile (the
// delete window is filled in, then cancelled), Data & privacy, the audit log, single sign-on with
// the local Keycloak (turned on for the pictures, then back to how it was: removed again if it wasn't
// set up before), identity verification and keys
// (nothing replaced), the "Delete contact" journey step (in a draft journey, deleted again), and
// two-step sign-in. Two-step sign-in is never turned on and the policy never saved: the server's
// answers to the setup, the turn-on and the sign-in are stood in for in the browser (page.route),
// so the pictures show the real screens with a made-up key and made-up recovery codes, and nothing
// changes for docs.admin@pulse.test (the account every script signs in with).
import { api, clearHighlights, go, highlight, runId, session, settle, shot } from "../lib.mjs";

const AREA = "tutorials";

// The local Keycloak from deploy/dev-seed.md (test values for the local Docker stack only).
const KEYCLOAK = {
  issuer: "http://localhost:8180/realms/pulse",
  discovery_url: "http://keycloak:8180/realms/pulse",
  client_id: "pulse-app",
  client_secret: "pulse-dev-client-secret",
};

const panel = (page, title) => page.locator("section.panel", { has: page.getByRole("heading", { name: title, exact: true }) });
const field = (page, label) => page.locator("label.field").filter({ has: page.locator(`xpath=./span[normalize-space(.)=${JSON.stringify(label)}]`) }).locator("input, select, textarea").first();

export default async function (browser) {
  const { context, page } = await session(browser);
  const sso = await api(context, "GET", "/v1/settings/sso");
  const journeys = [];
  try {
    await consent(context, page);
    await dataAndAudit(page);
    await signIn(browser, context, page, sso);
    await deleteStep(context, page, journeys);
    await twoStep(browser, page);
  } finally {
    for (const id of journeys) await api(context, "DELETE", `/v1/journeys/${id}`).catch(() => {});
    // Single sign-on back to how it was: removed if it wasn't set up, else off again.
    const now = await api(context, "GET", "/v1/settings/sso").catch(() => null);
    if (!sso.issuer) {
      if (now?.issuer) await api(context, "DELETE", "/v1/settings/sso").catch((e) => console.log(`  SSO remove: ${e}`));
    } else if (now?.has_client_secret) {
      await api(context, "PUT", "/v1/settings/sso", { ...now, enabled: sso.enabled, default_role: sso.default_role, auto_create_users: sso.auto_create_users, client_secret: "" }).catch((e) => console.log(`  SSO restore: ${e}`));
    }
    await context.close();
  }
}

async function consent(context, page) {
  // Contacts reachable by SMS (so they have a phone), newest first: the newest contacts may be
  // anonymous visitors other guides' scripts made.
  const contacts = (await api(context, "GET", `/v1/contacts?q=${encodeURIComponent("reachable:sms")}&limit=100`)).contacts;
  const c = contacts.find((x) => x.email && x.attrs?.phone && !Object.keys(x.attrs).some((k) => k.startsWith("optout_") && String(x.attrs[k]) === "true")) ?? contacts[0];

  // The contact's "Messages they agreed to".
  await page.setViewportSize({ width: 1440, height: 2400 });
  await go(page, `/contacts?open=${encodeURIComponent(c.external_id)}`);
  const agreed = page.locator("aside.drawer section", { has: page.getByRole("heading", { name: "Messages they agreed to" }) });
  await agreed.waitFor();
  await settle(page, { quiet: 800 });
  await shot(page, AREA, "11-consent", { clip: agreed, pad: 12 });
  await page.setViewportSize({ width: 1440, height: 900 });

  // Download data and Delete contact at the top of the profile; the delete window filled in, then
  // cancelled (nothing is downloaded or deleted).
  await go(page, `/contacts?open=${encodeURIComponent(c.external_id)}`);
  const drawer = page.locator("aside.drawer");
  const download = drawer.getByRole("link", { name: "Download data" });
  const del = drawer.getByRole("button", { name: "Delete contact" });
  await del.waitFor();
  await settle(page, { quiet: 600 });
  await highlight(download);
  await highlight(del);
  await shot(page, AREA, "11-contact-data", { clip: drawer.locator(".drawer-head, .drawer-body > div.row:has(a:text-is('Download data'))"), pad: 8 });
  await clearHighlights(page);
  await del.click();
  const confirmBox = page.getByRole("dialog", { name: /^Delete .*\?$/ });
  await confirmBox.getByLabel("Type DELETE to confirm").fill("DELETE");
  await settle(page);
  await shot(page, AREA, "11-delete-contact", { clip: confirmBox, pad: 0 });
  await confirmBox.getByRole("button", { name: "Cancel" }).click();
  await confirmBox.waitFor({ state: "detached" });

  // The contact's own pages (opened only; nothing is clicked on them).
  const { url } = await api(context, "GET", `/v1/contacts/${encodeURIComponent(c.external_id)}/preferences-link`);
  const pub = await context.newPage();
  const unsub = url.replace(/\?view=preferences$/, "");
  await pub.goto(unsub);
  await settle(pub);
  await shot(pub, AREA, "11-unsubscribe-page", { clip: pub.locator("main"), pad: 16 });
  await pub.goto(url);
  await settle(pub);
  await shot(pub, AREA, "11-preference-center", { clip: pub.locator("main"), pad: 16 });
  await pub.close();
}

async function dataAndAudit(page) {
  await go(page, "/settings/data");
  await shot(page, AREA, "11-data-privacy", { clip: page.locator(".page"), pad: 0 });

  // Settings → Automation & limits: the retention panel, the other panels, and the "What will be
  // deleted" dialog after shortening the events' retention on screen (cancelled, nothing saved).
  await page.setViewportSize({ width: 1440, height: 3000 });
  await go(page, "/settings/automation");
  const autoPanel = (title) => panel(page, title);
  await shot(page, AREA, "11-automation-retention", { clip: page.locator("header.page-head").or(autoPanel("Keep data for")), pad: 8 });
  await shot(page, AREA, "11-automation-retries", { clip: autoPanel("Retries of failed sends").or(autoPanel("Schedules")).or(autoPanel("Messages")), pad: 8 });
  await page.getByRole("spinbutton", { name: "Events", exact: true }).fill("30");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  const deleted = page.getByRole("dialog", { name: "What will be deleted" });
  await deleted.waitFor({ timeout: 30_000 });
  await page.setViewportSize({ width: 1440, height: 900 });
  await settle(page);
  await shot(page, AREA, "11-what-deleted", { clip: deleted, pad: 0 });
  await deleted.getByRole("button", { name: "Cancel" }).click();
  await go(page, "/settings/automation"); // drop the unsaved change

  await go(page, "/settings/audit");
  await page.locator(".audit-table tbody tr").first().waitFor();
  await page.getByLabel("Object type").selectOption({ label: "Sign-ins" });
  await settle(page, { quiet: 800 });
  await page.getByLabel("Object type").selectOption("");
  await settle(page, { quiet: 800 });
  await highlight(page.locator(".audit-export"));
  await page.setViewportSize({ width: 1440, height: 560 });
  await shot(page, AREA, "11-audit-log", { clip: page.locator(".page"), pad: 0 });
  await page.setViewportSize({ width: 1440, height: 900 });
  await clearHighlights(page);
  await page.locator(".audit-table tbody tr.clickable").first().click();
  await page.locator("tr.audit-detail").waitFor();
  await settle(page);
  await shot(page, AREA, "11-audit-detail", { clip: page.locator(".audit-table tbody tr.clickable.open, tr.audit-detail"), pad: 8 });
}

async function signIn(browser, context, page, sso) {
  await go(page, "/settings/access");
  const ssoPanel = panel(page, "Single sign-on (OIDC)");
  await field(page, "Issuer URL").fill(KEYCLOAK.issuer);
  await field(page, "Discovery URL (optional)").fill(KEYCLOAK.discovery_url);
  await field(page, "Client ID").fill(KEYCLOAK.client_id);
  await field(page, "Client secret").fill(KEYCLOAK.client_secret);
  await field(page, "Their role").selectOption("viewer");
  await ssoPanel.getByLabel("Allow signing in with SSO").check();
  const save = ssoPanel.getByRole("button", { name: "Save sign-on settings" });
  await save.click();
  await ssoPanel.locator(".badge.ok", { hasText: "On" }).waitFor();
  await settle(page, { quiet: 1500 });
  // Saved: the badge says On, and Remove single sign-on appears next to Save.
  await highlight(save);
  await highlight(ssoPanel.getByRole("button", { name: "Remove single sign-on" }));
  await shot(page, AREA, "11-sso", { clip: ssoPanel, pad: 8 });
  await clearHighlights(page);

  // The sign-in page, signed out: "Sign in with SSO" sends you to Keycloak.
  const { context: out, page: login } = await session(browser, { signIn: null });
  try {
    await go(login, "/login");
    await field(login, "Workspace ID").fill("acme-retail");
    await highlight(login.getByRole("button", { name: "Sign in with SSO" }));
    await shot(login, AREA, "11-login-sso", { clip: login.locator(".auth-form > *"), pad: 24 });
    await clearHighlights(login);
    await login.getByRole("button", { name: "Sign in with SSO" }).click();
    const reached = await login.waitForURL((u) => u.port === "8180", { timeout: 15_000 }).then(() => true, () => false);
    if (reached) {
      await settle(login, { quiet: 1000 });
      await shot(login, AREA, "11-keycloak", { clip: { x: 400, y: 240, width: 640, height: 460 } });
    } else {
      console.log(`  Keycloak not reached (now at ${login.url()}); 11-keycloak not taken`);
    }
  } finally {
    await out.close();
  }

  // Off again.
  await ssoPanel.getByLabel("Allow signing in with SSO").uncheck();
  await field(page, "Their role").selectOption(sso.default_role || "editor");
  await save.click();
  await ssoPanel.locator(".badge", { hasText: "Off" }).waitFor();

  // Identity verification and keys: shown, nothing replaced.
  await go(page, "/settings/access");
  await shot(page, AREA, "11-identity", { clip: panel(page, "Identity verification"), pad: 8 });
  const keys = panel(page, "Keys");
  await highlight(keys.getByRole("button", { name: "Replace API key" }));
  await shot(page, AREA, "11-keys", { clip: keys, pad: 8 });
  await clearHighlights(page);
}

async function deleteStep(context, page, journeys) {
  const j = await api(context, "POST", "/v1/journeys", {
    name: `Docs: erase on request ${runId()}`,
    graph: {
      trigger: { type: "event", event: "erasure_requested" }, entry: "wait1",
      nodes: { wait1: { type: "wait", seconds: 3600, next: "del1", x: 300, y: 140 }, del1: { type: "delete_contact", confirm: "DELETE", next: "exit", x: 300, y: 280 }, exit: { type: "exit", x: 300, y: 420 } },
    },
  });
  journeys.push(j.id);
  await go(page, `/journeys/${j.id}`);
  await page.locator('.react-flow__node[data-id="del1"]').click();
  const insp = page.locator("aside.inspector .insp");
  await insp.waitFor();
  await settle(page);
  await shot(page, AREA, "11-delete-step", { clip: insp, pad: 12 });
}

// A made-up authenticator key (base32), never stored anywhere: the setup answer is stood in for.
const FAKE_KEY = "PULSEDOCSEXAMPLEKEYNOTREAL234567";
const FAKE_SETUP = {
  secret: FAKE_KEY.match(/.{4}/g).join(" "),
  uri: `otpauth://totp/Pulse:docs.admin%40pulse.test%20(Acme%20Retail)?algorithm=SHA1&digits=6&issuer=Pulse&period=30&secret=${FAKE_KEY}`,
  issuer: "Pulse",
  account: "docs.admin@pulse.test (Acme Retail)",
};
const FAKE_CODES = ["k7wq2-mx9dp", "h3rtn-a8vze", "p2c6y-wq4kd", "e9mzs-t3hvb", "n5jxr-c7pfa", "u4bgk-z8qme", "y6dfw-r2nts", "s8vqa-m5kxh", "c3tpe-j9wzr", "x2hmn-b6dqu"];

async function twoStep(browser, page) {
  // Your account → Two-step sign-in: Off, then the setup with the QR code (the answer to "Set up
  // the app" stood in for, so no key is made), then the recovery codes (the turn-on stood in for).
  await page.route("**/v1/account/two-factor/setup", (r) => r.fulfill({ json: FAKE_SETUP }));
  await page.route("**/v1/account/two-factor/enable", (r) => r.fulfill({ json: { recovery_codes: FAKE_CODES } }));
  try {
    await page.setViewportSize({ width: 1440, height: 1200 });
    await go(page, "/account");
    const tf = panel(page, "Two-step sign-in");
    await highlight(page.getByRole("link", { name: "Your account" }));
    await highlight(tf.getByRole("button", { name: "Set up the app" }));
    await shot(page, AREA, "11-account", { clip: { x: 0, y: 0, width: 1440, height: 1200 } });
    await clearHighlights(page);
    await tf.getByRole("button", { name: "Set up the app" }).click();
    await tf.getByRole("button", { name: /Can't scan it/ }).click();
    await tf.getByLabel("Code from the app").fill("492039");
    await page.evaluate(() => document.activeElement?.blur());
    await settle(page);
    await shot(page, AREA, "11-account-setup", { clip: tf, pad: 8 });
    await tf.getByRole("button", { name: "Turn on two-step sign-in" }).click();
    await tf.getByText("Save these recovery codes now").waitFor();
    await page.evaluate(() => document.querySelectorAll(".toast").forEach((t) => t.remove()));
    await settle(page);
    await shot(page, AREA, "11-recovery-codes", { clip: tf, pad: 8 });
  } finally {
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await page.setViewportSize({ width: 1440, height: 900 });
  }
  const status = await page.request.get("/v1/account/two-factor").then((r) => r.json());
  if (status.enabled) throw new Error("two-step sign-in is on for the docs admin: turn it off on Your account");

  // The sign-in page's code step: the answer to the password stood in for ("a code is needed"), so
  // no password is sent.
  const { context: out, page: login } = await session(browser, { signIn: null });
  try {
    await login.route("**/v1/auth/login", (r) => r.fulfill({ json: { two_factor: "code" } }));
    await go(login, "/login");
    await field(login, "Workspace ID").fill("acme-retail");
    await field(login, "Email").fill("asha@acme-retail.test");
    await field(login, "Password").fill("not-sent-anywhere");
    await login.getByRole("button", { name: "Sign in", exact: true }).click();
    await login.getByRole("heading", { name: "Enter your code" }).waitFor();
    await login.getByLabel("Code from the app").fill("492039");
    await login.evaluate(() => document.activeElement?.blur());
    await settle(login);
    await shot(login, AREA, "11-signin-code", { clip: login.locator(".auth-form > *"), pad: 24 });
  } finally {
    await out.close();
  }

  // Settings → Sign-in & keys → Two-step sign-in: "Required for some roles or people" with the
  // Admin role ticked, on screen only (not saved).
  await page.setViewportSize({ width: 1440, height: 1600 });
  await go(page, "/settings/access");
  const policy = panel(page, "Two-step sign-in");
  await policy.getByLabel("Required for some roles or people").check();
  await policy.getByRole("checkbox", { name: "Admin", exact: true }).check();
  await settle(page);
  await shot(page, AREA, "11-two-step-policy", { clip: policy, pad: 8 });
  await go(page, "/settings/access"); // drop the unsaved choice
  await page.setViewportSize({ width: 1440, height: 900 });

  // Settings → Team: the Two-step sign-in column. Nobody in the docs workspace has it on, so the
  // list shows Priya with a password and two-step sign-in on (in the browser only) for the
  // Reset two-step button.
  await page.route("**/v1/users", async (r) => {
    if (r.request().method() !== "GET") return r.continue();
    const res = await r.fetch();
    const body = await res.json();
    for (const u of body.users) if (/^Priya/.test(u.name ?? "")) Object.assign(u, { password: true, two_factor: true });
    return r.fulfill({ response: res, json: body });
  });
  try {
    await go(page, "/settings/team");
    await highlight(page.getByRole("button", { name: "Reset two-step" }));
    await shot(page, AREA, "11-team-two-step", { clip: page.locator(".page > .panel"), pad: 8 });
    await clearHighlights(page);
  } finally {
    await page.unrouteAll({ behavior: "ignoreErrors" });
  }
}
