<script>
  // Real HISS-02 debt inside a component: an unbounded loop. .svelte is outside every
  // scanner extension, so V_total does not move and the ratchet never sees it.
  export function spin(done) {
    while (true) {
      if (done()) return;
    }
  }
</script>
