// Only a method of the impl takes the trait's contract. A local unsafe fn declared inside the
// method's body is a free function of its own and documents its own contract.
struct Plain;

impl Clone for Plain {
    fn clone(&self) -> Self {
        unsafe fn helper() {}
        Plain
    }
}
