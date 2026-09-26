// Each of these is impl Trait for T, where self.count reaches an inherent W::count first: an
// attribute on the header's line, a brace in a const generic argument, a semicolon in an array
// type, and a trait impl that follows a trait alias ending in a semicolon.
#[allow(unused)] impl Ones for W {
    fn count(&self) -> u32 {
        self.count()
    }
}

impl Ones for Grid<{ N + 1 }> {
    fn count(&self) -> u32 {
        self.count()
    }
}

impl<const N: usize> Ones for [u8; N] {
    fn count(&self) -> u32 {
        self.count()
    }
}

trait Countable = Ones + Clone;
impl Ones for Cell {
    fn count(&self) -> u32 {
        self.count()
    }
}
