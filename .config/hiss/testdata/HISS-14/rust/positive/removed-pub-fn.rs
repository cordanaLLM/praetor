// HISS-14: the published `pub fn connect(addr: &str) -> Conn` has been deleted and
// replaced by `dial`. Every downstream crate that called it stops compiling, so the
// public contract is not append-only. Only the commit message decides the outcome.
pub struct Conn {
    pub addr: String,
}

pub fn dial(addr: &str) -> Conn {
    Conn { addr: addr.to_string() }
}
