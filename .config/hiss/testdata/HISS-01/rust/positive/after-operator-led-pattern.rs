// rustc ends a block-like arm body before a binary operator, so a `-` after it opens the next
// arm's pattern (a negative literal), and that arm's call reaches the function.
pub fn clamp(n: i32) -> i32 {
    match n {
        clamp if clamp > 10 => { 1 }
        -1 => clamp(n + 1),
        _ => 0,
    }
}
