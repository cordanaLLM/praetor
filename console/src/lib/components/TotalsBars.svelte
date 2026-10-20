<!--
@component
Status totals as small horizontal bars (inline SVG, no chart library). The numbers are text, so
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
	const max = $derived(Math.max(1, ...rows.map((r) => r.nodes)));
</script>

<ul class="totals" aria-label="Plan graph totals">
	{#each rows as row (row.key)}
		<li>
			<span class="name">{row.key}</span>
			<svg
				viewBox="0 0 100 8"
				preserveAspectRatio="none"
				aria-hidden="true"
				focusable="false"
			>
				<rect class="track" x="0" y="0" width="100" height="8" rx="2" />
				<rect class="fill" x="0" y="0" width={(row.nodes / max) * 100} height="8" rx="2" />
			</svg>
			<span class="num">{row.nodes} nodes · {row.units.toLocaleString('en')} units</span>
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

	.num {
		grid-column: 2;
		font-size: 0.8125rem;
		color: var(--color-muted-foreground);
	}

	@media (min-width: 40rem) {
		li {
			grid-template-columns: 6rem 1fr 13rem;
		}

		.num {
			grid-column: auto;
		}
	}

	svg {
		width: 100%;
		height: 0.625rem;
	}

	.track {
		fill: var(--color-muted);
	}

	.fill {
		fill: var(--color-bar);
	}

	.unest {
		font-size: 0.875rem;
		color: var(--color-muted-foreground);
	}
</style>
