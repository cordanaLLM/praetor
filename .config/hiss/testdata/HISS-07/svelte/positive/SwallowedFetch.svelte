<script>
  // The rejection of the refresh is swallowed by an empty handler.
  export let refresh;
  function reload() {
    refresh().catch(() => {});
  }
</script>

<button on:click={reload}>Reload</button>
