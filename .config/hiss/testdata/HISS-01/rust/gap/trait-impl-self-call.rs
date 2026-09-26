// With no inherent next on Countdown, self.next() inside the trait impl reaches this very
// method. Telling that apart from forwarding needs every inherent impl of the type in the
// crate, so self-calls inside `impl Trait for T` are not decided.
pub struct Countdown(u32);

impl Iterator for Countdown {
    type Item = u32;

    fn next(&mut self) -> Option<u32> {
        if self.0 == 0 {
            return None;
        }
        self.0 -= 1;
        self.next()
    }
}
