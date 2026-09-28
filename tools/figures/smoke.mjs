#!/usr/bin/env node
// Proves every figure on a built site mounts the interactive player (ADR-0015, section 7).
//
//   node smoke.mjs [--site <dir>] [--base <path>] [--require-browser]
//
// The site is served from 127.0.0.1 on a free port by serve.mjs, because module scripts do not
// load from file://, under --base: the path the site is built for ('/' by default; an Astro site
// built with base '/docs/' is served under /docs/). Each page that holds a figure.praetor-figure is
// opened in Chromium twice:
// once normally, where every figure must mount, and every figure with scenario tabs must advance
// its active step on its own (autoplay) and then show a packet, under autoplay or after starting
// one of its scenario tabs; and once under prefers-reduced-motion, where every figure must mount
// and no packet may show. A page error or a console error fails the run.
//
// The figures load from the site's own origin, so every request to another origin is blocked and
// a console error that another origin raises does not count (`onSite`): a theme's
// repository widget asking api.github.com about a placeholder repository, or a web font, neither
// fails the run nor reaches the network.
//
// Without an installed Chromium the run exits 0 and says it skipped (HISS-21), unless
// --require-browser is given, as the Pages workflow does.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { basePath, serve } from './serve.mjs';

const ROOT = fileURLToPath(new URL('../../', import.meta.url));
const MAX_FILES = 20_000;
const MAX_PAGES = 256;
const PAGE_TIMEOUT_MS = 30_000;
/** How long autoplay gets to advance every figure's active step without any input. */
const PACKET_TIMEOUT_MS = 8_000;
/** How long one scenario tab gets to show a packet once started, and how many tabs are tried. */
const TAB_PACKET_TIMEOUT_MS = 3_000;
const MAX_TABS = 48;
const POLL_MS = 100;
const SETTLE_MS = 2_000;
const MAX_SCROLL_STEPS = 400;
/**
 * A built figure: a <figure> whose class list holds praetor-figure, with the attribute value in
 * double quotes, single quotes, or bare, as an HTML minifier writes it (htmlmin's
 * remove_optional_attribute_quotes, which mkdocs-minify-plugin turns on).
 */
const MARKER = /<figure\s[^>]*?\bclass=(?:"(?:[^"]*\s)?praetor-figure(?:\s[^"]*)?"|'(?:[^']*\s)?praetor-figure(?:\s[^']*)?'|praetor-figure(?=[\s/>]))/;

/** Whether a built page holds a figure.praetor-figure. */
export function holdsFigure(html) {
  return MARKER.test(html);
}

/** Built pages that hold a figure, as site-relative URL paths; the walk is iterative and bounded. */
export function figurePages(site) {
  const pages = [];
  const stack = [site];
  for (let seen = 0; stack.length > 0; seen++) {
    if (seen >= MAX_FILES) throw new Error(`${site} holds more than ${MAX_FILES} entries`);
    const path = stack.pop();
    if (statSync(path).isDirectory()) {
      for (const name of readdirSync(path)) stack.push(join(path, name));
    } else if (path.endsWith('index.html') && holdsFigure(readFileSync(path, 'utf8'))) {
      pages.push(`/${relative(site, path).split(sep).join('/').replace(/index\.html$/, '')}`);
    }
  }
  if (pages.length > MAX_PAGES) throw new Error(`more than ${MAX_PAGES} pages hold figures`);
  return pages.sort();
}

async function scrollThrough(page) {
  await page.evaluate(async (steps) => {
    for (let i = 0; i < steps && window.scrollY + window.innerHeight < document.body.scrollHeight; i++) {
      window.scrollBy(0, Math.max(200, window.innerHeight / 2));
      await new Promise((done) => setTimeout(done, 60));
    }
  }, MAX_SCROLL_STEPS);
}

/**
 * What each mounted player shows now: its figure, its scenario tab count, the selected tab, how far
 * the selected tab's progress line has run (upstream scales it with scaleX as the step clock runs)
 * and how many packets are visible. A paused player still shows the first beat's packets at the
 * start of their edges, so a packet proves no motion; only the selected tab or its progress line
 * moving shows the step clock running.
 */
const playerStates = () =>
  Array.from(document.querySelectorAll('.praetor-figure .interfig')).map((player) => {
    const tabs = Array.from(player.querySelectorAll('[role="tab"]'));
    const selected = tabs.findIndex((tab) => tab.getAttribute('aria-selected') === 'true');
    const scale = /scaleX\(([^)]+)\)/.exec(tabs[selected]?.querySelector('div')?.style.transform ?? '');
    const packets = Array.from(player.querySelectorAll('svg g')).filter(
      (g) => g.querySelector('circle[r="4.5"]') && g.style.opacity === '1',
    ).length;
    const slug = player.closest('.praetor-figure')?.getAttribute('data-figure') ?? '?';
    return { slug, tabs: tabs.length, selected, progress: scale ? Number(scale[1]) : Number.NaN, packets };
  });

