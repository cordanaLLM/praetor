// Opening a shared library by a runtime path and binding a symbol from it is the Rust
// form of the dynamic code loading HISS-08 prohibits. Nothing in internal/hiss,
// .golangci.yml or .config/semgrep/hiss-invariants.yml has a Rust HISS-08 rule.
fn resolve(path: &str) -> Result<libloading::Library, libloading::Error> {
    // SAFETY: present only so this fixture exercises HISS-08 alone; libloading's
    // constructor is unsafe because a library initialiser may run arbitrary code.
    unsafe { libloading::Library::new(path) }
}
