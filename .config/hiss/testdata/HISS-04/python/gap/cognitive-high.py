# Cognitive complexity 28 (seven nesting levels) in 11 lines.
def deeply_nested(count):
    if count > 0:
        if count > 1:
            if count > 2:
                if count > 3:
                    if count > 4:
                        if count > 5:
                            if count > 6:
                                count += 1
    return count
