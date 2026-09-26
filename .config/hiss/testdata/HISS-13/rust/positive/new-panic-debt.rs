// Added debt the scanner counts: a HISS-07 abort-policy panic! in library code raises V_total.
pub fn require_port(port: u16) -> u16 {
    if port == 0 {
        panic!("port must be non-zero");
    }
    port
}
