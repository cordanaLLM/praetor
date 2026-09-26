// The brace tracker follows one function at a time, so a fn nested inside another is part
// of its body, and the nested fn's own self-call is not attributed to it.
pub fn outer(n: u32) -> u32 {
    fn inner(n: u32) -> u32 {
        if n == 0 {
            return 0;
        }
        inner(n - 1)
    }
    inner(n)
}
