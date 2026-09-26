// The standard expression macros evaluate their input where it stands, so a self-call there
// is a call.
pub fn height(n: u32) -> u32 {
    assert!(height(n - 1) < n);
    println!("{}", height(n - 2));
    n
}
