<script>
  // {#each} iterates a finite list and is markup; the loop below is counted.
  export let items = [];
  let total = 0;
  for (let i = 0; i < items.length; i += 1) total += items[i];
</script>

{#each items as item}<li>{item}</li>{/each}
<p>while (true)</p>
