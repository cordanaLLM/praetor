<!--
@component
Copies `text` exactly as given and says so in a polite live region. The visible label names
what is copied, so several copy buttons on one page stay distinguishable.
-->
<script lang="ts">
	import Button from '@sveltesentio/ui/button/component';
	import { copyText, type CopyOutcome } from '#lib/copy.ts';

	interface Props {
		text: string;
		/** What is copied, for the accessible name: "Copy command for C1". */
		label: string;
		copy?: (text: string) => Promise<CopyOutcome>;
	}

	let { text, label, copy = copyText }: Props = $props();
	let outcome = $state<CopyOutcome | undefined>(undefined);
	let timer: ReturnType<typeof setTimeout> | undefined;

	async function onclick(): Promise<void> {
		outcome = await copy(text);
		clearTimeout(timer);
		timer = setTimeout(() => (outcome = undefined), 4000);
	}
</script>

<span class="copy">
	<Button variant="outline" size="sm" aria-label={label} {onclick}>
		<span aria-hidden="true">⧉</span> Copy
	</Button>
	<span class="status" role="status" aria-live="polite">
		{#if outcome === 'copied'}Copied{:else if outcome === 'failed'}Copy failed: select the
			command and copy it by hand{/if}
	</span>
</span>

<style>
	.copy {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		flex-wrap: wrap;
	}

	.status {
		font-size: 0.875rem;
		color: var(--color-muted-foreground);
	}
</style>
