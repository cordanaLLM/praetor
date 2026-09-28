struct Pair {
    first: u8,
}

fn make(p: *const u8) -> Pair {
    // SAFETY: clippy does not carry a proof above the statement into a struct literal field.
    let pair = Pair {
        first: unsafe { *p },
    };
    pair
}
