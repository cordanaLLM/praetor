<!--
@component
The state of one data file for a tab: an inline error (with the time of the last good data,
which stays on screen below it) or, when all is well, when the producer generated it.
-->
<script lang="ts">
	import type { FileSlot } from '#lib/data/store.svelte.ts';
	import type { DataFile } from '#lib/data/types.ts';
	import { formatUtc } from '#lib/data/time.ts';

	let { slot }: { slot: FileSlot<DataFile> } = $props();
</script>

{#if slot.error}
	<div class="error" role="alert" data-testid="file-error">
		<p class="title">Could not update {slot.name}.json</p>
		<p class="message">{slot.error.message}</p>
		<p class="last">
			{#if slot.data}
				Showing the last good data: received {formatUtc(slot.lastGoodAt)}, generated
				{formatUtc(slot.data.generated_at)}.
			{:else}
				No good copy of this file has loaded yet.
			{/if}
		</p>
	</div>
{:else if slot.data}
	<p class="meta">
		Generated {formatUtc(slot.data.generated_at)}{#if slot.loading}<span>
				· refreshing</span
			>{/if}
	</p>
{/if}

<style>
	.error {
		border: 1px solid var(--color-destructive);
		border-inline-start-width: 4px;
		border-radius: var(--radius-card);
		padding: var(--space-3) var(--space-4);
		margin-block: var(--space-3);
		background: var(--color-card);
	}

	.title {
		margin: 0;
		font-weight: 600;
		color: var(--color-destructive);
	}

	.message {
		margin: var(--space-1) 0;
		overflow-wrap: anywhere;
	}

	.last,
	.meta {
		margin: 0;
		font-size: 0.875rem;
		color: var(--color-muted-foreground);
	}

	.meta {
		margin-block: var(--space-2);
	}
</style>
