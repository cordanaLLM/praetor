<script>
  // A component with a public prop contract and no test of any dimension: no positive
  // render assertion, no negative case for a missing prop, no boundary case for an empty
  // or maximal label. No Svelte toolchain runs in any gate, so nothing observes it.
  let { label = "" } = $props();
</script>

<button>{label}</button>
