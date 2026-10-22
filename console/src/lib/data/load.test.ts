import { expect, test, vi } from 'vitest';
import { fixture, stubFetch } from '../../test/fixtures.ts';
import { loadFile } from './load.ts';
import { validateFile } from './validate.ts';

test('every committed fixture loads and passes the schema', async () => {
	const fetchImpl = stubFetch({ cards: {}, graph: {}, status: {} });
	for (const name of ['cards', 'graph', 'status'] as const) {
		const result = await loadFile(name, { fetchImpl });
		expect(result.ok, name).toBe(true);
	}
});

test('requests revalidate cached data and expose redirects with an abort signal', async () => {
	const fetchImpl = vi.fn(stubFetch({ graph: {} }));
	await loadFile('graph', { fetchImpl });
	expect(fetchImpl).toHaveBeenCalledWith('./graph.json', {
		cache: 'no-cache',
		redirect: 'manual',
		signal: expect.any(AbortSignal)
	});
});

test.each([500, 503])('an HTTP %s error names the file and the status', async (status) => {
	const result = await loadFile('cards', {
		fetchImpl: stubFetch({ cards: { status, headers: { 'content-type': 'text/html' } } })
	});
	expect(result).toEqual({
		ok: false,
		error: { kind: 'http', message: `cards.json answered HTTP ${status}` }
	});
});

test.each([
	{ type: 'opaqueredirect' as const },
	{ status: 401 },
	{ status: 403 },
	{ raw: '<html>Sign in</html>', headers: { 'content-type': 'text/html' } },
	{ raw: '<html>Sign in</html>', headers: { 'content-type': 'TEXT/HTML; charset=UTF-8' } }
])('an expired session is reported for %j', async (reply) => {
	const result = await loadFile('graph', { fetchImpl: stubFetch({ graph: reply }) });
	expect(result).toEqual({
		ok: false,
		error: { kind: 'signin', message: 'graph.json: the session ended, sign in again' }
	});
});

test('a network failure is reported, not thrown', async () => {
	const fetchImpl = stubFetch({ graph: { error: new TypeError('Failed to fetch') } });
	const result = await loadFile('graph', { fetchImpl });
	expect(result.ok).toBe(false);
	if (!result.ok) expect(result.error.kind).toBe('network');
});

test('a file that never answers times out', async () => {
	const fetchImpl = stubFetch({ status: { hang: true } });
	const result = await loadFile('status', { fetchImpl, timeoutMs: 20 });
	expect(result.ok).toBe(false);
	if (!result.ok) expect(result.error.message).toBe('status.json did not arrive within 20 ms');
});

test.each(['application/json', 'text/plain', 'text/htmlish'])(
	'text that is not JSON with content-type %s is a parse error',
	async (contentType) => {
		const result = await loadFile('cards', {
			fetchImpl: stubFetch({
				cards: { raw: '{"version": 1,', headers: { 'content-type': contentType } }
			})
		});
		expect(result.ok).toBe(false);
		if (!result.ok) {
			expect(result.error.kind).toBe('parse');
			expect(result.error.message).toMatch(/^cards\.json is not JSON: /);
		}
	}
);

test('a schema violation names the field', async () => {
	const bad = fixture('cards');
	(bad.cards[0] as unknown as Record<string, unknown>).state = 'soon';
	const result = await loadFile('cards', { fetchImpl: stubFetch({ cards: { body: bad } }) });
	expect(result.ok).toBe(false);
	if (result.ok) return;
	expect(result.error.kind).toBe('schema');
	expect(result.error.problems?.map((p) => p.path)).toContain('/cards/0/state');
	expect(result.error.message).toContain('/cards/0/state');
});

test('the browser validators refuse what the Go schema test refuses', () => {
	const graph = { ...fixture('graph'), version: 2 };
	const status = { ...fixture('status'), generated_at: 'yesterday' };
	const cards = fixture('cards');
	(cards.cards[0] as unknown as Record<string, unknown>).owner_email = 'x';
	expect(validateFile('graph', graph).map((p) => p.path)).toContain('/version');
	expect(validateFile('status', status).map((p) => p.path)).toContain('/generated_at');
	expect(validateFile('cards', cards).map((p) => p.path)).toContain('/cards/0');
});

