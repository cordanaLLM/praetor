// A method implementing a trait takes its contract from the trait declaration, which is where
// clippy's missing_safety_doc asks for the # Safety section. The impl methods need no docs of
// their own; the unsafe impl itself and each unsafe block inside still carry a proof.
use std::alloc::{GlobalAlloc, Layout, System};

struct Counting;

// SAFETY: every call is forwarded unchanged to the system allocator.
unsafe impl GlobalAlloc for Counting {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        // SAFETY: the caller's layout contract is the system allocator's.
        unsafe { System.alloc(layout) }
    }

    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        // SAFETY: ptr came from System.alloc with this layout.
        unsafe { System.dealloc(ptr, layout) }
    }
}
