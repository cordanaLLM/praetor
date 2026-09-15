package p

// HISS-12: a production database password embedded in a connection string. The
// credential is live and in git history, but no default rule matches a password
// inside a URI userinfo field.
const DSN = "postgres://admin:hunter2@db.internal.example.com:5432/prod"