/**
 * Whether `url` belongs to the served site: it is on `origin`'s origin, or it is empty or does not
 * parse, so a console error without a usable source still counts. The run blocks every request for
 * which this is false, and a console error whose source it is false for is that blocked request.
 */
export function onSite(url, origin) {
  if (!url) return true;
  try {
    return new URL(url).origin === new URL(origin).origin;
  } catch {
    return true;
  }
}

/** Whether a player's active step advanced between two readings: another tab, or more progress. */
export function stepAdvanced(first, now) {
  if (!first || !now || first.selected === -1 || now.selected === -1) return false;
  return now.selected !== first.selected || now.progress > first.progress;
}

/** Reads every player each POLL_MS for up to `ms`, handing each reading to `observe` until it returns true. */
async function poll(page, ms, observe) {
  const deadline = Date.now() + ms;
  for (let i = 0; i <= Math.ceil(ms / POLL_MS); i++) {
    if (observe(await page.evaluate(playerStates))) return true;
    if (Date.now() >= deadline) return false;
    await page.waitForTimeout(POLL_MS);
  }
  return false;
}

/**
 * Under autoplay alone, with no input, every player with scenario tabs must advance its active
 * step within PACKET_TIMEOUT_MS. A packet seen meanwhile is recorded; the returned list holds, per
 * player, whether it advanced and whether it showed a packet.
 */
async function watchAutoplay(page, first) {
  const seen = first.map((state) => ({ advanced: state.tabs === 0, packet: state.tabs === 0 }));
  await poll(page, PACKET_TIMEOUT_MS, (states) => {
    states.forEach((state, n) => {
      if (!seen[n]) return;
      seen[n].advanced ||= stepAdvanced(first[n], state);
      seen[n].packet ||= state.packets > 0;
    });
    return seen.every((flags) => flags.advanced);
  });
  return seen;
}

/**
 * Whether player `n` shows a packet after starting each of its scenario tabs in turn. A figure whose
 * opening scenarios only light boxes (lattice-join: four of them, about 14 s at its speed) shows its
 * first packet long after the autoplay wait; a tab starts its scenario from the first beat at once.
 */
async function packetAfterTabs(page, n, tabs) {
  const tab = page.locator('.praetor-figure .interfig').nth(n).locator('[role="tab"]');
  for (let i = 0; i < tabs && i < MAX_TABS; i++) {
    await tab.nth(i).click({ timeout: PAGE_TIMEOUT_MS });
    if (await poll(page, TAB_PACKET_TIMEOUT_MS, (states) => states[n]?.packets > 0)) return true;
  }
  return false;
}

