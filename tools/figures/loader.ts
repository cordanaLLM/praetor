// Mounts the interactive player into every documentation figure (ADR-0015, section 4).
//
// Each figure ships as a <picture> of committed SVGs, so a page works without this script. The
// loader watches every figure.praetor-figure[data-figure] and, when one nears the viewport, imports
// the player chunk and the figure's spec chunk and replaces the <picture> with the player; the
// caption and the text description stay. Material's navigation.instant swaps page content without
// a reload, so the loader runs again on every document$ emission and unmounts players whose host
// has left the page, which also closes a figure left in full screen.

type Mounted = { host: HTMLElement; unmount: () => void };
type Registry = Record<string, string>;
type Observable = { subscribe: (next: () => void) => unknown };

/** Figures handled per page (HISS-02). */
const MAX_FIGURES = 32;
/** How long the registry fetch may take before the figure keeps its SVG. */
const FETCH_TIMEOUT_MS = 10_000;
const SELECTOR = 'figure.praetor-figure[data-figure]';

const mounted: Mounted[] = [];
let registry: Promise<Registry> | null = null;
let observer: IntersectionObserver | null = null;

async function fetchRegistry(): Promise<Registry> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
  try {
    const response = await fetch(new URL('registry.json', import.meta.url), { signal: controller.signal });
    if (!response.ok) throw new Error(`figures: registry.json answered ${response.status}`);
    return (await response.json()) as Registry;
  } finally {
    clearTimeout(timer);
  }
}

async function mountFigure(figure: HTMLElement): Promise<void> {
  const slug = figure.dataset.figure ?? '';
  // A failed or timed-out fetch is not cached: the next figure, or the next page after instant
  // navigation, asks again instead of keeping its SVG until a full reload.
  registry ??= fetchRegistry().catch((error: unknown) => {
    registry = null;
    throw error;
  });
  const chunk = (await registry)[slug];
  const picture = figure.querySelector('picture');
  if (!chunk || !picture) return;
  const [player, spec] = await Promise.all([import('./player.tsx'), import(new URL(chunk, import.meta.url).href)]);
  const host = document.createElement('div');
  host.className = 'praetor-figure__player';
  picture.replaceWith(host);
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const unmount = player.mount(host, spec.default.props, { autoplay: !reduced, label: `${spec.default.title}: scenarios` });
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

const documents = (window as unknown as { document$?: Observable }).document$;
if (documents && typeof documents.subscribe === 'function') documents.subscribe(scan);
else if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', scan);
else scan();
