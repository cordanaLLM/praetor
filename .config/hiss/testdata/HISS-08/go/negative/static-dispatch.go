package p

// Handler is a statically linked capability: nothing is chosen at runtime, so the
// HISS-08 axiom permits this shape.
type Handler interface{ Handle(string) error }

// Dispatch calls a capability supplied by the caller.
func Dispatch(h Handler, arg string) error {
	return h.Handle(arg)
}
