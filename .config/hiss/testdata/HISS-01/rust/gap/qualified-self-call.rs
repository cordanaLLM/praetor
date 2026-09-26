// A self-call spelled through a path or with a turbofish is not matched: the bare-name form
// is the only spelling decided for a free function.
pub fn sum<T: Copy + Into<u64>>(items: &[T]) -> u64 {
    match items.split_first() {
        Some((head, rest)) => (*head).into() + sum::<T>(rest),
        None => 0,
    }
}

pub fn depth(n: u32) -> u32 {
    if n == 0 {
        return 0;
    }
    1 + crate::depth(n - 1)
}
