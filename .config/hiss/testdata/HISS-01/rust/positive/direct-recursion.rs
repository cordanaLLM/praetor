// factorial calls itself by its bare name, which HISS-01 forbids.
pub fn factorial(n: u64) -> u64 {
    if n <= 1 {
        return 1;
    }
    n * factorial(n - 1)
}
