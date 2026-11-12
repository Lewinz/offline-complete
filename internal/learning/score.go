// Package learning provides offline learning calculations. It does not persist
// data or determine whether a selection was authorized.
package learning

import (
	"errors"
	"math"
)

// Score combines a finite base score with an explicit selection count and a
// caller-supplied recency score in [0, 1]. A candidate with no selections must
// have zero recency. The caller is responsible for filtering candidates,
// expiring observations, and falling back to the base order on error.
func Score(base float64, selectedCount uint64, recency float64) (float64, error) {
	if math.IsNaN(base) || math.IsInf(base, 0) || math.IsNaN(recency) || math.IsInf(recency, 0) || recency < 0 || recency > 1 || (selectedCount == 0 && recency != 0) {
		return 0, errors.New("invalid ranking input")
	}
	frequency := math.Min(math.Log1p(float64(selectedCount)), 3)
	score := base + 0.15*frequency + 0.10*recency
	if math.IsInf(score, 0) || math.IsNaN(score) {
		return 0, errors.New("invalid ranking result")
	}
	return score, nil
}
