<!--
@component
Status totals as small horizontal bars (inline SVG, no chart library). Two bars per status row:
share of all nodes and share of all units in the whole plan. The numbers are text, so
the bars are decoration for sighted readers and the list reads the same without them.
-->
<script lang="ts">
	import { TOTAL_KEYS, type GraphFile } from '#lib/data/types.ts';

	let { totals, unestimated }: { totals: GraphFile['totals']; unestimated: number } = $props();
	const rows = $derived(
		TOTAL_KEYS.map((key) => ({
			key,
			nodes: totals[key]?.nodes ?? 0,
			units: totals[key]?.units ?? 0
		}))
	);
	const totalNodes = $derived(rows.reduce((sum, r) => sum + r.nodes, 0));
	const totalUnits = $derived(rows.reduce((sum, r) => sum + r.units, 0));
</script>

<ul class="totals" aria-label="Plan graph totals">
	{#each rows as row (row.key)}
		{@const nodeShare = totalNodes > 0 ? row.nodes / totalNodes : 0}
		{@const unitShare = totalUnits > 0 ? row.units / totalUnits : 0}
		{@const nodePct = totalNodes > 0 ? Math.round(nodeShare * 100) : 0}
		{@const unitPct = totalUnits > 0 ? Math.round(unitShare * 100) : 0}
		{@const nodeWidth = nodeShare * 100}
		{@const unitWidth = unitShare * 100}
		<li>
			<span class="name">{row.key}</span>
			<div class="bars">
				<svg
					class="bar-nodes"
					viewBox="0 0 100 6"
					preserveAspectRatio="none"
					aria-hidden="true"
					focusable="false"
				>
					<rect class="track" x="0" y="0" width="100" height="6" rx="2" />
					<rect
						class="fill fill-nodes"
						data-kind="nodes"
						x="0"
						y="0"
						width={nodeWidth}
						height="6"
						rx="2"
					/>
				</svg>
				<svg
					class="bar-units"
					viewBox="0 0 100 6"
					preserveAspectRatio="none"
					aria-hidden="true"
					focusable="false"
				>
					<rect class="track" x="0" y="0" width="100" height="6" rx="2" />
					<rect
						class="fill fill-units"
						data-kind="units"
						x="0"
						y="0"
						width={unitWidth}
						height="6"
						rx="2"
					/>
				</svg>
			</div>
			<span class="num"
				>{row.nodes} of {totalNodes} nodes ({nodePct} %) · {row.units} of {totalUnits} units ({unitPct}
				%)</span
			>
		</li>
	{/each}
</ul>
<p class="unest">{unestimated} unestimated</p>

<style>
	.totals {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: var(--space-2);
	}

	li {
		display: grid;
		grid-template-columns: 5.5rem 1fr;
		gap: 0 var(--space-3);
		align-items: center;
	}

	.bars {
		display: grid;
		gap: 2px;
		width: 100%;
	}

	.num {
		grid-column: 2;
		font-size: 0.8125rem;
		color: var(--color-muted-foreground);
	}

	@media (min-width: 40rem) {
		li {
			grid-template-columns: 6rem 1fr auto;
		}

		.num {
			grid-column: auto;
		}
	}

	svg {
		width: 100%;
		height: 0.375rem;
	}

	.track {
		fill: var(--color-muted);
	}

	.fill {
		fill: var(--color-bar);
	}

	.fill-units {
		opacity: 0.65;
	}

	.unest {
		font-size: 0.875rem;
		color: var(--color-muted-foreground);
	}
</style>
