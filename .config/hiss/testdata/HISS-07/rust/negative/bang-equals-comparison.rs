// A macro call needs its delimiter after the bang. An identifier spelled like an abort macro
// and compared with != is an ordinary expression, not a call to todo! or panic!.
pub fn pending(todo: usize, panic: usize) -> bool {
    if todo != 0 {
        return true;
    }
    panic!=0
}
