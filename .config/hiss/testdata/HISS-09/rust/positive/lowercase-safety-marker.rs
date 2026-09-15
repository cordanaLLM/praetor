fn first(p: *const u8) -> u8 {
    // Safety: the lowercase marker is not the proof spelling the rule accepts.
    unsafe { *p }
}
