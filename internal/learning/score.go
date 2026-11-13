// Package learning provides offline learning calculations. It does not persist
// data or determine whether a selection was authorized.
package learning

import (
	"errors"
	"math"
)

// WilsonLower returns a one-sided Wilson score lower bound for wins out of
// wins+losses. It keeps a small number of noisy corrections from immediately
// outranking established candidates.
func WilsonLower(wins, losses uint64, z float64) float64 {
	total := float64(wins) + float64(losses)
	if total == 0 || z <= 0 || math.IsNaN(z) || math.IsInf(z, 0) {
		return 0
	}
	p := float64(wins) / total
	z2 := z * z
	denominator := 1 + z2/total
	center := p + z2/(2*total)
	margin := z * math.Sqrt(p*(1-p)/total+z2/(4*total*total))
	result := (center - margin) / denominator
	if result < 0 {
		return 0
	}
	if result > 1 {
		return 1
	}
	return result
}

// Score combines a finite base score with an explicit selection count and a
// caller-supplied recency score in [0, 1]. A candidate with no selections must
// have zero recency. The caller is responsible for filtering candidates,
// expiring observations, and falling back to the base order on error.
func Score(base float64, selectedCount uint64, recency float64) (float64, error) {
	return ScoreWithCorrection(base, selectedCount, recency, 0, 0, 0)
}

// ScoreWithCorrection combines selection frequency, recency and correction
// evidence. A correction is an explicit disagreement with the offered list, so
// it gets a stronger positive weight; repeated rejection of a candidate gets a
// separate penalty.
func ScoreWithCorrection(base float64, selectedCount uint64, recency, correctionConfidence, correctionRecency, correctionPenalty float64) (float64, error) {
	if math.IsNaN(base) || math.IsInf(base, 0) ||
		math.IsNaN(recency) || math.IsInf(recency, 0) || recency < 0 || recency > 1 ||
		math.IsNaN(correctionConfidence) || math.IsInf(correctionConfidence, 0) || correctionConfidence < 0 || correctionConfidence > 1 ||
		math.IsNaN(correctionRecency) || math.IsInf(correctionRecency, 0) || correctionRecency < 0 || correctionRecency > 1 ||
		math.IsNaN(correctionPenalty) || math.IsInf(correctionPenalty, 0) || correctionPenalty < 0 ||
		(selectedCount == 0 && recency != 0) {
		return 0, errors.New("invalid ranking input")
	}
	frequency := math.Min(math.Log1p(float64(selectedCount)), 3)
	score := base + 0.15*frequency + 0.10*recency + 0.50*correctionConfidence + 0.10*correctionRecency - correctionPenalty
	if math.IsInf(score, 0) || math.IsNaN(score) {
		return 0, errors.New("invalid ranking result")
	}
	return score, nil
}
