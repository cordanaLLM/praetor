// The test module closes before the helper; its #[cfg(test)] must not reach past the brace.
#[cfg(test)]
mod tests {
    fn fixture(v: Option<i32>) -> i32 {
        v.unwrap()
    }
}

pub fn helper(v: Option<i32>) -> i32 {
    v.unwrap()
}
