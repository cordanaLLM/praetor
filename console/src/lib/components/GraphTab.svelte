<!--
@component
Plan graph: totals, quick wins (gain, effort, repository) and the ready frontier in rank order,
filtered by class, goal, status and repository and by the search box.
-->
<script lang="ts">
	import { distinct, filterFrontier, filterQuickWins, keyed } from '#lib/data/select.ts';
	import type { FileSlot } from '#lib/data/store.svelte.ts';
	import type { Filters, ViewState } from '#lib/view/state.svelte.ts';
	import FileStatus from './FileStatus.svelte';
	import GraphDone from './GraphDone.svelte';
	import Select from './Select.svelte';
	import Skeleton from './Skeleton.svelte';
	import TotalsBars from './TotalsBars.svelte';

	let { slot, view }: { slot: FileSlot<'graph'>; view: ViewState } = $props();

	const frontierAll = $derived(slot.data?.frontier ?? []);
	const winsAll = $derived(slot.data?.quick_wins ?? []);
	const wins = $derived(filterQuickWins(winsAll, view.filters));
	const frontier = $derived(filterFrontier(frontierAll, view.filters));

	function options(values: string[]) {
		return [{ value: '', label: 'Any' }, ...values.map((v) => ({ value: v, label: v }))];
	}

	const menus = $derived([
		{ key: 'gClass', label: 'Class', values: options(distinct(frontierAll, (f) => f.class)) },
		{ key: 'gGoal', label: 'Goal', values: options(distinct(frontierAll, (f) => f.goal)) },
		{
			key: 'gStatus',
			label: 'Status',
			values: options(distinct(frontierAll, (f) => f.status))
		},
		{
			key: 'gRepo',
			label: 'Repository',
			values: [
				{ value: '', label: 'Any' },
				...(slot.data?.repos ?? []).map((r) => ({
					value: r.repo,
					label: `${r.repo} (${r.items})`
				}))
			]
		}
	] as const);
</script>

<FileStatus {slot} />

{#if slot.pending}
	<Skeleton label="Loading the plan graph" />
{:else if slot.data}
	<section aria-labelledby="totals-h">
		<h2 id="totals-h">Totals</h2>
		<TotalsBars totals={slot.data.totals} unestimated={slot.data.unestimated} />
	</section>

	<GraphDone recent={slot.data.done_recent} trend={slot.data.done_trend} />

	<div class="filters" role="group" aria-label="Graph filters">
		{#each menus as m (m.key)}
			<Select
				id={`f-${m.key}`}
				label={m.label}
				value={view.filters[m.key as keyof Filters] as string}
				options={m.values}
				onchange={(v) => view.setFilter(m.key, v)}
			/>
		{/each}
	</div>

	<section aria-labelledby="wins-h">
		<h2 id="wins-h">Quick wins ({wins.length} of {winsAll.length})</h2>
		<ul class="rows">
			{#each keyed(wins, (w) => w.id) as { key, item: w } (key)}
				<li class="row" id={`win-${key}`}>
					<span class="title">{w.title}</span>
					<span class="meta">
						<span>Gain {w.gain}/5</span><span>Effort {w.effort}/5</span>
						<span>{w.kind}</span>{#if w.repo}<span>{w.repo}</span>{/if}
					</span>
				</li>
			{:else}
				<li class="none">No quick win matches.</li>
			{/each}
		</ul>
	</section>

	<section aria-labelledby="frontier-h">
		<h2 id="frontier-h">Ready frontier ({frontier.length} of {frontierAll.length}), by rank</h2>
		<ol class="rows">
			{#each keyed(frontier, (f) => f.id) as { key, item: f } (key)}
				<li class="row" id={`frontier-${key}`}>
					<span class="title">{f.title}</span>
					<span class="meta">
						<span>Score {f.score}</span><span>{f.status}</span>
						{#if f.class}<span>{f.class}</span>{/if}{#if f.goal}<span>{f.goal}</span
							>{/if}
					</span>
				</li>
			{:else}
				<li class="none">No frontier item matches.</li>
			{/each}
		</ol>
	</section>
{/if}

<style>
	h2 {
		font-size: 1rem;
		margin: var(--space-4) 0 var(--space-2);
	}

	.filters {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-3);
		margin-block: var(--space-4) 0;
	}

	.rows {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: var(--space-2);
	}

	.row {
		display: grid;
		gap: var(--space-1);
		padding: var(--space-3) var(--space-4);
		background: var(--color-card);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-card);
	}

	.title {
		font-weight: 600;
	}

	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		font-size: 0.8125rem;
		color: var(--color-muted-foreground);
	}

	.none {
		color: var(--color-muted-foreground);
		font-size: 0.875rem;
	}
</style>
