<script>
  // save returns a promise nobody awaits or handles.
  export let save;
  export let value = "";
</script>

<button on:click={() => save(value)}>Save</button>
