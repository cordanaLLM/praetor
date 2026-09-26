// is_even and is_odd form a two-function cycle. Deciding it needs a call graph across
// functions, which the Rust line scanner does not build.
pub fn is_even(n: u32) -> bool {
    if n == 0 {
        return true;
    }
    is_odd(n - 1)
}

pub fn is_odd(n: u32) -> bool {
    if n == 0 {
        return false;
    }
    is_even(n - 1)
}
