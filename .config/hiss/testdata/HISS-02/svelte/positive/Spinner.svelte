<script>
  // An unbounded loop inside a component's script block.
  export function spin(done) {
    while (true) {
      if (done()) return;
    }
  }
</script>

<span class="spinner"></span>
