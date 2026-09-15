package p

// HISS-12: a production database password committed in source. It is a real
// credential, but a human-chosen phrase rather than a high-entropy token, so it falls
// below the entropy floor every default rule applies.
const DatabasePassword = "correct-horse-battery-staple"