test('graph validation requires repository counts, accepts zero and an optional frontier repo', () => {
	const graph = fixture('graph');
	expect(validateFile('graph', graph)).toEqual([]);
	expect(validateFile('graph', { ...graph, repos: [] })).toEqual([]);
	const missing: Record<string, unknown> = { ...graph };
	delete missing.repos;
	expect(validateFile('graph', missing).map((p) => p.message)).toContain(
		"must have required property 'repos'"
	);
	for (const items of [-1, 0.5, '0']) {
		expect(
			validateFile('graph', { ...graph, repos: [{ repo: 'example/alpha', items }] }).map(
				(p) => p.path
			)
		).toContain('/repos/0/items');
	}
	const row = { id: 'N1', title: 'Ready', status: 'ready', score: 1 };
	expect(validateFile('graph', { ...graph, frontier: [row] })).toEqual([]);
	expect(
		validateFile('graph', { ...graph, frontier: [{ ...row, repo: 1 }] }).map((p) => p.path)
	).toContain('/frontier/0/repo');
});

test('graph requires done fields and accepts an empty recent list with 14 trend days', () => {
	const graph = fixture('graph');
	expect(validateFile('graph', { ...graph, done_recent: [] })).toEqual([]);
	for (const key of ['done_recent', 'done_trend'] as const) {
		const missing: Record<string, unknown> = { ...graph };
		delete missing[key];
		expect(validateFile('graph', missing).map((p) => p.message)).toContain(
			`must have required property '${key}'`
		);
	}
	for (const length of [13, 15]) {
		const done_trend = Array.from({ length }, () => graph.done_trend[0]);
		expect(validateFile('graph', { ...graph, done_trend }).map((p) => p.path)).toContain(
			'/done_trend'
		);
	}
	const done_recent = Array.from({ length: 100 }, () => graph.done_recent[0]);
	expect(validateFile('graph', { ...graph, done_recent })).toEqual([]);
	expect(
		validateFile('graph', { ...graph, done_recent: [...done_recent, done_recent[0]] }).map(
			(p) => p.path
		)
	).toContain('/done_recent');
});

test('done activity validation rejects invalid dates, counts and extra properties', () => {
	const graph = fixture('graph');
	for (const invalid of [
		{ id: '' },
		{ done_at: 'yesterday' },
		{ done_at: '2026-01-15T09:00:00+01:00' },
		{ repo: 1 },
		{ unexpected: true }
	]) {
		expect(
			validateFile('graph', { ...graph, done_recent: [{ ...graph.done_recent[0], ...invalid }] })
				.length
		).toBeGreaterThan(0);
	}
	for (const invalid of [
		{ day: 'today' },
		{ done: 0.5 },
		{ extra: 1 },
		{ closed: -1 },
		{ reopened: -1 },
		{ closed: 0.5 }
	]) {
		expect(
			validateFile('graph', {
				...graph,
				done_trend: [{ ...graph.done_trend[0], ...invalid }, ...graph.done_trend.slice(1)]
			}).length
		).toBeGreaterThan(0);
	}
	expect(
		validateFile('graph', {
			...graph,
			done_trend: [
				{ ...graph.done_trend[0], done: -1, closed: 1, reopened: 2 },
				...graph.done_trend.slice(1)
			]
		})
	).toEqual([]);
});

test('boundaries: empty lists pass, one past maxItems fails', () => {
	const empty = { ...fixture('cards'), cards: [] };
	expect(validateFile('cards', empty)).toEqual([]);
	const card = fixture('cards').cards[0];
	const full = {
		...empty,
		cards: Array.from({ length: 500 }, (_, i) => ({ ...card, id: `C${i}` }))
	};
	expect(validateFile('cards', full)).toEqual([]);
	const over = { ...full, cards: [...full.cards, card] };
	expect(validateFile('cards', over).map((p) => p.path)).toContain('/cards');
});

test('a long list of problems is cut short in the message', async () => {
	const bad = fixture('cards');
	for (const c of bad.cards) (c as unknown as Record<string, unknown>).state = 'soon';
	const result = await loadFile('cards', { fetchImpl: stubFetch({ cards: { body: bad } }) });
	if (result.ok) throw new Error('expected a schema error');
	expect(result.error.message).toMatch(/\(and \d+ more\)$/);
});
