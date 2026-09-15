struct Handle(*mut u8);

// unsafe impl asserts a thread-safety invariant the compiler cannot check, and is
// exactly the shape clippy::undocumented_unsafe_blocks requires a SAFETY proof for.
// The matcher is `unsafe` followed by `{`, so `unsafe impl` never reaches it.
unsafe impl Send for Handle {}
