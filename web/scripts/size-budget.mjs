// Frontend load budgets from PERFORMANCE.md section 2, asserted against the
// production build. It measures exactly what index.html pulls in on first paint
// (the modulepreloaded JS entry graph and the linked CSS), brotli-compressed,
// because that is the critical path. Font and timing budgets are not here yet:
// fonts are still 303 KiB unsubsetted (target 60 KiB) and timing needs CDP
// throttling; both are tracked in next_steps.md.
//
// Usage: pnpm build && node scripts/size-budget.mjs
import { readFileSync, existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { constants, brotliCompressSync } from 'node:zlib';

const ROOT = new URL('..', import.meta.url).pathname;
const BUILD = join(ROOT, 'build');

const BUDGETS_KIB = { js: 80, css: 20 };
// A lazy route chunk is not on the critical path, but it is the only way a
// single screen can regress by importing a heavy dependency (the rich-text
// editor is the first). The largest non-preloaded node chunk is bounded.
const ROUTE_BUDGET_KIB = 40;

function brotli(bytes) {
	return brotliCompressSync(bytes, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }).length;
}

const html = readFileSync(join(BUILD, 'index.html'), 'utf8');
const seen = new Set();
const assets = [];
// SvelteKit emits "/_app/..." or, with relative paths, "./_app/..."; accept both
// and normalise, so a config change cannot make the scan match nothing.
const ref = /["'](?:\.\/|\/)(_app\/immutable\/[^"']+\.(?:js|css))["']/g;
for (let m = ref.exec(html); m; m = ref.exec(html)) {
	if (!seen.has(m[1])) {
		seen.add(m[1]);
		assets.push(`/${m[1]}`);
	}
}

// An empty scan would report 0 KiB and pass every budget; that is a broken
// check, not a fast page.
if (!assets.some((a) => a.endsWith('.js'))) {
	console.error('size-budget: found no critical-path JS in index.html; the scan is broken');
	process.exit(1);
}

const totals = { js: 0, css: 0 };
for (const asset of assets) {
	const file = join(BUILD, asset.replace(/^\//, ''));
	if (!existsSync(file)) {
		console.error(`size-budget: index.html references a missing file: ${asset}`);
		process.exit(1);
	}
	const kind = asset.endsWith('.js') ? 'js' : 'css';
	totals[kind] += brotli(readFileSync(file));
}

let failed = false;
for (const [kind, budgetKiB] of Object.entries(BUDGETS_KIB)) {
	const gotKiB = totals[kind] / 1024;
	const ok = gotKiB <= budgetKiB;
	failed ||= !ok;
	console.log(
		`${ok ? 'ok  ' : 'FAIL'} critical-path ${kind}: ${gotKiB.toFixed(1)} KiB (budget ${budgetKiB} KiB, brotli)`
	);
}

// The largest lazy route chunk, so a screen that pulls in a heavy dependency has
// to face a budget instead of only the critical path passing.
const nodesDir = join(BUILD, '_app/immutable/nodes');
let largest = { name: '', bytes: 0 };
if (existsSync(nodesDir)) {
	for (const file of readdirSync(nodesDir)) {
		if (!file.endsWith('.js')) continue;
		const asset = `/_app/immutable/nodes/${file}`;
		if (assets.includes(asset)) continue; // already counted on the critical path
		const size = brotli(readFileSync(join(nodesDir, file)));
		if (size > largest.bytes) largest = { name: file, bytes: size };
	}
}
if (largest.name) {
	const gotKiB = largest.bytes / 1024;
	const ok = gotKiB <= ROUTE_BUDGET_KIB;
	failed ||= !ok;
	console.log(
		`${ok ? 'ok  ' : 'FAIL'} largest route chunk ${largest.name}: ${gotKiB.toFixed(1)} KiB (budget ${ROUTE_BUDGET_KIB} KiB, brotli)`
	);
}

if (failed) {
	console.error('size-budget: the build exceeds PERFORMANCE.md section 2 (or update the budget with a note)');
	process.exit(1);
}
