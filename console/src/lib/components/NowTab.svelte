<!--
@component
Operator cards, filtered and grouped by state or place, in the order the producer wrote them.
-->
<script lang="ts">
	import { filterCards, groupCards, keyed } from '#lib/data/select.ts';
	import type { FileSlot } from '#lib/data/store.svelte.ts';
	import { CARD_STATES, CARD_WHERES } from '#lib/data/types.ts';
	import type { ViewState } from '#lib/view/state.svelte.ts';
	import CardItem from './CardItem.svelte';
	import FileStatus from './FileStatus.svelte';
	import Select from './Select.svelte';
	import Skeleton from './Skeleton.svelte';

	let { slot, view }: { slot: FileSlot<'cards'>; view: ViewState } = $props();

	const any = { value: '', label: 'Any' };
	const stateOptions = [any, ...CARD_STATES.map((s) => ({ value: s, label: s }))];
	const whereOptions = [any, ...CARD_WHERES.map((w) => ({ value: w, label: w }))];
	const groupOptions = [
		{ value: 'state', label: 'State' },
		{ value: 'where', label: 'Place' },
		{ value: 'none', label: 'No grouping' }
	];

	const visible = $derived(filterCards(slot.data?.cards ?? [], view.filters));
	const order = $derived(view.filters.group === 'where' ? CARD_WHERES : CARD_STATES);
	const groups = $derived(groupCards(visible, view.filters.group, order));
</script>

<div class="filters" role="group" aria-label="Card filters">
	<Select
		id="f-state"
		label="State"
		value={view.filters.cardState}
		options={stateOptions}
		onchange={(v) => view.setFilter('cardState', v as typeof view.filters.cardState)}
	/>
	<Select
		id="f-where"
		label="Place"
		value={view.filters.cardWhere}
		options={whereOptions}
		onchange={(v) => view.setFilter('cardWhere', v as typeof view.filters.cardWhere)}
	/>
	<Select
		id="f-group"
		label="Group by"
		value={view.filters.group}
		options={groupOptions}
		onchange={(v) => view.setFilter('group', v as typeof view.filters.group)}
	/>
</div>

<FileStatus {slot} />

{#if slot.pending}
	<Skeleton label="Loading cards" />
{:else if slot.data}
	<p class="count" aria-live="polite">{visible.length} of {slot.data.cards.length} cards</p>
	{#each groups as group (group.key)}
		<section class="group" aria-labelledby={`group-${group.key}`}>
			{#if view.filters.group !== 'none'}
				<h2 id={`group-${group.key}`}>{group.key} <span>({group.items.length})</span></h2>
			{:else}
				<h2 id={`group-${group.key}`} class="sr-only">Cards</h2>
			{/if}
			<ul>
				{#each keyed(group.items, (c) => c.id) as { key, item: card } (key)}
					<li>
						<CardItem
							{card}
							uid={key}
							expanded={view.isExpanded(card.id)}
							ontoggle={(id) => view.toggleExpanded(id)}
						/>
					</li>
				{/each}
			</ul>
		</section>
	{:else}
		<p class="none">No card matches these filters.</p>
	{/each}
{/if}

<style>
	.filters {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-3);
	}

	.count,
	.none {
		color: var(--color-muted-foreground);
		font-size: 0.875rem;
	}

	h2 {
		font-size: 0.875rem;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--color-muted-foreground);
		margin: var(--space-4) 0 var(--space-2);
	}

	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: var(--space-3);
	}
</style>
