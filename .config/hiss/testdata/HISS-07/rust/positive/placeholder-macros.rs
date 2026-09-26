// todo!, unimplemented! and unreachable! abort as surely as panic! does.
pub fn mode(n: u8) -> &'static str {
    match n {
        0 => todo!(),
        1 => unimplemented!("mode one"),
        _ => unreachable!(),
    }
}
