// Mounts the interactive player into every documentation figure (ADR-0015, section 4; ADR-0016,
// section 3).
//
// Each figure ships as a <picture> of committed SVGs, so a page works without this script. The
// loader watches every figure.praetor-figure[data-figure] and, when one nears the viewport, fetches
// the SVG its <img> shows (currentSrc, from the same origin and normally an HTTP-cache hit), reads
// the props from the SVG's <metadata id="figure-spec">, imports the player from ./player.js beside
// this file and replaces the <picture> with it; the caption and the text description stay. Both
// SVG variants carry the full props, steps included, so the variant the browser picked under
// reduced motion serves as well as the animated one. No file names a figure: the player is the
// same for every site.
//
// Material's navigation.instant swaps page content without a reload, so the loader runs again on
// every document$ emission, and on Astro's astro:page-load event for the same reason. Each run
// unmounts players whose host has left the page, which also closes a figure left in full screen.
import type { FlowProps } from './third_party/interfig/upstream/src/model.ts';

type Mounted = { host: HTMLElement; unmount: () => void };
type Observable = { subscribe: (next: () => void) => unknown };

/** Figures handled per page (HISS-02). */
const MAX_FIGURES = 32;
/** How long fetching one figure's SVG may take before the figure keeps its SVG. */
const FETCH_TIMEOUT_MS = 10_000;
/** The largest SVG the loader reads; the committed figures are well under 0.1 MB. */
export const MAX_SVG_BYTES = 4 * 1024 * 1024;
/**
 * The markers core.mjs (`decorate`) writes around the spec, as upstream's figure-svg.mjs does. They
 * are a copy of SPEC_OPEN and SPEC_CLOSE in core.mjs: importing core.mjs would bundle node:crypto
 * and the render engine into the browser loader. figures.test.mjs holds this reader and the `site`
 * check, which imports core.mjs's markers, to the same verdicts on every committed SVG.
 */
const SPEC_OPEN = '<metadata id="figure-spec"><![CDATA[';
const SPEC_CLOSE = ']]></metadata>';
const SELECTOR = 'figure.praetor-figure[data-figure]';

const mounted: Mounted[] = [];
let observer: IntersectionObserver | null = null;

const isObject = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

/**
 * The props embedded in a figure SVG, or an error that names the SVG (`name`, its URL when fetched)
 * and says what is wrong in the words of `svgSpecError` in checks.mjs.
 */
export function specFromSvg(svg: string, name = 'the SVG'): FlowProps {
  const start = svg.indexOf(SPEC_OPEN);
  if (start < 0) throw new Error(`${name} carries no <metadata id="figure-spec">`);
  const end = svg.indexOf(SPEC_CLOSE, start + SPEC_OPEN.length);
  if (end < 0) throw new Error(`${name} does not close its <metadata id="figure-spec">`);
  let spec: unknown;
  try {
    spec = JSON.parse(svg.slice(start + SPEC_OPEN.length, end));
  } catch (error) {
    throw new Error(`${name} embeds a figure spec that is not JSON (${error instanceof Error ? error.message : String(error)})`, { cause: error });
  }
  if (!isObject(spec) || !isObject(spec.props) || !isObject(spec.props.layout) || !Array.isArray(spec.props.edges)) {
    throw new Error(`${name} embeds a figure spec without props.layout and props.edges`);
  }
  return spec.props as FlowProps;
}

/** The body of `response` as text, refusing more than `cap` bytes before reading past them. */
export async function readCapped(response: Response, cap: number): Promise<string> {
  const declared = Number(response.headers.get('content-length') ?? 0);
  if (declared > cap) throw new Error(`the SVG is ${declared} bytes; the loader reads at most ${cap}`);
  const reader = response.body?.getReader();
  if (!reader) return '';
  const chunks: Uint8Array[] = [];
  let size = 0;
  // A non-empty chunk adds at least one byte, so cap + 1 reads bound the loop (HISS-02).
  for (let i = 0; i <= cap; i++) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > cap) {
      await reader.cancel();
      throw new Error(`the SVG exceeds ${cap} bytes; the loader reads at most ${cap}`);
    }
    chunks.push(value);
  }
  const bytes = new Uint8Array(size);
  let at = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, at);
    at += chunk.byteLength;
  }
  return new TextDecoder().decode(bytes);
}

/** Fetches `url` with the timeout and the byte cap, and reads the props from the SVG. */
export async function fetchSpec(url: string, fetcher: typeof fetch = fetch): Promise<FlowProps> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
  try {
    const response = await fetcher(url, { signal: controller.signal });
    if (!response.ok) throw new Error(`${url} answered ${response.status}`);
    return specFromSvg(await readCapped(response, MAX_SVG_BYTES), url);
  } finally {
    clearTimeout(timer);
  }
}

/** The caption's text, which names the scenario tab list; the slug when the figure has none. */
export function figureTitle(figure: HTMLElement): string {
  const caption = figure.querySelector('figcaption')?.textContent?.trim();
  return caption || (figure.dataset.figure ?? '');
}

async function mountFigure(figure: HTMLElement): Promise<void> {
  const picture = figure.querySelector('picture');
  const image = picture?.querySelector('img');
  const source = image?.currentSrc || image?.src;
  if (!picture || !source) throw new Error('the figure holds no <picture> with an <img>');
  const [props, player] = await Promise.all([fetchSpec(source), import('./player.js')]);
  const host = document.createElement('div');
  // not-content is Starlight's opt-out from its Markdown typography: without it, its rule that
  // spaces every block after a sibling (margin-top: 1rem) pushes all but the first scenario tab
  // down. Material has no such class and ignores it.
  host.className = 'praetor-figure__player not-content';
  picture.replaceWith(host);
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const unmount = player.mount(host, props, { autoplay: !reduced, label: `${figureTitle(figure)}: scenarios` });
  mounted.push({ host, unmount });
}

function onVisible(entries: IntersectionObserverEntry[]): void {
  for (const entry of entries) {
    if (!entry.isIntersecting) continue;
    const figure = entry.target as HTMLElement;
    observer?.unobserve(figure);
    figure.dataset.figureState = 'loading';
    mountFigure(figure).then(
      () => { figure.dataset.figureState = 'mounted'; },
      (error: unknown) => {
        figure.dataset.figureState = 'failed';
        console.error(`figures: ${figure.dataset.figure} kept its SVG:`, error);
      },
    );
  }
}

/** Unmounts players that left the page, then watches the figures the current page holds. */
function scan(): void {
  for (let i = mounted.length - 1; i >= 0; i--) {
    if (mounted[i].host.isConnected) continue;
    mounted[i].unmount();
    mounted.splice(i, 1);
  }
  observer ??= new IntersectionObserver(onVisible, { rootMargin: '200px 0px' });
  const figures = Array.from(document.querySelectorAll<HTMLElement>(SELECTOR)).slice(0, MAX_FIGURES);
  for (const figure of figures) {
    if (figure.dataset.figureState) continue;
    figure.dataset.figureState = 'waiting';
    observer.observe(figure);
  }
}

/** Scans now or once the document is parsed, and again after every client-side navigation. */
function start(): void {
  document.addEventListener('astro:page-load', scan);
  const documents = (window as unknown as { document$?: Observable }).document$;
  if (documents && typeof documents.subscribe === 'function') documents.subscribe(scan);
  else if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', scan);
  else scan();
}

// The tests import this module in Node for its pure functions, where there is no page to scan.
if (typeof window !== 'undefined' && typeof document !== 'undefined') start();
