// An unsafe fn documents its callers' contract in a rustdoc # Safety section, which is what
// clippy's missing_safety_doc asks for. The section is accepted for every visibility and
// qualifier alike; before, a private unsafe fn with the section was reported while a pub one
// was never checked at all.

/// Reads one byte.
///
/// # Safety
///
/// `p` must be valid for reads.
unsafe fn read_private(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}

/// # Safety
///
/// `p` must be valid for reads.
pub unsafe fn read_pub(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}

/// # Safety
///
/// `p` must be valid for reads.
pub(crate) const unsafe fn read_const(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}

/// # Safety
///
/// `p` must be valid for reads.
pub unsafe extern "C" fn read_extern(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}
