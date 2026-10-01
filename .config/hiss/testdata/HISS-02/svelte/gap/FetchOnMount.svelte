<script>
  import { onMount } from "svelte";
  // A fetch without an AbortSignal deadline; the I/O half is not decided.
  let data;
  onMount(async () => {
    data = await (await fetch("/api/data")).json();
  });
</script>

<pre>{JSON.stringify(data)}</pre>
