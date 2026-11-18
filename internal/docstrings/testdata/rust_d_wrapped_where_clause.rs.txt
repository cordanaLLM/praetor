/// Clones `value` when `T` implements `Clone`.
pub fn clone_where<T>(
    value: &T,
) -> T
where
    T: Clone,
{
    value.clone()
}
