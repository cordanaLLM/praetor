// A closure bound to the function's own name shadows it; the call reaches the closure.
pub fn step(n: u32) -> u32 {
    let step = |x: u32| x + 1;
    step(n)
}

// A parameter of the same name shadows it too.
pub fn apply(apply: fn(u32) -> u32, n: u32) -> u32 {
    apply(n)
}
