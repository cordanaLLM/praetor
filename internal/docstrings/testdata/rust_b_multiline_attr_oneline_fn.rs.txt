/// Returns `a` with a multi-line attribute.
#[cfg_attr(
    feature = "trace",
    inline(never),
)]
pub fn one_line(a: u32) -> u32 {
    a
}
