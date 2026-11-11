# Documentation screenshots

Every picture in `docs/tutorials/` and `docs/developer/` is taken from the running app by a script,
so anyone can retake them after the UI changes. The scripts use [Playwright](https://playwright.dev)
in its official Docker image; the host needs only Docker and a running Pulse (`make up`).

```bash
make up              # Pulse at http://localhost:7080
make docs-shots      # every script (runs make docs-seed first, see below)
make docs-shots SHOT=01            # scripts whose name starts with 01
make docs-shots SHOT=01,dev-01     # several, comma-separated
```

`make docs-shots`:

1. runs `make docs-seed`, which creates the **Acme Retail** workspace (`acme-retail`) the first time
   and does nothing after that (credentials: [deploy/dev-seed.md](../../deploy/dev-seed.md)). Once
   the scripts have saved a signed-in session in `.cache/docs-shots/sessions/`, this step is skipped:
   Pulse allows 10 failed sign-ins per workspace and email in 15 minutes, and reusing the session keeps the runs quick. After resetting the
   database, run `make docs-seed` yourself;
2. starts `mcr.microsoft.com/playwright:v1.49.1-jammy` with `--network host`, so the browser reaches
   Pulse at `http://localhost:7080` and Mailpit at `http://localhost:18025`. The container is only a
   client: nothing listens on the host network;
3. installs the `playwright` npm package of the same version (`PLAYWRIGHT_VERSION` in the Makefile)
   into `.cache/docs-shots` once (git-ignored), then runs `run.mjs`, which runs each matching script
   in `shots/` with a fresh browser and stops with an error if any fails.

Pictures are written to `docs/<area>/img/<name>.png`, overwriting the old ones. Review them
(`git diff --stat docs/*/img`, then look) before committing.

To use another Pulse, override the variables: `make docs-shots PULSE_URL=http://localhost:7081
DOCS_WORKSPACE=… DOCS_EMAIL=… DOCS_PASSWORD=…`. To upgrade Playwright, change `PLAYWRIGHT_VERSION`
(the image tag and the npm package follow it).

## Files

| File | What it is |
|---|---|
| `run.sh` | Container entry point: installs the npm package if needed, runs `run.mjs` |
| `run.mjs` | Runs `shots/*.mjs` (filtered by `$SHOT`), one browser per script |
| `lib.mjs` | Shared helpers (below) |
| `shots/NN-<guide>.mjs` | One script per tutorial, e.g. `01-getting-started.mjs` → `docs/tutorials/img/01-*.png` |
| `shots/dev-NN-<guide>.mjs` | One script per developer guide, e.g. `dev-01-installation.mjs` → `docs/developer/img/dev-01-*.png` |

## Writing a script

Add a new file rather than editing someone else's: scripts don't share state, so several people (or
agents) can work on different guides at once. A script exports one function that gets a Playwright
`Browser`:

```js
// shots/03-segments.mjs: screenshots for docs/tutorials/03-segments.md
import { clearHighlights, go, highlight, session, shot } from "../lib.mjs";

const AREA = "tutorials";

export default async function (browser) {
  const { context, page } = await session(browser);          // signed in to acme-retail
  await go(page, "/segments");
  await highlight(page.getByRole("button", { name: "New segment" }));
  await shot(page, AREA, "03-segments-list");                   // docs/tutorials/img/03-segments-list.png
  await clearHighlights(page);
  await shot(page, AREA, "03-segment-card", { clip: page.locator(".panel").first() });
  await context.close();
}
```

Name pictures after the script that takes them, so they sort with their guide: `NN-<what it shows>.png`
for a tutorial (`03-segments-list.png` from `03-segments.mjs`) and `dev-NN-<what it shows>.png` for a
developer guide (`dev-04-snippet.png` from `dev-04-web-sdk.mjs`). Embed them with a relative path:
`![What the reader sees](img/03-segments-list.png)`.

### Helpers in `lib.mjs`

| Helper | Does |
|---|---|
| `session(browser, { size, signIn })` | A new browser context and page. `size`: `"desktop"` (1440×900, the default) or `"mobile"` (390×844). `signIn`: the docs workspace by default (`DOCS`), other `{workspace, email, password}`, or `null` for signed out |
| `login(context, creds)` | Signs in through the API (no form). `session()` already does this, once per workspace and email: the session cookie is kept in `.cache/docs-shots/sessions/` and reused while it works |
| `api(context, method, path, body)` | Calls the Pulse API with the context's session: set up data a shot needs |
| `go(page, path)` | Opens an app path and waits until it has settled |
| `settle(page, { quiet })` | Waits for network idle, fonts and loading placeholders, then `quiet` ms |
| `until(fn, { timeout, what })` | Polls until `fn()` is truthy, e.g. a contact has arrived |
| `highlight(locator)` / `clearHighlights(page)` | An orange "click here" outline over an element |
| `maskSecrets(page, selectors)` | Replaces server API keys (`pk_…`) and integration keys (`pzk_…`) on the page, plus anything matching `selectors`, with dots |
| `shot(page, area, name, { clip, pad, fullPage })` | Saves `docs/<area>/img/<name>.png`. `clip`: a locator (the box around every element it matches, plus `pad` px) or a rectangle |
| `runId()` | A short unique suffix for names and emails a script creates |
| `BASE`, `DOCS`, `VIEWPORTS` | The Pulse URL, the docs workspace's credentials, the window sizes |

### Rules

- **No secrets in pictures.** Call `maskSecrets(page)` before any shot that may show a server API key
  (the sign-up "workspace ready" screen, Settings → Sign-in & keys after **Replace API key**, Zapier
  connectors). Signing secrets, client secrets and provider credentials stay hidden unless you click
  "Show"; don't. Site keys (`sk_…`) are public and may appear.
- **Only test values.** Use the docs workspace, or create throwaway data with `runId()` in names and
  `.test` / `example.com` addresses. Never type a real password, token or key.
- **Keep files small.** Prefer a `clip` around the part the text talks about over the whole window;
  desktop shots are 1440×900 at scale 1. For a tall panel (a drawer, a long form), make the window
  taller with `page.setViewportSize({ width: 1440, height: 2000 })` and clip the panel.
- **Wait for real data**, don't sleep blindly: `until()` on the API, or wait for the text you expect.
  The pipeline is asynchronous (events reach contacts and segments within seconds).
- **Don't change the docs workspace for good.** Open forms and fill them in for a picture, then
  **Cancel**; if a guide needs to save something, give it a `runId()` name. Never touch `demo-retail`.
- Scripts that sign up create a new workspace on every run (as tutorial 1 does); that's expected in
  a local database.

After retaking screenshots or editing the guides, run `make docs-check`: it checks every link,
image and heading anchor in `docs/tutorials` and `docs/developer` and lists screenshots nothing uses.
