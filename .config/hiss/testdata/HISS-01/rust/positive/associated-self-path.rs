pub struct Tree;

impl Tree {
    // count is an associated function with no receiver; Self::count reaches it.
    pub fn count(n: u32) -> u32 {
        if n == 0 {
            return 0;
        }
        1 + Self::count(n - 1)
    }
}
