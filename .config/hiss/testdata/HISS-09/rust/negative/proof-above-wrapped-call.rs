// A proof above a statement reaches a block in the statement's wrapped call arguments, its
// method chain and the right-hand side of an assignment, as clippy's
// accept-comment-above-statement does.
fn first(p: *const u8) -> u8 {
    // SAFETY: p is non-null, aligned and points at an initialised byte.
    unsafe { *p }
}

pub fn sum(p: *const u8, q: &[*const u8]) -> u32 {
    let mut total;
    // SAFETY: p is non-null, aligned and points at an initialised byte.
    total =
        u32::from(unsafe { first(p) });
    // SAFETY: p is non-null, aligned and points at an initialised byte.
    let pair = u32::max(
        total,
        u32::from(unsafe { first(p) }),
    );
    // SAFETY: every pointer in q is non-null, aligned and initialised.
    let rest: u32 = q
        .iter()
        .map(|b| u32::from(unsafe { first(*b) }))
        .sum();
    total = pair + rest;
    total
}
