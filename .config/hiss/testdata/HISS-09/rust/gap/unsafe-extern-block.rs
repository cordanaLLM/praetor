// A Rust 2024 `unsafe extern` block asserts that the declared foreign signatures match
// the real ones. The matcher requires a brace directly after `unsafe`, so the
// intervening `extern "C"` hides the block.
unsafe extern "C" {
    fn strlen(s: *const u8) -> usize;
}
