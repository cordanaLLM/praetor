// Real HISS-07 debt: owner decision Q-014 refuses the assert family in library code, but no
// Rust pattern matches assert!, so nothing enters the baseline and V_total does not move.
pub fn ratio(a: u32, b: u32) -> u32 {
    assert!(b != 0, "divisor must be non-zero");
    a / b
}
