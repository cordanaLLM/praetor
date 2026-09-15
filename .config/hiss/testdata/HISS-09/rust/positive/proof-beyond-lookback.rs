fn first(p: *const u8) -> u8 {
    // SAFETY: p is non-null and aligned.
    // filler 2
    // filler 3
    // filler 4
    // filler 5
    // filler 6
    // filler 7
    // filler 8
    // filler 9
    unsafe { *p }
}
