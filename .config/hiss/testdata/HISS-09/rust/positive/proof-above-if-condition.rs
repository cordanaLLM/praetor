fn check(p: *const u8) -> bool {
    // SAFETY: clippy does not carry a proof above an if into a block in its condition.
    if is_set(
        unsafe { *p },
    ) {
        return true;
    }
    false
}
