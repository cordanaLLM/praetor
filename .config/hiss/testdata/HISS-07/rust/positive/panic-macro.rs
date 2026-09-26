// panic! in library code aborts instead of returning an error (abort policy, Q-014).
pub fn require_port(port: u16) -> u16 {
    if port == 0 {
        panic!("port must be non-zero");
    }
    port
}
