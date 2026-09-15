<script>
  // HISS-11: the component pulls an unpinned third-party bundle at run time with no
  // subresource integrity, so the browser executes whatever the CDN serves.
  export let title = "chart";
</script>

<svelte:head>
  <script src="https://cdn.jsdelivr.net/npm/chart.js@latest/dist/chart.umd.js"></script>
</svelte:head>

<h1>{title}</h1>
