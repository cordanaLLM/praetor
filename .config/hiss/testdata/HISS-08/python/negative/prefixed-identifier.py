def compute(expression):
    total = evaluate(expression)
    total += my_eval(expression)
    return executor(total)
