// 55 statements in 13 lines: over the HISS-04 statement cap of 50 while far under the
// length cap, which is the only thing measured for Rust.
fn many_statements(mut count: i32) -> i32 {
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count += 1; count += 1; count += 1; count += 1; count += 1;
    count
}
