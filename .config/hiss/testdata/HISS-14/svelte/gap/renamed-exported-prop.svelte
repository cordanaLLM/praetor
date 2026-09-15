<script>
  // HISS-14: the published prop `disabled` was renamed to `readonly`. Every parent that
  // passed disabled keeps compiling and silently loses the behaviour. Committed as
  // "fix(api): clarify the published prop name" nothing in this repository notices: no
  // analyzer compares a component's exported props against the previous release.
  export let readonly = false;
</script>

<button {readonly}>ok</button>
