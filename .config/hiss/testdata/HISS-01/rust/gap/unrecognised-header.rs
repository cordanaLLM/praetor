// The scanner recognises fn, pub fn, pub(crate) fn and their async forms only. A function
// behind any other header is never opened, so its self-call goes unseen.
pub(super) fn walk(n: u32) -> u32 {
    if n == 0 {
        return 0;
    }
    walk(n - 1)
}

const fn fold(n: u32) -> u32 {
    if n == 0 {
        return 0;
    }
    fold(n - 1)
}
