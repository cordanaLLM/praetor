#!/usr/bin/env node
// Proves every figure on a built site mounts the interactive player (ADR-0015, section 7).
//
//   node smoke.mjs [--site <dir>] [--require-browser]
//
// The site is served from 127.0.0.1 on a free port by node:http, because module scripts do not
// load from file://. Each page that holds a figure.praetor-figure is opened in Chromium twice:
// once normally, where every figure must mount and a figure with scenario tabs must move a packet,
// and once under prefers-reduced-motion, where every figure must mount and no packet may show.
// A page error or a console error fails the run.
//
// Without an installed Chromium the run exits 0 and says it skipped (HISS-21), unless
// --require-browser is given, as the Pages workflow does.
import { createServer } from 'node:http';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { extname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = fileURLToPath(new URL('../../', import.meta.url));
const MAX_FILES = 20_000;
const MAX_PAGES = 256;
const PAGE_TIMEOUT_MS = 30_000;
const PACKET_TIMEOUT_MS = 8_000;
/** How long one scenario tab gets to move a packet once started, and how many tabs are tried. */
const TAB_PACKET_TIMEOUT_MS = 3_000;
const MAX_TABS = 48;
const SETTLE_MS = 2_000;
const MAX_SCROLL_STEPS = 400;
const TYPES = {
  '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8',
  '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png', '.ico': 'image/x-icon',
  '.xml': 'application/xml', '.txt': 'text/plain; charset=utf-8', '.woff2': 'font/woff2',
};
const MARKER = 'class="praetor-figure"';

/** Built pages that hold a figure, as site-relative URL paths; the walk is iterative and bounded. */
export function figurePages(site) {
  const pages = [];
  const stack = [site];
  for (let seen = 0; stack.length > 0; seen++) {
    if (seen >= MAX_FILES) throw new Error(`${site} holds more than ${MAX_FILES} entries`);
    const path = stack.pop();
    if (statSync(path).isDirectory()) {
      for (const name of readdirSync(path)) stack.push(join(path, name));
    } else if (path.endsWith('index.html') && readFileSync(path, 'utf8').includes(MARKER)) {
      pages.push(`/${relative(site, path).split(sep).join('/').replace(/index\.html$/, '')}`);
    }
  }
  if (pages.length > MAX_PAGES) throw new Error(`more than ${MAX_PAGES} pages hold figures`);
  return pages.sort();
}

/** A static file server for `site` on 127.0.0.1 with bounded request timeouts. */
export function serve(site) {
  const root = resolve(site);
  const server = createServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://127.0.0.1');
    let file = resolve(root, `.${decodeURIComponent(url.pathname)}`);
    if (file !== root && !file.startsWith(root + sep)) return void response.writeHead(403).end();
    if (existsSync(file) && statSync(file).isDirectory()) file = join(file, 'index.html');
    if (!existsSync(file)) return void response.writeHead(404).end();
    response.writeHead(200, { 'content-type': TYPES[extname(file)] ?? 'application/octet-stream' });
    response.end(readFileSync(file));
  });
  server.requestTimeout = 10_000;
  server.headersTimeout = 5_000;
  return new Promise((resolveServer, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => resolveServer(server));
  });
}

async function scrollThrough(page) {
  await page.evaluate(async (steps) => {
    for (let i = 0; i < steps && window.scrollY + window.innerHeight < document.body.scrollHeight; i++) {
      window.scrollBy(0, Math.max(200, window.innerHeight / 2));
      await new Promise((done) => setTimeout(done, 60));
    }
  }, MAX_SCROLL_STEPS);
}

const visiblePackets = () =>
  Array.from(document.querySelectorAll('.praetor-figure .interfig svg g')).filter(
    (g) => g.querySelector('circle[r="4.5"]') && g.style.opacity === '1',
  ).length;

/**
 * Whether a packet moves on the page: first under autoplay, then after starting each scenario tab
 * in turn. A figure whose opening scenarios only light boxes (lattice-join: four of them, about
 * 14 s at its speed) moves its first packet long after the autoplay wait, and its player is still
 * sound; a tab starts its scenario from the first beat at once.
 */
