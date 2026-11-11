// Checks markdown links/images/anchors in given dirs; lists unreferenced images in <dir>/img.
import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { join, resolve, dirname } from "node:path";
const root = process.argv[2];
const dirs = process.argv.slice(3);
const slug = (h) => h.trim().toLowerCase().replace(/<[^>]+>/g, "").replace(/[`*_~]/g, (c) => c === "_" ? "_" : "").replace(/[^\p{L}\p{N}\s_-]/gu, "").replace(/\s/g, "-");
const anchorsCache = new Map();
function anchors(file) {
  if (anchorsCache.has(file)) return anchorsCache.get(file);
  const set = new Set(); const seen = new Map(); let inCode = false;
  for (const line of readFileSync(file, "utf8").split("\n")) {
    if (/^\s*(```|~~~)/.test(line)) { inCode = !inCode; continue; }
    if (inCode) continue;
    const m = /^#{1,6}\s+(.*?)\s*#*\s*$/.exec(line);
    if (m) { let s = slug(m[1]); const n = seen.get(s) || 0; seen.set(s, n + 1); set.add(n ? `${s}-${n}` : s); }
    for (const a of line.matchAll(/<a\s+(?:name|id)="([^"]+)"/g)) set.add(a[1]);
  }
  anchorsCache.set(file, set); return set;
}
let bad = 0; const referenced = new Set(); let count = 0;
for (const d of dirs) {
  const dir = resolve(root, d);
  for (const f of readdirSync(dir).filter((f) => f.endsWith(".md"))) {
    const file = join(dir, f); let inCode = false; let ln = 0;
    for (const line of readFileSync(file, "utf8").split("\n")) {
      ln++;
      if (/^\s*(```|~~~)/.test(line)) { inCode = !inCode; continue; }
      if (inCode) continue;
      const stripped = line.replace(/`[^`]*`/g, "");
      for (const m of stripped.matchAll(/!?\[(?:[^\]]|\][^(])*?\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)/g)) {
        const url = m[1]; count++;
        if (/^(https?:|mailto:|tel:)/.test(url)) continue;
        const [p, h] = url.split("#");
        const target = p ? resolve(dirname(file), decodeURIComponent(p)) : file;
        if (!existsSync(target)) { console.log(`MISSING ${d}/${f}:${ln} -> ${url}`); bad++; continue; }
        referenced.add(target);
        if (h !== undefined) {
          if (!target.endsWith(".md")) continue;
          if (!anchors(target).has(h)) { console.log(`BAD ANCHOR ${d}/${f}:${ln} -> ${url}`); bad++; }
        }
      }
      for (const m of stripped.matchAll(/<img[^>]+src="([^"]+)"/g)) {
        const target = resolve(dirname(file), m[1]); count++;
        if (!existsSync(target)) { console.log(`MISSING ${d}/${f}:${ln} -> ${m[1]}`); bad++; } else referenced.add(target);
      }
    }
  }
  const img = join(dir, "img");
  if (existsSync(img)) for (const f of readdirSync(img)) if (!referenced.has(join(img, f))) console.log(`UNREFERENCED ${d}/img/${f}`);
}
console.log(`${count} links checked, ${bad} problems`);
process.exit(bad ? 1 : 0);
