<script>
  // HISS-11: the component resolves a dependency at run time from a floating specifier,
  // so the executed code is whatever the registry serves at that moment and no lockfile
  // entry or integrity hash covers it.
  import { onMount } from "svelte";

  let chart = null;

  onMount(async () => {
    chart = await import("https://esm.sh/chart.js@latest");
  });
</script>

<p>{chart ? "ready" : "loading"}</p>