async function packetMoved(page, tabs) {
  const moved = (ms) => page.waitForFunction(visiblePackets, undefined, { timeout: ms }).then(() => true, () => false);
  if (await moved(PACKET_TIMEOUT_MS)) return true;
  const tab = page.locator('.praetor-figure [role="tab"]');
  for (let i = 0; i < tabs && i < MAX_TABS; i++) {
    await tab.nth(i).click({ timeout: PAGE_TIMEOUT_MS });
    if (await moved(TAB_PACKET_TIMEOUT_MS)) return true;
  }
  return false;
}

/** Findings for one page in one motion mode. */
async function checkPage(context, base, path, reduced) {
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(`page error: ${error.message}`));
  page.on('console', (message) => message.type() === 'error' && errors.push(`console error: ${message.text()}`));
  try {
    await page.goto(base + path, { waitUntil: 'load', timeout: PAGE_TIMEOUT_MS });
    const expected = await page.locator('figure.praetor-figure').count();
    await scrollThrough(page);
    await page.waitForFunction((n) => document.querySelectorAll('.praetor-figure .interfig').length >= n, expected,
      { timeout: PAGE_TIMEOUT_MS }).catch(() => undefined);
    const mounted = await page.locator('.praetor-figure .interfig').count();
    if (mounted !== expected) errors.push(`${mounted} of ${expected} figure(s) mounted the player`);
    const tabs = await page.locator('.praetor-figure [role="tab"]').count();
    if (reduced) {
      await page.waitForTimeout(SETTLE_MS);
      const packets = await page.evaluate(visiblePackets);
      if (packets) errors.push(`${packets} packet(s) moving under prefers-reduced-motion`);
    } else if (tabs && !(await packetMoved(page, tabs))) {
      errors.push('no packet moved within the timeout, under autoplay or after starting any scenario tab');
    }
  } finally {
    await page.close();
  }
  return errors.map((error) => `${path}${reduced ? ' (reduced motion)' : ''}: ${error}`);
}

async function launch(requireBrowser) {
  const { chromium } = await import('playwright');
  if (existsSync(chromium.executablePath())) return chromium.launch({ timeout: PAGE_TIMEOUT_MS });
  const reason = `no Chromium at ${chromium.executablePath()}; install it with: npx --prefix tools/figures playwright install chromium`;
  if (requireBrowser) throw new Error(reason);
  console.log(`figures smoke: skipped: ${reason}`);
  return null;
}

async function smoke(site, requireBrowser) {
  if (!existsSync(join(site, 'index.html'))) throw new Error(`${site} holds no built site; run mkdocs build first`);
  const pages = figurePages(site);
  if (pages.length === 0) throw new Error(`no page under ${site} holds a figure.praetor-figure`);
  const browser = await launch(requireBrowser);
  if (!browser) return [];
  const server = await serve(site);
  const base = `http://127.0.0.1:${server.address().port}`;
  const errors = [];
  try {
    for (const reduced of [false, true]) {
      const context = await browser.newContext({ reducedMotion: reduced ? 'reduce' : 'no-preference' });
      for (const path of pages) errors.push(...(await checkPage(context, base, path, reduced)));
      await context.close();
    }
  } finally {
    await browser.close();
    server.close();
  }
  if (!errors.length) console.log(`figures smoke: ${pages.length} page(s) mount every figure, with and without reduced motion`);
  return errors;
}

export async function main(argv) {
  const siteAt = argv.indexOf('--site');
  const site = resolve(ROOT, siteAt === -1 ? 'site' : (argv[siteAt + 1] ?? ''));
  const known = new Set(['--site', '--require-browser', ...(siteAt === -1 ? [] : [argv[siteAt + 1]])]);
  if (argv.some((arg) => !known.has(arg)) || (siteAt !== -1 && !argv[siteAt + 1])) {
    console.error('usage: node smoke.mjs [--site <dir>] [--require-browser]');
    return 2;
  }
  const errors = await smoke(site, argv.includes('--require-browser'));
  for (const error of errors) console.error(`figures smoke: ${error}`);
  return errors.length ? 1 : 0;
}

if (import.meta.main ?? (process.argv[1] !== undefined && fileURLToPath(import.meta.url) === resolve(process.argv[1]))) {
  main(process.argv.slice(2)).then(
    (code) => { process.exitCode = code; },
    (error) => { console.error(`figures smoke: ${error.message}`); process.exitCode = 2; },
  );
}
