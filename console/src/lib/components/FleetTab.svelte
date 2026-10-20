<!--
@component
Agent sessions (repository, state, idle minutes, memory, latest line) and the holds in force.
-->
<script lang="ts">
	import { filterSessions, keyed } from '#lib/data/select.ts';
	import type { FileSlot } from '#lib/data/store.svelte.ts';
	import { formatUtc } from '#lib/data/time.ts';
	import type { ViewState } from '#lib/view/state.svelte.ts';
	import FileStatus from './FileStatus.svelte';
	import Skeleton from './Skeleton.svelte';

	let { slot, view }: { slot: FileSlot<'status'>; view: ViewState } = $props();
	const sessions = $derived(filterSessions(slot.data?.sessions ?? [], view.filters.q));
	const stateClass: Record<string, string> = {
		active: 'ok',
		quiet: 'warn',
		'not running': 'idle'
	};
</script>

<FileStatus {slot} />

{#if slot.pending}
	<Skeleton label="Loading sessions" />
{:else if slot.data}
	<section aria-labelledby="holds-h">
		<h2 id="holds-h">Holds in force ({slot.data.holds.length})</h2>
		{#if slot.data.holds.length === 0}
			<p class="none">No holds.</p>
		{:else}
			<ul class="holds">
				{#each keyed(slot.data.holds, (x) => x.id) as { key, item: h } (key)}
					<li>
						<code>{h.id}</code>{#if h.since}<span class="label">
								since {formatUtc(h.since)}</span
							>{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</section>
	<section aria-labelledby="sessions-h">
		<h2 id="sessions-h">Sessions ({sessions.length} of {slot.data.sessions.length})</h2>
		<ul class="sessions">
			{#each keyed(sessions, (x) => `${x.repo}:${x.pid ?? ''}`) as { key, item: s } (key)}
				<li class="session">
					<span class="repo">{s.repo}</span>
					<span class={`state ${stateClass[s.state] ?? 'idle'}`}>
						<span aria-hidden="true">●</span>
						{s.state}
					</span>
					<span class="num"><span class="label">Idle</span> {s.idle_min ?? '–'} min</span>
					<span class="num"
						><span class="label">Memory</span>
						{s.gb === undefined ? '–' : `${s.gb.toFixed(1)} GB`}</span
					>
					<span class="latest">{s.latest ?? 'No output yet'}</span>
				</li>
			{:else}
				<li class="none">No session matches.</li>
			{/each}
		</ul>
	</section>
{/if}

<style>
	h2 {
		font-size: 1rem;
		margin: var(--space-4) 0 var(--space-2);
	}

	.none,
	.label {
		color: var(--color-muted-foreground);
		font-size: 0.875rem;
	}

	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: var(--space-2);
	}

	.holds {
		display: flex;
		flex-wrap: wrap;
	}

	.holds li {
		padding: var(--space-1) var(--space-3);
		border: 1px solid var(--color-border);
		border-radius: 999px;
		background: var(--color-card);
	}

	.session {
		display: grid;
		grid-template-columns: 1fr auto;
		gap: var(--space-1) var(--space-3);
		padding: var(--space-3) var(--space-4);
		background: var(--color-card);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-card);
	}

	.latest {
		grid-column: 1 / -1;
		font-family: var(--font-mono);
		font-size: 0.8125rem;
		overflow-wrap: anywhere;
	}

	@media (min-width: 48rem) {
		.session {
			grid-template-columns: minmax(10rem, 1fr) 8rem 7rem 9rem;
		}
	}

	.repo {
		font-weight: 600;
		overflow-wrap: anywhere;
	}

	.ok {
		color: var(--color-ok);
	}

	.warn {
		color: var(--color-warn);
	}

	.idle {
		color: var(--color-idle);
	}
</style>
