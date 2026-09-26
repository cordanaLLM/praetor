// A brace closed inside a match arm or a closure body ends neither: the if-else branch, the
// nested match and the block below all sit inside the expression that bound the name, so each
// call reaches the local fn pointer, not the function.
pub fn pick(slot: Option<fn() -> u8>, c: bool) -> u8 {
    match slot {
        Some(pick) => if c { 1 } else { pick() },
        None => 0,
    }
}

pub fn wrapped(slot: Option<fn() -> u8>, c: bool) -> u8 {
    match slot {
        Some(wrapped) => if c {
            1
        } else {
            wrapped()
        },
        None => 0,
    }
}

pub fn chained(slot: Option<fn() -> u8>, c: u8) -> u8 {
    match slot {
        Some(chained) => if c == 0 { 1 } else if c == 1 { 2 } else { chained() }
        None => 0,
    }
}

pub fn larger(slot: Option<fn() -> u8>) -> u8 {
    match slot {
        Some(larger) => match 1 { _ => 2u8 }.max(larger()),
        None => 0,
    }
}

pub fn sum(handlers: &[fn() -> u8], c: bool) -> u8 {
    handlers.iter().map(|sum| if c { 0 } else { sum() }).sum()
}

pub fn total(handlers: &[fn() -> u8]) -> u8 {
    handlers.iter().map(|total| { 0 } + total()).sum()
}

pub enum K {
    A,
    B,
}

// The pipe of an or-pattern is not a closure's opening pipe.
pub fn each(k: K, handlers: &[fn() -> u8]) -> u8 {
    match k {
        K::A | K::B => handlers.iter().map(|each| each()).sum(),
    }
}
