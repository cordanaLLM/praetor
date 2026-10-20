<!--
@component
Open DECISION and ASK ids, oldest first, with who sent them and when.
-->
<script lang="ts">
	import { filterDecisions, keyed } from '#lib/data/select.ts';
	import type { FileSlot } from '#lib/data/store.svelte.ts';
	import { formatUtc } from '#lib/data/time.ts';
	import type { ViewState } from '#lib/view/state.svelte.ts';
	import FileStatus from './FileStatus.svelte';
	import Skeleton from './Skeleton.svelte';

	let { slot, view }: { slot: FileSlot<'status'>; view: ViewState } = $props();
	const list = $derived(filterDecisions(slot.data?.decisions ?? [], view.filters.q));
</script>

<FileStatus {slot} />

{#if slot.pending}
	<Skeleton label="Loading decisions" />
{:else if slot.data}
	<p class="count">{list.length} of {slot.data.decisions.length} open, oldest first</p>
	<ol class="rows">
		{#each keyed(list, (x) => x.id) as { key, item: d } (key)}
			<li class="row" id={`decision-${key}`}>
				<span class={`kind kind-${d.kind}`}>{d.kind}</span>
				<code class="id">{d.id}</code>
				<span class="from"><span class="label">From</span> {d.from ?? 'unknown'}</span>
				<span class="at"
					><span class="label">Sent</span> {d.at ? formatUtc(d.at) : 'unknown'}</span
				>
			</li>
		{:else}
			<li class="none">No open decision matches.</li>
		{/each}
	</ol>
{/if}

<style>
	.count,
	.none,
	.label {
		color: var(--color-muted-foreground);
		font-size: 0.875rem;
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
		grid-template-columns: auto 1fr;
		gap: var(--space-1) var(--space-3);
		align-items: baseline;
		padding: var(--space-3) var(--space-4);
		background: var(--color-card);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-card);
	}

	.from,
	.at {
		grid-column: 1 / -1;
	}

	@media (min-width: 48rem) {
		.row {
			grid-template-columns: 6rem minmax(0, 1fr) 14rem 14rem;
		}

		.from,
		.at {
			grid-column: auto;
		}
	}

	.id {
		overflow-wrap: anywhere;
	}

	.kind {
		font-size: 0.75rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		color: var(--color-primary);
	}

	.kind-ASK {
		color: var(--color-warn);
	}
</style>
