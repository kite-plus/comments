// After the Vite builds: record the loader's version for the server, publish
// it as the pinned /embed/v1.<minor>.<patch>.js, and fail the build when a
// budget of widget.md section 10 is exceeded.
import { readFileSync, writeFileSync, mkdirSync, readdirSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { join } from "node:path";

const dist = new URL("../dist/", import.meta.url).pathname;
const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url)));
const version = pkg.version;
if (!/^1\.\d+\.\d+$/.test(version)) throw new Error(`embed version ${version} is not 1.x.y`);

writeFileSync(join(dist, "build.json"), JSON.stringify({ embed_version: version }) + "\n");
mkdirSync(join(dist, "embed"), { recursive: true });
writeFileSync(join(dist, "embed", `v${version}.js`), readFileSync(join(dist, "embed.js")));

const gz = (file) => gzipSync(readFileSync(file), { level: 9 }).length;
const manifest = JSON.parse(readFileSync(join(dist, ".vite", "manifest.json")));
const app = manifest["src/app/main.tsx"];
const budgets = [
  ["embed.js", gz(join(dist, "embed.js")), 4 * 1024],
  ["app script", [app.file, ...(app.imports ?? []).map((i) => manifest[i].file)].reduce((n, f) => n + gz(join(dist, f)), 0), 40 * 1024],
  ["app css (inlined)", (app.css ?? []).reduce((n, f) => n + gz(join(dist, f)), 0), 12 * 1024],
];
let failed = false;
for (const [name, size, max] of budgets) {
  const ok = size <= max;
  failed ||= !ok;
  console.log(`${ok ? "ok  " : "FAIL"} ${name}: ${(size / 1024).toFixed(2)} KB gzip (budget ${max / 1024} KB)`);
}
console.log(`files: ${readdirSync(join(dist, "assets")).join(", ")}`);
if (failed) process.exit(1);
