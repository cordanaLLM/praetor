// An arm whose body is block-like ends at the brace closing it, comma or not, so the next
// arm's call reaches the function again.
pub fn run(slot: Option<fn() -> u8>, c: bool) -> u8 {
    match slot {
        Some(run) => if c { 1 } else { run() }
        None => run(Some(|| 0), c),
    }
}

pub fn step(slot: Option<fn() -> u8>) -> u8 {
    match (slot, 1) {
        (Some(step), _) => { step() }
        (None, _) => step(None),
    }
}
