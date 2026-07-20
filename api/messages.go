package api

import (
	"fmt"
	"strconv"
)

func errTooManyStops(got, max int) error {
	return fmt.Errorf("too many stops: %d (limit is %d per request)", got, max)
}

func badCoord(i int, lat, lng float64) error {
	return fmt.Errorf("stop %d has out-of-range coordinates (lat=%.4f, lng=%.4f); lat must be -90..90 and lng -180..180", i, lat, lng)
}

func defaultName(i int) string {
	if i == 0 {
		return "Depot"
	}
	return "Stop " + strconv.Itoa(i)
}
