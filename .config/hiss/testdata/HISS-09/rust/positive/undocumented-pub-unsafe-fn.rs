/// Reads one byte, with no safety section: nothing tells a caller what makes p valid.
pub unsafe fn first(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}
