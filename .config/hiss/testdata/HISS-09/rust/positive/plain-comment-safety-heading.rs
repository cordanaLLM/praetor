// # Safety
//
// A plain comment is not rustdoc, so this heading documents nothing a caller sees.
unsafe fn first(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}
