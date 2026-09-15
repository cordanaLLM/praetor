<script>
  // HISS-12: an RSA private key embedded in a component.
  // Reported by gitleaks rule private-key.
  const SIGNING_KEY = `-----BEGIN RSA PRIVATE KEY-----
MIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu
KUpRKfFLfRYC9AIKjbJTWit+CqvjWYzvQwECAwEAAQJAIJLixBy2qpFoS4DSmoEm
-----END RSA PRIVATE KEY-----`;
</script>

<p>{SIGNING_KEY.length}</p>
