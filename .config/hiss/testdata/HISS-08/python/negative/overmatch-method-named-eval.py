# A bound method named eval on a hand-written AST interpreter executes no dynamic code:
# it walks a parsed tree. The line matcher accepts any non-identifier byte before the
# name, so the leading '.' does not stop it and this legitimate file IS reported today.
class Interpreter:
    def eval(self, node):
        return node.value


def run(interpreter, node):
    return interpreter.eval(node)
