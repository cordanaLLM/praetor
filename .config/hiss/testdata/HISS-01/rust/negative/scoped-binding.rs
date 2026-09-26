// While a local of the function's name is in scope, a bare call reaches the local.
pub fn each(handlers: &[fn()]) {
    for each in handlers {
        each();
    }
}

pub fn first(slot: Option<fn() -> u8>) -> u8 {
    if let Some(first) = slot {
        return first();
    }
    match slot {
        Some(first) => first(),
        None => 0,
    }
}

pub fn apply(pairs: &[(u32, fn(u32) -> u32)]) -> u32 {
    pairs.iter().map(|(n, apply)| apply(*n)).sum()
}

pub fn run(n: u32) -> u32 {
    let run = |x: u32| x + 1;
    {
        run(n);
    }
    run(n)
}
