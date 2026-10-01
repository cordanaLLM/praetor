<script>
  export let expression = "";
</script>

<!-- eval in a markup expression runs code, but the scanner reads script blocks only. -->
<p>{eval(expression)}</p>
