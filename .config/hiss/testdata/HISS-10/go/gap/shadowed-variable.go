package p

// Shadowed rebinds out inside the block, so the outer out keeps its original value and the
// recomputed one is silently discarded. The govet shadow analyzer reports this; the
// repository enables govet without it.
func Shadowed(text string, flag bool) string {
	out := "outer"
	if flag {
		out := text + "!"
		if len(out) > 100 {
			return "long"
		}
	}
	return out
}
