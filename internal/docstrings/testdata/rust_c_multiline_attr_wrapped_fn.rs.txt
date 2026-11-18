/// Combines `a` and `b` with a multi-line attribute.
#[cfg_attr(
    feature = "trace",
    inline(never),
)]
pub fn wrapped(
    a: u32,
    b: u32,
) -> u32 {
    a + b
}
