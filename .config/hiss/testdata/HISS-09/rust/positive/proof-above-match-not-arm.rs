fn pick(n: i32, p: *const u8, q: *const u8) -> u8 {
    match n {
        // SAFETY: p is non-null, aligned and initialised; this proves the first arm only.
        0 => unsafe { *p },
        -1 => unsafe { *q },
        _ => 0,
    }
}
