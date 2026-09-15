// The credential is read from the environment; nothing secret is in the file.
pub fn token() -> Result<String, std::env::VarError> {
    std::env::var("SERVICE_API_TOKEN")
}
