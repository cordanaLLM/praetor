import { cleanup, fireEvent, render, within } from '@testing-library/svelte';
import { afterEach, expect, test } from 'vitest';
import { fixture, stubFetch } from '../../test/fixtures.ts';
import { FileSlot } from '../data/store.svelte.ts';
import type { GraphFile } from '../data/types.ts';
import { ViewState } from '../view/state.svelte.ts';
import { safeStorage } from '../view/storage.ts';
import GraphTab from './GraphTab.svelte';

afterEach(cleanup);

async function graphTab(graph = fixture('graph')) {
	const slot = new FileSlot('graph');
	await slot.refresh({ fetchImpl: stubFetch({ graph: { body: graph } }) });
	const view = new ViewState({ storage: safeStorage(() => null) });
	return { ...render(GraphTab, { slot, view }), graph, view };
}

test('Repository options include every graph repository with counts in file order', async () => {
	const { getByLabelText, graph } = await graphTab();
	const select = getByLabelText('Repository') as HTMLSelectElement;
	expect(Array.from(select.options, (o) => ({ value: o.value, label: o.textContent }))).toEqual([
		{ value: '', label: 'Any' },
		...graph.repos.map((r) => ({ value: r.repo, label: `${r.repo} (${r.items})` }))
	]);
	const withoutWin = graph.repos.find(
		(r) => r.items > 0 && !graph.quick_wins.some((w) => w.repo === r.repo)
	);
	expect(withoutWin).toBeDefined();
	expect(within(select).getByRole('option', { name: 'example/eta (8)' })).toBeTruthy();
	expect(within(select).getByRole('option', { name: 'example/theta (0)' })).toBeTruthy();
});

test('done activity renders fixture entries newest first, with local times and 14 UTC days', async () => {
	const { getByRole, graph } = await graphTab();
	expect(getByRole('heading', { name: 'Done in the last 24 hours' })).toBeTruthy();
	const recent = within(getByRole('list', { name: 'Recently done nodes' })).getAllByRole(
		'listitem'
	);
	expect(recent).toHaveLength(graph.done_recent.length);
	expect(graph.done_recent.length).toBeGreaterThan(1);
	expect(recent.map((row) => row.querySelector('code')?.textContent)).toEqual(
		[...graph.done_recent].sort((a, b) => b.done_at.localeCompare(a.done_at)).map((node) => node.id)
	);
	graph.done_recent.forEach((node, i) => {
		const row = recent[i];
		if (!row) throw new Error(`Missing done node ${node.id}`);
		expect(within(row).getByText(node.title)).toBeTruthy();
		if (node.repo) expect(within(row).getByText(node.repo)).toBeTruthy();
		const time = row.querySelector('time');
		expect(time?.getAttribute('datetime')).toBe(node.done_at);
		expect(time?.textContent).toBe(new Date(node.done_at).toLocaleString());
		if (!node.repo) expect(row.querySelectorAll('.meta > span')).toHaveLength(0);
	});
	const trend = within(getByRole('list', { name: 'Done per UTC day' })).getAllByRole('listitem');
	expect(trend).toHaveLength(14);
	expect(graph.done_trend.some((day) => day.done === 0)).toBe(true);
	graph.done_trend.forEach((day, i) => {
		const row = trend[i];
		if (!row) throw new Error(`Missing trend day ${day.day}`);
		expect(within(row).getByText(day.day)).toBeTruthy();
		expect(within(row).getByText(`${day.done} done`)).toBeTruthy();
	});
});

test('an empty done list has an explicit message and keeps all 14 trend days', async () => {
	const { getByRole } = await graphTab({ ...fixture('graph'), done_recent: [] });
	const recent = within(getByRole('region', { name: 'Done in the last 24 hours' }));
	expect(recent.getByText('No nodes went done in the last 24 hours.')).toBeTruthy();
	expect(recent.queryByRole('time')).toBeNull();
	expect(
		within(getByRole('list', { name: 'Done per UTC day' })).getAllByRole('listitem')
	).toHaveLength(14);
});

test('selecting a repository without quick wins filters frontier rows by their own repo', async () => {
	const { getByLabelText, container, graph, view } = await graphTab();
	const select = getByLabelText('Repository');
	await fireEvent.change(select, { target: { value: 'example/eta' } });
	expect(view.filters.gRepo).toBe('example/eta');
	expect(Array.from(container.querySelectorAll('li[id^="frontier-"]'), (row) => row.id)).toEqual(
		graph.frontier.filter((r) => r.repo === 'example/eta').map((r) => `frontier-${r.id}`)
	);
	expect(container.querySelectorAll('li[id^="win-"]')).toHaveLength(0);
	await fireEvent.change(select, { target: { value: 'example/theta' } });
	expect(container.querySelectorAll('li[id^="frontier-"]')).toHaveLength(0);
	await fireEvent.change(select, { target: { value: '' } });
	expect(container.querySelectorAll('li[id^="frontier-"]')).toHaveLength(graph.frontier.length);
});

