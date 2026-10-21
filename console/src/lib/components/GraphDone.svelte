<!--
@component
Recent completions in producer order, with local times, and daily counts by UTC date.
-->
<script lang="ts">
	import { keyed } from '#lib/data/select.ts';
	import type { GraphFile } from '#lib/data/types.ts';

	let { recent, trend }: { recent: GraphFile['done_recent']; trend: GraphFile['done_trend'] } =
		$props();
</script>

<section aria-labelledby="done-recent-h">
	<h2 id="done-recent-h">Done in the last 24 hours</h2>
	<ol class="rows" aria-label="Recently done nodes">
		{#each keyed(recent, (node) => node.id) as { key, item: node } (key)}
			<li class="row">
				<span class="title">{node.title}</span>
				<span class="meta">
					<code>{node.id}</code>
					{#if node.repo}<span>{node.repo}</span>{/if}
					<time datetime={node.done_at}>{new Date(node.done_at).toLocaleString()}</time>
				</span>
			</li>
		{:else}
			<li class="none">No nodes went done in the last 24 hours.</li>
		{/each}
	</ol>
</section>

<section aria-labelledby="done-trend-h">
	<h2 id="done-trend-h">Done over 14 days (UTC)</h2>
	<ol class="trend" aria-label="Done per UTC day">
		{#each trend as day (day.day)}
			<li><time datetime={day.day}>{day.day}</time><span>{day.done} done</span></li>
		{/each}
	</ol>
</section>

<style>
	h2 {
		font-size: 1rem;
		margin: var(--space-4) 0 var(--space-2);
	}

	.rows,
	.trend {
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
		overflow-wrap: anywhere;
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

	.trend {
		grid-template-columns: repeat(auto-fit, minmax(7rem, 1fr));
		font-size: 0.8125rem;
	}

	.trend li {
		display: grid;
		gap: var(--space-1);
		padding: var(--space-2);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-card);
	}
</style>
