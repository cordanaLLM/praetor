/// Doc comment above an attribute that is never closed before fn.
#[cfg_attr(feature = "trace"
pub fn unclosed_attr(a: u32) -> u32 {
    a
}

/// Doc comment above an attribute that is not closed within the 32-line bound.
#[cfg_attr(
    feature = "f01",
    feature = "f02",
    feature = "f03",
    feature = "f04",
    feature = "f05",
    feature = "f06",
    feature = "f07",
    feature = "f08",
    feature = "f09",
    feature = "f10",
    feature = "f11",
    feature = "f12",
    feature = "f13",
    feature = "f14",
    feature = "f15",
    feature = "f16",
    feature = "f17",
    feature = "f18",
    feature = "f19",
    feature = "f20",
    feature = "f21",
    feature = "f22",
    feature = "f23",
    feature = "f24",
    feature = "f25",
    feature = "f26",
    feature = "f27",
    feature = "f28",
    feature = "f29",
    feature = "f30",
    feature = "f31",
    feature = "f32",
    feature = "f33",
)]
pub fn over_bound_attr(a: u32) -> u32 {
    a
}
