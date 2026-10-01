<script>
  // eval of user input inside a component's script block.
  export let expression = "";
  $: result = eval(expression);
</script>

<p>{result}</p>
