<script>
  // Real HISS-02 debt inside a component: an unbounded loop. The script scanner reads the
  // component's script block and reports it, so V_total moves.
  export function spin(done) {
    while (true) {
      if (done()) return;
    }
  }
</script>
