<script>
  // The rejection is shown to the user instead of being swallowed.
  export let refresh;
  let error = "";
  function reload() {
    refresh().catch((reason) => {
      error = String(reason);
    });
  }
</script>

<button on:click={reload}>Reload</button>
<p>{error}</p>
