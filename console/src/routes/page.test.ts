import { cleanup, render, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { stubFetch } from '../test/fixtures.ts';
import Page from './+page.svelte';

vi.mock('$app/navigation', () => ({ replaceState: vi.fn() }));

const initialUrl = location.href;

beforeEach(() => {
	window.history.replaceState({}, '', '/console?tab=graph#frontier-N1');
	vi.stubGlobal('matchMedia', () => ({
		matches: false,
		addEventListener: vi.fn(),
		removeEventListener: vi.fn()
	}));
	vi.stubGlobal('scrollTo', vi.fn());
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	window.history.replaceState({}, '', initialUrl);
});

test.each([2, 3])('the page shows one sign-in note when %s files redirect', async (count) => {
	vi.stubGlobal(
		'fetch',
		stubFetch({
			cards: { type: 'opaqueredirect' },
			graph: { type: 'opaqueredirect' },
			status: count === 3 ? { type: 'opaqueredirect' } : {}
		})
	);
	const { getByRole, getAllByRole } = render(Page);
	const note = await waitFor(() => getByRole('alert', { name: 'Sign-in required' }));
	expect(note.textContent).toMatch(/Signed out:\s*sign in again/);
	const links = getAllByRole('link', { name: 'sign in again' });
	expect(links).toHaveLength(1);
	expect(links[0]?.getAttribute('href')).toBe(location.href.split('#')[0]);
	expect(links[0]?.hasAttribute('data-sveltekit-reload')).toBe(true);
	const header = getByRole('banner');
	expect(within(header).getByRole('alert', { name: 'Sign-in required' })).toBe(note);
	expect(note.compareDocumentPosition(getByRole('navigation', { name: 'Sections' })) & 4).toBe(4);
});

test('the page shows no sign-in note when all files load', async () => {
	vi.stubGlobal('fetch', stubFetch({ cards: {}, graph: {}, status: {} }));
	const { findByText, queryByRole } = render(Page);
	await findByText(/^Generated /);
	expect(queryByRole('alert', { name: 'Sign-in required' })).toBeNull();
});
