<!--
@component
Placeholder rows shown while a file has not arrived yet. Announced once as "Loading".
-->
<script lang="ts">
	let { rows = 4, label = 'Loading' }: { rows?: number; label?: string } = $props();
</script>

<div class="skeleton" role="status" aria-busy="true" aria-label={label} data-testid="skeleton">
	{#each Array.from({ length: Math.min(rows, 12) }, (_, i) => i) as i (i)}
		<div class="row" style:width={`${92 - ((i * 13) % 30)}%`}></div>
	{/each}
</div>

<style>
	.skeleton {
		display: grid;
		gap: var(--space-3);
		padding-block: var(--space-2);
	}

	.row {
		height: 3.25rem;
		border-radius: var(--radius-card);
		background: linear-gradient(
			90deg,
			var(--color-muted) 0%,
			var(--color-secondary) 50%,
			var(--color-muted) 100%
		);
		background-size: 200% 100%;
		animation: shimmer 1.6s ease-in-out infinite;
	}

	@keyframes shimmer {
		from {
			background-position: 100% 0;
		}
		to {
			background-position: -100% 0;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.row {
			animation: none;
		}
	}
</style>
