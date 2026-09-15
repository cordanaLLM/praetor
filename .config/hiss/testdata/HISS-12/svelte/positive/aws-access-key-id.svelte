<script>
  // HISS-12: a long-lived AWS access key id committed inside a component.
  // Reported by gitleaks rule aws-access-token.
  const AWS_ACCESS_KEY_ID = "AKIAZ3MTQ7XK2LPWV4RD";
</script>

<p>{AWS_ACCESS_KEY_ID.slice(0, 4)}</p>
