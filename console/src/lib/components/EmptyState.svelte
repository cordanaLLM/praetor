<!--
@component
A tab whose data is not part of this data version yet.
-->
<script lang="ts">
	let { title, body }: { title: string; body: string } = $props();
</script>

<div class="empty">
	<h2>{title}</h2>
	<p>{body}</p>
</div>

<style>
	.empty {
		border: 1px dashed var(--color-border);
		border-radius: var(--radius-card);
		padding: var(--space-6);
		text-align: center;
		color: var(--color-muted-foreground);
	}

	h2 {
		margin: 0 0 var(--space-2);
		font-size: 1.125rem;
		color: var(--color-foreground);
	}

	p {
		margin: 0 auto;
		max-width: 40rem;
	}
</style>
