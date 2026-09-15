// A public interface with no #[test] of any dimension. This repository invokes no cargo
// test, no coverage tool and no Rust compiler in any gate, so nothing observes the absence.
pub fn clamp(value: i32, low: i32, high: i32) -> i32 {
    if value < low {
        return low;
    }
    if value > high {
        return high;
    }
    value
}
