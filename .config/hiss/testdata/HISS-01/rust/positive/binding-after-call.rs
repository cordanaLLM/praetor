// A let shadows only the code after its semicolon, so the call above it, and the call in its
// own initializer, still reach countdown.
pub fn countdown(n: u32) -> u32 {
    if n > 0 {
        countdown(n - 1);
    }
    let countdown = 0;
    countdown
}

pub fn fold(n: u32) -> u32 {
    let fold = fold(n - 1);
    fold
}

// A name followed by a parenthesis is a call, not a match pattern.
pub fn depth(n: u32) -> u32 {
    let r = match depth(n - 1) { 0 => 1, d => d + 1 };
    r
}

// A closure parameter, a for pattern, an if-let pattern and a let in an inner block go out of
// scope where their construct ends; the call after it reaches the function.
pub fn walk(n: u32) -> u32 {
    let inc = |walk: u32| walk + 1;
    for walk in 0..n {
        let _ = walk;
    }
    if let Some(walk) = n.checked_sub(1) {
        let _ = walk;
    }
    {
        let walk = 1;
        let _ = walk;
    }
    inc(walk(n - 1))
}
