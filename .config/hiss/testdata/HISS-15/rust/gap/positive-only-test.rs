// Only the positive dimension is asserted. The negative dimension (an inverted range) and
// the boundary dimension (i32::MIN, i32::MAX, value exactly at low and at high) are absent.
#[cfg(test)]
mod tests {
    use super::clamp;

    #[test]
    fn inside_range() {
        assert_eq!(clamp(5, 0, 10), 5);
    }
}
