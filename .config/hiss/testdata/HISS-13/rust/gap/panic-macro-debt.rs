// Real HISS-07 debt: panic! aborts instead of propagating, exactly what the Go scanner
// reports as "Legacy panic invocation in production code path" and what clippy::panic warns
// about. No Rust pattern matches panic!, so nothing enters the baseline and V_total does
// not move.
pub fn require_port(port: u16) -> u16 {
    if port == 0 {
        panic!("port must be non-zero");
    }
    port
}
