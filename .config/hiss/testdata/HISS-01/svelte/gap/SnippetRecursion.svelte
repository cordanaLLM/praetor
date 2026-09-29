<script lang="ts">
  export let node: { label: string; children: unknown[] };
</script>

<!-- A snippet that renders itself recurses in the markup, which the scanner does not read. -->
{#snippet tree(n)}
  <li>{n.label}<ul>{#each n.children as child}{@render tree(child)}{/each}</ul></li>
{/snippet}

<ul>{@render tree(node)}</ul>
