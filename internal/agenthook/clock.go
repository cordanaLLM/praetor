package agenthook

import "time"

const clockContextFormat = "2006-01-02 15:04:05 MST"

func clockContext(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return "clock: " + now().In(time.Local).Format(clockContextFormat) +
		" (from the system clock; use this, never estimate)"
}
