// A function written in a macro_rules template is not decided, so this self-call is real
// recursion that goes unreported: its signature holds a $metavariable, and the scanner cannot
// see the template's expansion.
macro_rules! countdown {
    ($t:ty) => {
        pub fn countdown(n: $t) -> $t {
            if n == 0 { 0 } else { countdown(n - 1) }
        }
    };
}

countdown!(u32);