/** Findings for the players on one page under autoplay: autoplay first, then packets. */
async function motionErrors(page) {
  const first = await page.evaluate(playerStates);
  const seen = await watchAutoplay(page, first);
  const errors = [];
  for (const [n, state] of first.entries()) {
    if (!seen[n].advanced) {
      errors.push(`${state.slug}: autoplay did not advance the active step within ${PACKET_TIMEOUT_MS} ms`);
    } else if (!seen[n].packet && !(await packetAfterTabs(page, n, state.tabs))) {
      errors.push(`${state.slug}: no packet showed, under autoplay or after starting any scenario tab`);
    }
  }
  return errors;
}

/** Findings for one page in one motion mode. */
async function checkPage(context, origin, path, reduced) {
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(`page error: ${error.message}`));
  page.on('console', (message) => message.type() === 'error' && onSite(message.location().url, origin) &&
    errors.push(`console error: ${message.text()}`));
  try {
    await page.goto(origin + path, { waitUntil: 'load', timeout: PAGE_TIMEOUT_MS });
    const expected = await page.locator('figure.praetor-figure').count();
    await scrollThrough(page);
    await page.waitForFunction((n) => document.querySelectorAll('.praetor-figure .interfig').length >= n, expected,
      { timeout: PAGE_TIMEOUT_MS }).catch(() => undefined);
    const mounted = await page.locator('.praetor-figure .interfig').count();
    if (mounted !== expected) errors.push(`${mounted} of ${expected} figure(s) mounted the player`);
    if (reduced) {
      await page.waitForTimeout(SETTLE_MS);
      const packets = (await page.evaluate(playerStates)).reduce((sum, state) => sum + state.packets, 0);
      if (packets) errors.push(`${packets} packet(s) moving under prefers-reduced-motion`);
    } else {
      errors.push(...(await motionErrors(page)));
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

async function smoke(site, base, requireBrowser) {
  if (!existsSync(join(site, 'index.html'))) throw new Error(`${site} holds no built site; build the site first`);
  const pages = figurePages(site);
  if (pages.length === 0) throw new Error(`no page under ${site} holds a figure.praetor-figure`);
  const browser = await launch(requireBrowser);
  if (!browser) return [];
  const server = await serve(site, base);
  const origin = `http://127.0.0.1:${server.address().port}${basePath(base).slice(0, -1)}`;
  const errors = [];
  try {
    for (const reduced of [false, true]) {
      const context = await browser.newContext({ reducedMotion: reduced ? 'reduce' : 'no-preference' });
      await context.route((url) => !onSite(url.href, origin), (route) => route.abort('blockedbyclient'));
      for (const path of pages) errors.push(...(await checkPage(context, origin, path, reduced)));
      await context.close();
    }
  } finally {
    await browser.close();
    server.close();
  }
  if (!errors.length) console.log(`figures smoke: ${pages.length} page(s) mount every figure, with and without reduced motion`);
  return errors;
}

const OPTIONS = { site: { type: 'string', default: 'site' }, base: { type: 'string', default: '/' }, 'require-browser': { type: 'boolean', default: false } };

/** The parsed command line, or null when it is not `[--site DIR] [--base PATH] [--require-browser]`. */
export function parseCommandLine(argv) {
  try {
    const { values } = parseArgs({ args: argv, options: OPTIONS, strict: true, allowPositionals: false });
    return values.site ? { site: resolve(ROOT, values.site), base: basePath(values.base), requireBrowser: values['require-browser'] } : null;
  } catch {
    return null;
  }
}

export async function main(argv) {
  const options = parseCommandLine(argv);
  if (!options) {
    console.error('usage: node smoke.mjs [--site <dir>] [--base <path>] [--require-browser]');
    return 2;
  }
  const errors = await smoke(options.site, options.base, options.requireBrowser);
  for (const error of errors) console.error(`figures smoke: ${error}`);
  return errors.length ? 1 : 0;
}

if (import.meta.main ?? (process.argv[1] !== undefined && fileURLToPath(import.meta.url) === resolve(process.argv[1]))) {
  main(process.argv.slice(2)).then(
    (code) => { process.exitCode = code; },
    (error) => { console.error(`figures smoke: ${error.message}`); process.exitCode = 2; },
  );
}
