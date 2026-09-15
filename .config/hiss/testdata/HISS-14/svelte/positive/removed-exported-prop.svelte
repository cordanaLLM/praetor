<script>
  // HISS-14: the component previously published `export let label`. It has been removed
  // and replaced by `title`, so every parent that passed label silently renders nothing.
  // The component's public contract is not append-only. Only the commit message decides
  // the outcome.
  export let title = "";
</script>

<h1>{title}</h1>
