<!--
@component
One operator card: title and the one-line `say`, expanding to the detail. Expansion is held by
the view state (by card id), so a live data update keeps it open.
-->
<script lang="ts">
	import type { Card } from '#lib/data/types.ts';
	import CopyButton from './CopyButton.svelte';

	interface Props {
		card: Card;
		/** Unique key for element ids (card ids are not promised unique). */
		uid?: string;
		expanded: boolean;
		ontoggle: (id: string) => void;
	}

	let { card, uid, expanded, ontoggle }: Props = $props();
	const base = $derived(`card-${uid ?? card.id}`);
	const regionId = $derived(`${base}-detail`);
	const hasDetail = $derived(
		Boolean(card.detail || card.why_now || card.consequence_if_skipped || card.est_minutes)
	);
</script>

<article class="card" id={base} data-card-id={card.id} aria-labelledby={`${base}-title`}>
	<header>
		<button
			type="button"
			class="toggle"
			aria-expanded={expanded}
			aria-controls={regionId}
			onclick={() => ontoggle(card.id)}
		>
			<span class="chev" aria-hidden="true">{expanded ? '▾' : '▸'}</span>
			<span class="title" id={`${base}-title`}>{card.title}</span>
		</button>
		<span class="badges">
			<span class={`badge state-${card.state}`}>{card.state}</span>
			<span class="badge">{card.where}</span>
		</span>
	</header>
	<p class="say"><span class="label">Say when done:</span> {card.say}</p>
	<div class="detail" id={regionId} hidden={!expanded}>
		<p class="act"><span class="label">Do:</span> {card.act}</p>
		{#if card.detail}<p>{card.detail}</p>{/if}
		<dl>
			{#if card.why_now}<dt>Why now</dt>
				<dd>{card.why_now}</dd>{/if}
			{#if card.consequence_if_skipped}<dt>If skipped</dt>
				<dd>{card.consequence_if_skipped}</dd>{/if}
			{#if card.est_minutes}<dt>Estimate</dt>
				<dd>{card.est_minutes} min</dd>{/if}
		</dl>
		{#if !hasDetail}<p class="none">No further detail.</p>{/if}
	</div>
	{#if card.cmd || card.link}
		<div class="actions">
			{#if card.cmd}
				<code class="cmd">{card.cmd}</code>
				<CopyButton text={card.cmd} label={`Copy command for ${card.id}`} />
			{/if}
			{#if card.link}
				<a href={card.link} target="_blank" rel="noopener noreferrer">
					Open link<span class="sr-only"> for {card.id} (new tab)</span>
				</a>
			{/if}
		</div>
	{/if}
</article>

<style>
	.card {
		background: var(--color-card);
		color: var(--color-card-foreground);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-card);
		padding: var(--space-3) var(--space-4);
	}

	header {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.toggle {
		display: flex;
		gap: var(--space-2);
		align-items: baseline;
		min-height: 44px;
		padding: var(--space-1) 0;
		border: 0;
		background: none;
		color: inherit;
		font: inherit;
		text-align: start;
		cursor: pointer;
		flex: 1 1 16rem;
	}

	.title {
		font-weight: 600;
		font-size: 1rem;
	}

	.badges {
		display: flex;
		gap: var(--space-1);
		padding-block-start: var(--space-2);
	}

	.badge {
		font-size: 0.75rem;
		line-height: 1.25rem;
		padding: 0 var(--space-2);
		border-radius: 999px;
		border: 1px solid var(--color-border);
		color: var(--color-muted-foreground);
	}

	.state-now {
		color: var(--color-warn);
		border-color: currentColor;
	}

	.say,
	.act {
		margin: var(--space-1) 0;
	}

	.label {
		color: var(--color-muted-foreground);
	}

	.detail p {
		margin: var(--space-2) 0;
	}

	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--space-1) var(--space-3);
		margin: var(--space-2) 0;
	}

	dt {
		color: var(--color-muted-foreground);
	}

	dd {
		margin: 0;
	}

	.none {
		color: var(--color-muted-foreground);
	}

	.actions {
		display: flex;
		gap: var(--space-2) var(--space-3);
		align-items: center;
		flex-wrap: wrap;
		margin-block-start: var(--space-2);
	}

	.cmd {
		display: block;
		flex: 1 1 100%;
		padding: var(--space-2) var(--space-3);
		border-radius: 0.5rem;
		background: var(--color-muted);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		font-size: 0.875rem;
	}

	a {
		display: inline-flex;
		align-items: center;
		min-height: 44px;
	}
</style>
