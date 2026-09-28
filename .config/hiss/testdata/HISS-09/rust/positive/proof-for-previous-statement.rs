fn both(p: *const u8, q: *const u8) -> u8 {
    // SAFETY: p is non-null, aligned and initialised; this proves the first statement only.
    let a = unsafe { *p };
    let b =
        unsafe { *q };
    a + b
}
