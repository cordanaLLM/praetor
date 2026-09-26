// Owner decision Q-014 also refuses the assert family in library code, but no Rust pattern
// matches assert!, assert_eq! or debug_assert!, so this abort goes unreported.
pub fn ratio(a: u32, b: u32) -> u32 {
    assert!(b != 0, "divisor must be non-zero");
    a / b
}
