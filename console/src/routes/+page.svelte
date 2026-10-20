<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { focusableElements, nextTrapTarget } from '@sveltesentio/ui/dialog';
	import { replaceState } from '$app/navigation';
	import CommandPalette from '@sveltesentio/ui/cmd/palette';
	import Input from '@sveltesentio/ui/input/component';
	import ThemeToggle from '@sveltesentio/ui/theme-toggle/component';
	import type { ThemeMode } from '@sveltesentio/ui/theme-toggle';
	import { ConsoleApp } from '#lib/app.svelte.ts';
	import { buildRegistry, TAB_LABELS } from '#lib/commands.ts';
	import { formatUtc } from '#lib/data/time.ts';
	import { applyMode, storedMode } from '#lib/theme.ts';
	import { TABS, type Tab } from '#lib/view/state.svelte.ts';
	import TabBar from '#lib/components/TabBar.svelte';
	import NowTab from '#lib/components/NowTab.svelte';
	import DecisionsTab from '#lib/components/DecisionsTab.svelte';
	import FleetTab from '#lib/components/FleetTab.svelte';
	import GraphTab from '#lib/components/GraphTab.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';

	const app = new ConsoleApp({
		writeUrl: (query) => replaceState(query ? `?${query}` : location.pathname, {}),
		eventsUrl: import.meta.env.VITE_CONSOLE_EVENTS_URL,
		refreshSeconds: import.meta.env.VITE_CONSOLE_REFRESH_SECONDS
	});
	const { data, view } = app;

	let paletteOpen = $state(false);
	let mode = $state<ThemeMode>('system');
	let returnFocus: HTMLElement | null = null;

	const registry = $derived(
		buildRegistry(
			{ cards: data.cards.data, graph: data.graph.data, status: data.status.data },
			{
				tab: (tab) => void app.selectTab(tab),
				reveal: (tab, id, cardId) => void app.reveal(tab, id, cardId),
				actions: [
					{
						id: 'act:search',
						title: 'Search everything',
						group: 'Actions',
						keywords: ['find', 'filter'],
						run: () => document.getElementById('q')?.focus()
					},
					{
						id: 'act:clear',
						title: 'Clear search and filters',
						group: 'Actions',
						run: () => view.clearFilters()
					},
					{
						id: 'act:refresh',
						title: 'Refresh data now',
						group: 'Actions',
						run: () => void data.refresh()
					},
					{
						id: 'act:theme',
						title: 'Switch theme (light, dark, system)',
						group: 'Actions',
						run: () => cycleTheme()
					}
				]
			}
		)
	);

	const liveText = $derived(
		!app.live
			? 'Starting'
			: app.live.mode === 'timer'
				? 'Refreshing on a timer'
				: app.live.state === 'open'
					? 'Live'
					: 'Reconnecting'
	);
	const newest = $derived(
		Math.max(0, ...[data.cards, data.graph, data.status].map((s) => s.lastGoodAt ?? 0)) ||
			undefined
	);

	function setMode(next: ThemeMode): void {
		mode = next;
		applyMode(next, app.storage, document.documentElement);
	}

	function cycleTheme(): void {
		const order: ThemeMode[] = ['light', 'dark', 'system'];
		setMode(order[(order.indexOf(mode) + 1) % order.length] ?? 'system');
	}

	function trapPalette(event: KeyboardEvent): void {
		const panel = document.querySelector('.ssentio-cmd__panel');
		if (!panel) return;
		event.preventDefault();
		nextTrapTarget(focusableElements(panel), document.activeElement, event.shiftKey)?.focus();
	}

	function onKeydown(event: KeyboardEvent): void {
		if (paletteOpen && event.key === 'Tab') return trapPalette(event);
		const n = Number(event.key);
		if (!event.altKey || event.ctrlKey || event.metaKey || !(n >= 1 && n <= TABS.length))
			return;
		event.preventDefault();
		void app.selectTab(TABS[n - 1] as Tab);
	}

	$effect(() => {
		if (paletteOpen) {
			returnFocus = document.activeElement as HTMLElement | null;
			// The library's autofocus attribute does not focus dynamically inserted inputs.
			void tick().then(() => document.getElementById('ssentio-cmd-input')?.focus());
		} else if (returnFocus) {
			const target = returnFocus;
			returnFocus = null;
			// A command may have moved focus on purpose; only restore when it fell to the body.
			queueMicrotask(() => {
				if (document.activeElement === document.body) target.focus();
			});
		}
	});

	onMount(() => {
		mode = storedMode(app.storage);
		const media = window.matchMedia('(prefers-color-scheme: dark)');
		const follow = () => mode === 'system' && setMode('system');
		media.addEventListener('change', follow);
		const stop = app.start(new URL(location.href).searchParams);
		return () => {
			stop();
			media.removeEventListener('change', follow);
		};
	});
