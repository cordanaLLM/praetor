// A live update replaces the data; the open tab, filters and expanded cards must survive it.
import { fireEvent, render, screen } from '@testing-library/svelte';

import { flushSync } from 'svelte';
import { expect, test } from 'vitest';
import { fixture, stubFetch } from '../test/fixtures.ts';
import NowTab from './components/NowTab.svelte';
import { FileSlot } from './data/store.svelte.ts';
import type { CardsFile } from './data/types.ts';
import { safeStorage } from './view/storage.ts';
import { ViewState } from './view/state.svelte.ts';

function toggleOf(container: HTMLElement, id: string): HTMLButtonElement {
	const button = container.querySelector<HTMLButtonElement>(
		`[data-card-id="${id}"] button[aria-expanded]`
	);
	if (!button) throw new Error(`no card ${id}`);
	return button;
}

function nextVersion(): CardsFile {
	const next = fixture('cards');
	next.generated_at = '2026-01-15T10:30:00Z';
	next.cards = next.cards.map((c) => ({ ...c, say: `${c.say} (updated)` }));
	next.cards.push({
		id: 'C999',
		title: 'A card that arrived live',
		state: 'now',
		where: 'home',
		act: 'look',
		say: 'seen'
	});
	return next;
}

test('expanded cards, filters and the tab survive a live data update', async () => {
	const fetchImpl = stubFetch({ cards: (n) => (n === 1 ? {} : { body: nextVersion() }) });
	const slot = new FileSlot('cards');
	await slot.refresh({ fetchImpl });
	const view = new ViewState({ storage: safeStorage(() => null) });
	view.restore(new URLSearchParams('tab=now&where=anywhere'));
	const { container } = render(NowTab, { slot, view });

	const toggle = toggleOf(container, 'C1');
	await fireEvent.click(toggle);
	expect(toggle.getAttribute('aria-expanded')).toBe('true');
	const before = toggle;

	await slot.refresh({ fetchImpl });
	flushSync();

	const after = toggleOf(container, 'C1');
	expect(after).toBe(before); // same DOM node: nothing was re-created
	expect(after.getAttribute('aria-expanded')).toBe('true');
	expect(container.querySelector('[data-card-id="C1"] .say')?.textContent).toContain(
		'review done (updated)'
	);
	expect(view.tab).toBe('now');
	expect(view.filters.cardWhere).toBe('anywhere');
	expect((screen.getByLabelText('Place') as HTMLSelectElement).value).toBe('anywhere');
	expect(screen.queryByText('A card that arrived live')).toBeNull(); // filtered out (home)
});

test('a failed update keeps the cards on screen with an inline error', async () => {
	const fetchImpl = stubFetch({ cards: (n) => (n === 1 ? {} : { status: 500 }) });
	const slot = new FileSlot('cards');
	await slot.refresh({ fetchImpl });
	const view = new ViewState({ storage: safeStorage(() => null) });
	const { container } = render(NowTab, { slot, view });
	await slot.refresh({ fetchImpl });
	flushSync();
	expect(screen.getByRole('alert').textContent).toContain('cards.json answered HTTP 500');
	expect(screen.getByRole('alert').textContent).toContain('Showing the last good data');
	expect(toggleOf(container, 'C1')).toBeTruthy();
});

test('before the first answer a skeleton shows, never an empty list', () => {
	const slot = new FileSlot('cards');
	const view = new ViewState({ storage: safeStorage(() => null) });
	render(NowTab, { slot, view });
	expect(screen.getByTestId('skeleton')).toBeTruthy();
});
