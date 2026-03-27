package downloader

import (
	"math"
	"time"
)

// DaysLeftCeil возвращает число суток до момента expiry относительно now: длительность до истечения
// делится на 24 часа и округляется вверх (ceil). Если expiry не позже now (уже истекло или совпадает), возвращает 0.
func DaysLeftCeil(now, expiry time.Time) int {
	if !expiry.After(now) {
		return 0
	}
	sub := expiry.Sub(now)
	return int(math.Ceil(sub.Hours() / 24))
}
