// A guard sees only its own arm's pattern. The local a later arm binds is not in scope here,
// so the call in the guard reaches the function.
pub fn ready(slot: Option<u8>) -> bool {
    match slot {
        Some(n) if ready(None) => n > 0,
        Some(ready) => ready > 0,
        None => false,
    }
}
