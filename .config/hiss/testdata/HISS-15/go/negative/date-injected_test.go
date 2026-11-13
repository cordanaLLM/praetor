package p
import "time"
var base = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
var expiry = base.AddDate(0, 0, -1).Format(time.DateOnly)
