// A library function ending the whole process; only fn main may decide that.
pub fn fail_hard(code: i32) {
    std::process::exit(code);
}
