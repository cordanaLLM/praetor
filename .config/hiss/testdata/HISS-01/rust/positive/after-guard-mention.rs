// A guard that names the arm's local again leaves no binding behind for the next arm, whose
// call reaches the function.
pub fn fire(slot: Option<fn()>) -> u8 {
    match slot {
        Some(fire) if fire as usize != 0 => 1,
        _ => fire(None),
    }
}
