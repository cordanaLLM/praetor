pub struct Counter {
    left: u32,
}

impl Counter {
    // drain re-enters itself through the receiver.
    pub fn drain(&mut self) -> u32 {
        if self.left == 0 {
            return 0;
        }
        self.left -= 1;
        1 + self.drain()
    }
}
