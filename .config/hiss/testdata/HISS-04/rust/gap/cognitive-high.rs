// Cognitive complexity 28 (seven nesting levels) in 18 lines. No Rust cognitive-complexity
// mechanism exists in this repository.
fn deeply_nested(mut count: i32) -> i32 {
    if count > 0 {
        if count > 1 {
            if count > 2 {
                if count > 3 {
                    if count > 4 {
                        if count > 5 {
                            if count > 6 {
                                count += 1;
                            }
                        }
                    }
                }
            }
        }
    }
    count
}
