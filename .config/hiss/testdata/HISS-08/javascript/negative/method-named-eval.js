// A method named eval on a hand-written interpreter executes nothing dynamically, and a call
// through a selector reaches that method, not the builtin.
export class Interpreter {
  eval(node) {
    return node.value;
  }
}

export function run(interpreter, node) {
  return interpreter.eval(node);
}
