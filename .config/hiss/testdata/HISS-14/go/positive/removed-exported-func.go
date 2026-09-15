package p

// HISS-14: the exported function Connect(addr string) (*Conn, error), published in the
// previous release, has been deleted and replaced by Dial. Every caller that linked
// against Connect stops compiling, so the public contract is not append-only.
//
// What actually decides this: only the commit message. Committed as
// "feat(api)!: remove the published symbol" with no Migration: footer, the change is
// refused by `standardsctl forge check-commits` (measured: exit 1).
type Conn struct{ Addr string }

func Dial(addr string) (*Conn, error) { return &Conn{Addr: addr}, nil }
