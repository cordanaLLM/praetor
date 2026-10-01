<script>
  // A clean addition: no scanner rule fires, so V_total is unchanged.
  export let a = 0;
  export let b = 0;
  $: total = a + b;
</script>

<p>{total}</p>
