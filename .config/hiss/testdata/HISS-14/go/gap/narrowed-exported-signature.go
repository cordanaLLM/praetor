package p

import "context"

// HISS-14: Publish previously accepted (topic string, payload []byte). It now requires
// a leading context.Context, so every existing caller breaks. This is exactly as
// breaking as the positive fixture, but committed with an ordinary
// "fix(api): thread a ctx through the published entry point" header nothing in this
// repository notices: no analyzer compares the exported surface against the previous
// release (measured: exit 0).
func Publish(ctx context.Context, topic string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	return ctx.Err()
}
