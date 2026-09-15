fn first(p: *const u8) -> u8 {
    unsafe { *p } // SAFETY: p is non-null, aligned and initialised.
}
