// A trait declaration is where an unsafe method's contract is written down, so its undocumented
// declaration is reported even though an impl of it would not be.
pub trait Reader {
    unsafe fn read(&self, p: *const u8) -> u8;
}
