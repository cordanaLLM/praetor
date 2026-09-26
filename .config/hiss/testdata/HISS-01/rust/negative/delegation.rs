use std::io;

pub struct Wrapper<W> {
    inner: W,
}

impl<W: io::Write> Wrapper<W> {
    // Forwarding to the same-named method on another value is delegation, not recursion.
    pub fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

mod codec {
    pub fn parse(s: &str) -> usize {
        s.len()
    }
}

// A path names a different item: codec::parse is not this parse.
pub fn parse(s: &str) -> usize {
    codec::parse(s)
}
