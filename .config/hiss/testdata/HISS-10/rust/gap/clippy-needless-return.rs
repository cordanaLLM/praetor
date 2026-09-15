// clippy warns `needless_return` on the explicit trailing return.
pub fn double(n: i32) -> i32 {
    return n * 2;
}
