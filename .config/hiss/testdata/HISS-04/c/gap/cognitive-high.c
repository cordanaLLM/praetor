/* Cognitive complexity 28 (seven nesting levels) in 18 lines. */
int deeply_nested(int count) {
    if (count > 0) {
        if (count > 1) {
            if (count > 2) {
                if (count > 3) {
                    if (count > 4) {
                        if (count > 5) {
                            if (count > 6) {
                                count++;
                            }
                        }
                    }
                }
            }
        }
    }
    return count;
}
