// A function written in a macro_rules template is not decided. Here fn_impl's body is the
// $body fragment, so the brace after its header opens the outer function's unsafe block, and
// the calls in it reach fn_impl from the function the template names, not fn_impl itself.
macro_rules! dispatch {
    ($name:ident, $x:ident, $body:block) => {
        pub fn $name(n: u32) -> u32 {
            unsafe fn fn_impl($x: u32) -> u32 $body
            unsafe {
                if n > 1 {
                    fn_impl(n)
                } else {
                    fn_impl(0)
                }
            }
        }
    };
}

dispatch!(run, n, { n + 1 });
