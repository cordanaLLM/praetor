// rustc warns `unused_variables` on this binding.
pub fn scale(values: &[i32]) -> i32 {
    let factor = 2;
    values.iter().sum()
}
