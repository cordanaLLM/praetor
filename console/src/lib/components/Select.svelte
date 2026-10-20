<!--
@component
A labelled native select: keyboard and screen-reader behaviour come from the platform.
-->
<script lang="ts">
	interface Props {
		id: string;
		label: string;
		value: string;
		options: readonly { value: string; label: string }[];
		onchange: (value: string) => void;
	}

	let { id, label, value, options, onchange }: Props = $props();
</script>

<span class="field">
	<label for={id}>{label}</label>
	<select {id} {value} onchange={(e) => onchange(e.currentTarget.value)}>
		{#each options as option (option.value)}
			<option value={option.value}>{option.label}</option>
		{/each}
	</select>
</span>

<style>
	.field {
		display: inline-flex;
		flex-direction: column;
		gap: 0.125rem;
		font-size: 0.875rem;
		min-width: 9rem;
		flex: 1 1 9rem;
	}

	label {
		color: var(--color-muted-foreground);
	}

	select {
		min-height: 44px;
		padding: 0 var(--space-2);
		border: 1px solid var(--color-input);
		border-radius: 0.5rem;
		background: var(--color-card);
		color: var(--color-foreground);
		font: inherit;
	}
</style>
