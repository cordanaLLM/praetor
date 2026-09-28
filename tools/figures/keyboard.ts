// Keyboard access for the interfig scenario tabs (ADR-0015, section 6).
//
// Upstream's tabs are native buttons with role="tab", so Tab reaches every one of them but the
// arrow keys do nothing and the tab list has no name. This shim adds the WAI-ARIA tabs pattern's
// roving tabindex on top, from outside the vendored source: Left and Right move to the previous
// and next tab, Home and End to the first and last, and moving activates the tab. Retire it once
// an upstream sync brings the same behaviour in (tools/figures/third_party/interfig/VENDOR.md).

/** Tabs per tab list the shim will handle; interfig figures are capped at 12 steps. */
const MAX_TABS = 64;

/** Where focus moves from `index` for `key`, or -1 when the key is not a tab-list key. */
export function nextTab(key: string, index: number, count: number): number {
  if (count <= 0 || index < 0) return -1;
  switch (key) {
    case 'ArrowRight':
      return (index + 1) % count;
    case 'ArrowLeft':
      return (index - 1 + count) % count;
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    default:
      return -1;
  }
}

function tabsOf(list: Element): HTMLElement[] {
  return Array.from(list.querySelectorAll<HTMLElement>('[role="tab"]')).slice(0, MAX_TABS);
}

/** Labels every tab list under `host` and leaves only the selected tab in the Tab order. */
export function syncTabs(host: HTMLElement, label: string): void {
  for (const list of Array.from(host.querySelectorAll('[role="tablist"]')).slice(0, MAX_TABS)) {
    if (!list.hasAttribute('aria-label')) list.setAttribute('aria-label', label);
    const tabs = tabsOf(list);
    const selected = tabs.findIndex((tab) => tab.getAttribute('aria-selected') === 'true');
    tabs.forEach((tab, i) => {
      const tabIndex = i === (selected === -1 ? 0 : selected) ? '0' : '-1';
      if (tab.getAttribute('tabindex') !== tabIndex) tab.setAttribute('tabindex', tabIndex);
    });
  }
}

/** Adds roving arrow-key focus to the tabs under `host`; the returned function removes it. */
export function enhanceTabs(host: HTMLElement, label: string): () => void {
  const onKey = (event: KeyboardEvent) => {
    const tab = (event.target as Element | null)?.closest?.('[role="tab"]');
    const list = tab?.closest('[role="tablist"]');
    if (!tab || !list) return;
    const tabs = tabsOf(list);
    const next = nextTab(event.key, tabs.indexOf(tab as HTMLElement), tabs.length);
    if (next === -1) return;
    event.preventDefault();
    tabs[next].focus();
    tabs[next].click();
  };
  const observer = new MutationObserver(() => syncTabs(host, label));
  observer.observe(host, { subtree: true, childList: true, attributes: true, attributeFilter: ['aria-selected'] });
  host.addEventListener('keydown', onKey);
  syncTabs(host, label);
  return () => {
    observer.disconnect();
    host.removeEventListener('keydown', onKey);
  };
}