</script>

<svelte:window onkeydown={onKeydown} onscroll={() => app.onScroll()} />

<a class="skip" href="#main">Skip to content</a>

<header class="top">
	<div class="bar">
		<h1>Praetor console</h1>
		<p class="live" role="status">
			<span class={`dot ${app.live?.state ?? 'idle'}`} aria-hidden="true"></span>
			{liveText}{#if newest}<span class="when"> · updated {formatUtc(newest)}</span>{/if}
		</p>
		<div class="tools">
			<button
				type="button"
				class="palette-btn"
				onclick={() => (paletteOpen = true)}
				aria-keyshortcuts="Control+K Meta+K"
			>
				Jump to… <kbd>Ctrl K</kbd>
			</button>
			<ThemeToggle {mode} onchange={setMode} showLabel class="theme" />
		</div>
	</div>
	<div class="search" role="search">
		<label for="q" class="sr-only">Search cards, decisions, sessions and graph items</label>
		<Input
			id="q"
			type="search"
			placeholder="Search cards, decisions, sessions, graph"
			value={view.filters.q}
			oninput={(e) => view.setFilter('q', e.currentTarget.value)}
		/>
	</div>
	<nav aria-label="Sections">
		<TabBar active={view.tab} counts={app.counts} onselect={(tab) => void app.selectTab(tab)} />
	</nav>
</header>

<main id="main">
	<div
		class="panel"
		id={`panel-${view.tab}`}
		role="tabpanel"
		aria-labelledby={`tab-${view.tab}`}
		tabindex="-1"
	>
		<h2 class="sr-only">{TAB_LABELS[view.tab]}</h2>
		{#if view.tab === 'now'}
			<NowTab slot={data.cards} {view} />
		{:else if view.tab === 'decisions'}
			<DecisionsTab slot={data.status} {view} />
		{:else if view.tab === 'fleet'}
			<FleetTab slot={data.status} {view} />
		{:else if view.tab === 'graph'}
			<GraphTab slot={data.graph} {view} />
		{:else if view.tab === 'quota'}
			<EmptyState
				title="No quota data yet"
				body="Data version 1 carries no credit or quota file. This tab fills in when a later data version adds one; until then there is nothing to show."
			/>
		{:else}
			<EmptyState
				title="No history yet"
				body="Data version 1 carries no history. This tab fills in when a later data version adds it."
			/>
		{/if}
	</div>
</main>

<label for="ssentio-cmd-input" class="sr-only">Search commands</label>
<CommandPalette
	{registry}
	bind:open={paletteOpen}
	placeholder="Jump to a tab, card, decision or graph item"
/>

<style>
	.skip {
		position: absolute;
		left: var(--space-2);
		top: -4rem;
		padding: var(--space-2) var(--space-3);
		background: var(--color-primary);
		color: var(--color-primary-foreground);
		border-radius: 0.5rem;
		z-index: 60;
	}

	.skip:focus {
		top: var(--space-2);
	}

	.top {
		position: sticky;
		top: 0;
		z-index: 10;
		background: var(--color-background);
		border-bottom: 1px solid var(--color-border);
		padding: var(--space-3) var(--space-4) var(--space-1);
	}

	.bar,
	.search,
	nav,
	main {
		max-width: 72rem;
		margin-inline: auto;
	}

	.bar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-4);
	}

	h1 {
		font-size: 1.125rem;
		margin: 0;
		letter-spacing: -0.01em;
	}

	.live {
		margin: 0;
		font-size: 0.8125rem;
		color: var(--color-muted-foreground);
		display: flex;
		align-items: center;
		gap: var(--space-2);
		flex: 1 1 auto;
	}

	.dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
		background: var(--color-idle);
	}

	.dot.open,
	.dot.polling {
		background: var(--color-ok);
	}

	.tools {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}

	.tools :global(.theme) {
		min-width: 44px;
		min-height: 44px;
	}

	.palette-btn {
		min-height: 44px;
		padding: 0 var(--space-3);
		border: 1px solid var(--color-border);
		border-radius: 0.5rem;
		background: var(--color-card);
		color: var(--color-foreground);
		font: inherit;
		font-size: 0.875rem;
		cursor: pointer;
	}

	kbd {
		font-size: 0.75rem;
		color: var(--color-muted-foreground);
		margin-inline-start: var(--space-1);
	}

	.search {
		margin-block: var(--space-2);
	}

	.search :global(input) {
		height: 44px;
		font-size: 1rem;
	}

	main {
		padding: var(--space-3) var(--space-4) var(--space-6);
	}

	.panel:focus {
		outline: none;
	}
</style>
