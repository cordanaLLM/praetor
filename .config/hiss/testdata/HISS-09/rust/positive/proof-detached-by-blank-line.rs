fn first(p: *const u8) -> u8 {
    // SAFETY: p is non-null and aligned.

    unsafe { *p }
}
