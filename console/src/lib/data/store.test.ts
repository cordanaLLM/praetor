import { expect, test } from 'vitest';
import { fixture, stubFetch } from '../../test/fixtures.ts';
import { ConsoleData, FileSlot } from './store.svelte.ts';

test('a slot is pending until its first answer, then holds the data', async () => {
	const slot = new FileSlot('cards');
	expect(slot.pending).toBe(true);
	await slot.refresh({ fetchImpl: stubFetch({ cards: {} }) }, () => 1000);
	expect(slot.pending).toBe(false);
	expect(slot.data?.cards.length).toBe(fixture('cards').cards.length);
	expect(slot.lastGoodAt).toBe(1000);
	expect(slot.error).toBeUndefined();
});

test('a failed refresh keeps the last good data and its time beside the error', async () => {
	const fetchImpl = stubFetch({ graph: (n) => (n === 1 ? {} : { status: 500 }) });
	const slot = new FileSlot('graph');
	await slot.refresh({ fetchImpl }, () => 1000);
	const good = slot.data;
	await slot.refresh({ fetchImpl }, () => 2000);
	expect(slot.data).toBe(good);
	expect(slot.lastGoodAt).toBe(1000);
	expect(slot.error?.kind).toBe('http');
});

test('a good answer after an error clears it', async () => {
	const fetchImpl = stubFetch({ status: (n) => (n === 1 ? { raw: 'nope' } : {}) });
	const slot = new FileSlot('status');
	await slot.refresh({ fetchImpl });
	expect(slot.error?.kind).toBe('parse');
	expect(slot.pending).toBe(false);
	await slot.refresh({ fetchImpl });
	expect(slot.error).toBeUndefined();
	expect(slot.data).toBeDefined();
});

test('an older, slower answer never overwrites a newer one', async () => {
	const older = { ...fixture('cards'), generated_at: '2026-01-01T00:00:00Z' };
	let release: () => void = () => {};
	const gate = new Promise<void>((r) => (release = r));
	const fast = stubFetch({ cards: {} });
	const slow = (async (...args: Parameters<typeof fetch>) => {
		await gate;
		return stubFetch({ cards: { body: older } })(...args);
	}) as typeof fetch;
	const slot = new FileSlot('cards');
	const first = slot.refresh({ fetchImpl: slow });
	await slot.refresh({ fetchImpl: fast });
	release();
	await first;
	expect(slot.data?.generated_at).toBe(fixture('cards').generated_at);
});

test('one bad file leaves the other files untouched', async () => {
	const data = new ConsoleData({
		fetchImpl: stubFetch({ cards: {}, graph: { status: 404 }, status: {} })
	});
	await data.refresh();
	expect(data.cards.error).toBeUndefined();
	expect(data.status.error).toBeUndefined();
	expect(data.graph.error?.kind).toBe('http');
	expect(data.signinRequired).toBe(false);
});

test.each(['cards', 'graph', 'status'] as const)(
	'a sign-in error in %s requires sign-in until a successful refresh',
	async (name) => {
		const data = new ConsoleData({
			fetchImpl: stubFetch({
				cards: {},
				graph: {},
				status: {},
				[name]: (n: number) => (n === 1 ? { type: 'opaqueredirect' } : {})
			})
		});
		expect(data.signinRequired).toBe(false);
		await data.refresh();
		expect(data.signinRequired).toBe(true);
		await data.refresh();
		expect(data.signinRequired).toBe(false);
	}
);
