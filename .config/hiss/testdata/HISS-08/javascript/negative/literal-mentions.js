// eval(x) in a comment, a string, a template or a regular expression is text, and a timer
// given a function evaluates nothing.
export const note = "eval(x)" + `new Function(${"a"})` + /eval\(/.source;
setTimeout(() => refresh(), 100);
