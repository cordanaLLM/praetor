<script>
  // HISS-12: a production database password committed in a component. A real
  // credential, but a human-chosen phrase rather than a high-entropy token.
  const DATABASE_PASSWORD = "correct-horse-battery-staple";
</script>

<p>{DATABASE_PASSWORD.length}</p>
