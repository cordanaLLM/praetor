// A match arm's pattern binds its names for the guard as well as the body, so a guard that
// calls the arm's local calls the fn pointer, not the function.
pub fn check(slot: Option<fn() -> bool>) -> u8 {
    match slot {
        Some(check) if check() => 1,
        _ => 0,
    }
}

pub fn inline(slot: Option<fn() -> bool>) -> u8 { match slot { Some(inline) if inline() => 1, _ => 0 } }

pub fn both(slot: Option<fn() -> u8>) -> u8 {
    match slot {
        Some(both) if both() > 0 => { both() }
        _ => 0,
    }
}
