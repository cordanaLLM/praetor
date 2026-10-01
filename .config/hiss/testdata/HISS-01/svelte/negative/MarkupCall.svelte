<script>
  // walk is called from the markup, never from its own body.
  export let items = [];

  function walk(list) {
    return list.length;
  }
</script>

<p>{walk(items)}</p>
