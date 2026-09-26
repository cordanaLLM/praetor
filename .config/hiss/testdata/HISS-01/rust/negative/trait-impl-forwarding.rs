// Inside `impl Trait for T`, self.f and Self::f do not name the trait method being defined.
// rustc compiles both of these with -D unconditional_recursion and they terminate.
pub trait Ones {
    fn count_ones(&self) -> u32;
}

pub struct W(u32);

impl W {
    pub fn count_ones(&self) -> u32 {
        self.0.count_ones()
    }
}

// An inherent method outranks the trait method, so self.count_ones() reaches W::count_ones.
impl Ones for W {
    fn count_ones(&self) -> u32 {
        self.count_ones()
    }
}

pub struct A;
pub struct B;
pub struct E;

impl From<B> for E {
    fn from(_: B) -> Self {
        E
    }
}

// Self::from picks the From impl by argument type, so Self::from(B) reaches From<B>.
impl From<A> for E {
    fn from(_a: A) -> Self {
        Self::from(B)
    }
}
