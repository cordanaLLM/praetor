// Every rustdoc spelling clippy reads as the safety section: any heading level, the SAFETY
// and Implementation safety headings, a /** */ doc comment, a #[doc] attribute, and other
// attributes between the docs and the header.

/// ## Safety
///
/// `p` must be valid for reads.
#[inline]
#[must_use]
unsafe fn heading_level(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}

/// # SAFETY
unsafe fn upper_case() {}

/// # Implementation safety
unsafe fn implementation() {}

/**
 * # Safety
 *
 * `p` must be valid for reads.
 */
unsafe fn block_doc(p: *const u8) -> u8 {
    // SAFETY: the caller upholds this function's contract.
    unsafe { *p }
}

#[doc = "# Safety"]
unsafe fn doc_attribute() {}
