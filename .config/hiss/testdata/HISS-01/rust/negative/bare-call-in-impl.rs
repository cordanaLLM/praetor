pub fn render(v: u32) -> String {
    v.to_string()
}

pub struct View(u32);

impl View {
    // Inside an impl a bare call resolves to the free function render, not to this method.
    pub fn render(&self) -> String {
        render(self.0)
    }

    // The same holds for an associated function without a receiver.
    pub fn build(v: u32) -> String {
        render(v)
    }
}
