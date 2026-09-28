// rustfmt moves a long `let v = unsafe { .. };` onto two lines and leaves the SAFETY comment
// above the let. clippy accepts a proof above the statement holding the block.

/// # Safety
///
/// `p` must be valid for reads.
unsafe fn read(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}

pub fn wrapped(p: &u8) -> u8 {
    // SAFETY: `p` is a live reference, so it is valid for reads.
    let value =
        unsafe { read(p) };
    value
}
