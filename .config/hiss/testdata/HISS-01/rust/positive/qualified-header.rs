// The Rust header grammar opens a function behind any visibility and qualifier, so a
// pub(super) or const fn's self-call is reported like a plain fn's.
pub mod inner {
    pub(super) fn walk(n: u32) -> u32 {
        if n == 0 {
            return 0;
        }
        walk(n - 1)
    }
}

pub fn walk_three() -> u32 {
    inner::walk(3)
}

pub const fn fold(n: u32) -> u32 {
    if n == 0 {
        return 0;
    }
    fold(n - 1)
}
