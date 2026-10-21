import { cleanup, fireEvent, render, within } from '@testing-library/svelte';
import { afterEach, expect, test } from 'vitest';
import { fixture, stubFetch } from '../../test/fixtures.ts';
import { FileSlot } from '../data/store.svelte.ts';
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
