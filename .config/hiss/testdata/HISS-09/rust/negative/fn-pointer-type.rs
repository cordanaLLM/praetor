// An unsafe fn pointer type names no function, so it declares no contract to document.
pub struct Hooks(
    unsafe fn(u8),
    unsafe fn(*const u8) -> u8,
);
