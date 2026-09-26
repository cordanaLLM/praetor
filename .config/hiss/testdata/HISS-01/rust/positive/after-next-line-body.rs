// An arm whose block body starts on the line after its arrow still ends at that block's
// closing brace, so the next arm's call reaches the function.
pub fn apply(slot: Option<fn() -> u8>) -> u8 {
    match slot {
        Some(apply) =>
            { apply() }
        None => apply(Some(|| 0)),
    }
}
