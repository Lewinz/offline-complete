package learning

import (
	"errors"
	"time"
)

const recencyWindow = 90 * 24 * time.Hour

// Recency returns a linearly decaying weight for the last explicit selection.
// It does not expire observations or adjust selection counts.
func Recency(lastSelection, now time.Time) (float64, error) {
	if lastSelection.IsZero() || now.IsZero() || lastSelection.After(now) {
		return 0, errors.New("invalid selection time")
	}
	age := now.Sub(lastSelection)
	if age >= recencyWindow {
		return 0, nil
	}
	return 1 - float64(age)/float64(recencyWindow), nil
}
