<!--
@component
WAI-ARIA tabs: one tab stop for the list, arrow keys (and Home and End) move between tabs and
activate them.
-->
<script lang="ts">
	import { TAB_LABELS } from '#lib/commands.ts';
	import { TABS, type Tab } from '#lib/view/state.svelte.ts';

	interface Props {
		active: Tab;
		counts: Partial<Record<Tab, number>>;
		onselect: (tab: Tab) => void;
	}

	let { active, counts, onselect }: Props = $props();
	const buttons: Partial<Record<Tab, HTMLButtonElement>> = {};

	function move(event: KeyboardEvent): void {
		const keys: Record<string, number> = { ArrowRight: 1, ArrowLeft: -1, Home: -99, End: 99 };
		const step = keys[event.key];
		if (step === undefined) return;
		event.preventDefault();
		const i = TABS.indexOf(active);
		const next =
			Math.abs(step) === 99
				? step < 0
					? 0
					: TABS.length - 1
				: (i + step + TABS.length) % TABS.length;
		const tab = TABS[next] ?? 'now';
		onselect(tab);
		buttons[tab]?.focus();
	}
</script>

<div class="tabs" role="tablist" aria-label="Console sections" tabindex="-1" onkeydown={move}>
	{#each TABS as tab (tab)}
		<button
			bind:this={buttons[tab]}
			type="button"
			role="tab"
			id={`tab-${tab}`}
			aria-selected={tab === active}
			aria-controls={`panel-${tab}`}
			tabindex={tab === active ? 0 : -1}
			onclick={() => onselect(tab)}
		>
			{TAB_LABELS[tab]}
			{#if counts[tab] !== undefined}<span class="count">{counts[tab]}</span>{/if}
		</button>
	{/each}
</div>

<style>
	.tabs {
		display: flex;
		gap: var(--space-1);
		overflow-x: auto;
		scrollbar-width: thin;
		padding: var(--space-1);
		margin: 0 calc(-1 * var(--space-1));
	}

	button {
		flex: 0 0 auto;
		min-height: 44px;
		padding: 0 var(--space-3);
		border: 1px solid transparent;
		border-radius: 999px;
		background: none;
		color: var(--color-muted-foreground);
		font: inherit;
		font-weight: 500;
		cursor: pointer;
	}

	button:hover {
		color: var(--color-foreground);
		background: var(--color-muted);
	}

	button[aria-selected='true'] {
		color: var(--color-primary-foreground);
		background: var(--color-primary);
	}

	.count {
		display: inline-block;
		min-width: 1.5rem;
		margin-inline-start: var(--space-1);
		font-size: 0.75rem;
		font-variant-numeric: tabular-nums;
	}
</style>
