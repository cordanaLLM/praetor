// HISS-12: a production database password embedded in a connection string. Live and
// in git history, but no default rule matches a URI userinfo password.
pub const DSN: &str = "postgres://admin:hunter2@db.internal.example.com:5432/prod";
