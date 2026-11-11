// Runs the capture scripts in shots/ (all, or those whose name starts with one of the
// comma-separated prefixes in $SHOT), one fresh browser each. Exits non-zero if any fails.
import { readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { BASE, launch } from "./lib.mjs";

const dir = join(dirname(fileURLToPath(import.meta.url)), "shots");
const only = (process.env.SHOT || "").split(",").map((s) => s.trim()).filter(Boolean);
const scripts = readdirSync(dir)
  .filter((f) => f.endsWith(".mjs") && (only.length === 0 || only.some((p) => f.startsWith(p))))
  .sort();
if (scripts.length === 0) {
  console.error(`no capture scripts in shots/ match SHOT=${process.env.SHOT}`);
  process.exit(1);
}

const health = await fetch(`${BASE}/healthz`).catch((e) => e);
if (health instanceof Error) {
  console.error(`Pulse isn't reachable at ${BASE} (${health.message}). Start it with \`make up\`.`);
  process.exit(1);
}

let failed = 0;
for (const f of scripts) {
  console.log(`${f}`);
  const browser = await launch();
  try {
    const mod = await import(pathToFileURL(join(dir, f)).href);
    await mod.default(browser);
  } catch (err) {
    failed++;
    console.error(`  FAILED: ${err.stack || err}`);
  } finally {
    await browser.close();
  }
}
console.log(failed ? `${failed} of ${scripts.length} scripts failed` : `done: ${scripts.length} script(s)`);
process.exit(failed ? 1 : 0);
