package forge

import "time"

// ReviewAge is shared by the CLI and stop verdict. Future timestamps clamp at zero.
func ReviewAge(now, created time.Time) string {
	age := now.Sub(created)
	if age < 0 {
		age = 0
	}
	return age.Truncate(time.Second).String()
}