test('TotalsBars: share math with planted totals (bar width equals share, not max ratio)', async () => {
	const plantedGraph: GraphFile = {
		...fixture('graph'),
		totals: {
			waiting: { nodes: 100, units: 993 },
			active: { nodes: 200, units: 4000 },
			ready: { nodes: 400, units: 10000 },
			blocked: { nodes: 40, units: 0 },
			done: { nodes: 1917, units: 26007 }
		}
	};
	const { getByRole } = await graphTab(plantedGraph);
	const totalsList = getByRole('list', { name: 'Plan graph totals' });
	const rows = within(totalsList).getAllByRole('listitem');
	const doneRow = rows.find((r) => r.querySelector('.name')?.textContent === 'done');
	if (!doneRow) throw new Error('Missing done row');

	expect(
		within(doneRow).getByText('1917 of 2657 nodes (72 %) · 26007 of 41000 units (63 %)')
	).toBeTruthy();

	const fillNodes =
		doneRow.querySelector('.fill-nodes') ?? doneRow.querySelectorAll('rect.fill')[0];
	const fillUnits =
		doneRow.querySelector('.fill-units') ?? doneRow.querySelectorAll('rect.fill')[1];
	if (!fillNodes || !fillUnits) throw new Error('Missing fill nodes or units');

	const nodeWidth = Number(fillNodes.getAttribute('width'));
	const unitWidth = Number(fillUnits.getAttribute('width'));

	expect(nodeWidth).toBeCloseTo((1917 / 2657) * 100, 2);
	expect(nodeWidth).not.toBe(100);
	expect(unitWidth).toBeCloseTo((26007 / 41000) * 100, 2);
});

test('TotalsBars: zero totals produce 0 width and 0 % without NaN', async () => {
	const zeroGraph: GraphFile = {
		...fixture('graph'),
		totals: {
			waiting: { nodes: 0, units: 0 },
			active: { nodes: 0, units: 0 },
			ready: { nodes: 0, units: 0 },
			blocked: { nodes: 0, units: 0 },
			done: { nodes: 0, units: 0 }
		}
	};
	const { getByRole } = await graphTab(zeroGraph);
	const totalsList = getByRole('list', { name: 'Plan graph totals' });
	const rows = within(totalsList).getAllByRole('listitem');
	for (const row of rows) {
		expect(within(row).getByText('0 of 0 nodes (0 %) · 0 of 0 units (0 %)')).toBeTruthy();
		const fills = row.querySelectorAll('rect.fill');
		expect(fills.length).toBeGreaterThanOrEqual(2);
		fills.forEach((fill) => {
			const width = fill.getAttribute('width');
			expect(width).not.toContain('NaN');
			expect(Number(width)).toBe(0);
		});
	}
});

test('GraphDone: trend row with closes and reopens renders +closed / -reopened = net with matching aria-label', async () => {
	const base = fixture('graph');
	const first = base.done_trend[0];
	if (!first) throw new Error('Missing first day');
	const trendGraph: GraphFile = {
		...base,
		done_trend: [{ day: first.day, closed: 10, reopened: 4, done: 6 }, ...base.done_trend.slice(1)]
	};
	const { getByRole } = await graphTab(trendGraph);
	const trendList = getByRole('list', { name: 'Done per UTC day' });
	const rows = within(trendList).getAllByRole('listitem');
	const firstRow = rows[0];
	if (!firstRow) throw new Error('Missing first row');
	expect(within(firstRow).getByText('+10 / -4 = 6')).toBeTruthy();
	const label = '+10 / -4 = 6';
	const ariaLabel =
		firstRow.getAttribute('aria-label') ??
		firstRow.querySelector('span')?.getAttribute('aria-label');
	expect(ariaLabel).toBe(label);
});

test('GraphDone: older file without closed renders done alone', async () => {
	const { getByRole, graph } = await graphTab();
	const trendList = getByRole('list', { name: 'Done per UTC day' });
	const rows = within(trendList).getAllByRole('listitem');
	const firstDay = graph.done_trend[0];
	if (!firstDay) throw new Error('Missing first day');
	const firstRow = rows[0];
	if (!firstRow) throw new Error('Missing first row');
	expect(within(firstRow).getByText(`${firstDay.done} done`)).toBeTruthy();
	const label = `${firstDay.done} done`;
	const ariaLabel =
		firstRow.getAttribute('aria-label') ??
		firstRow.querySelector('span')?.getAttribute('aria-label');
	expect(ariaLabel).toBe(label);
});

test('GraphDone: negative net is marked below 0', async () => {
	const base = fixture('graph');
	const first = base.done_trend[0];
	if (!first) throw new Error('Missing first day');
	const negativeGraph: GraphFile = {
		...base,
		done_trend: [{ day: first.day, closed: 2, reopened: 5, done: -3 }, ...base.done_trend.slice(1)]
	};
	const { getByRole } = await graphTab(negativeGraph);
	const trendList = getByRole('list', { name: 'Done per UTC day' });
	const rows = within(trendList).getAllByRole('listitem');
	const firstRow = rows[0];
	if (!firstRow) throw new Error('Missing first row');
	expect(within(firstRow).getByText('+2 / -5 = -3')).toBeTruthy();
	expect(
		firstRow.classList.contains('negative') || firstRow.querySelector('.negative')
	).toBeTruthy();
	const label = '+2 / -5 = -3';
	const ariaLabel =
		firstRow.getAttribute('aria-label') ??
		firstRow.querySelector('span')?.getAttribute('aria-label');
	expect(ariaLabel).toBe(label);
});
