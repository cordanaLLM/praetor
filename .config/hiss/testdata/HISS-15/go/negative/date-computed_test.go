package p
import "time"
var manifest = "expires: " + time.Now().AddDate(0, 0, 14).Format(time.DateOnly)
