package learning

import (
	"math"
	"testing"
)

func TestScore(t *testing.T) {
	cases := []struct {
		name    string
		base    float64
		count   uint64
		recency float64
		want    float64
	}{
		{"base only", 0.7, 0, 0, 0.7},
		{"one selection", 0.2, 1, 1, 0.2 + 0.15*math.Ln2 + 0.10},
		{"frequency cap", -0.5, 1000, 0.5, 0},
		{"large count", 0, math.MaxUint64, 1, 0.55},
		{"negative base", -1, 0, 0, -1},
		{"largest finite base", math.MaxFloat64, 1, 1, math.MaxFloat64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Score(tc.base, tc.count, tc.recency)
			if err != nil || math.IsNaN(got) || math.Abs(got-tc.want) > 1e-12 {
				t.Fatalf("Score = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestScoreRejectsInvalidLearningData(t *testing.T) {
	for _, tc := range []struct {
		base    float64
		count   uint64
		recency float64
	}{
		{math.NaN(), 1, 0}, {math.Inf(1), 1, 0}, {math.Inf(-1), 1, 0},
		{0, 1, math.NaN()}, {0, 1, math.Inf(1)}, {0, 1, -0.1}, {0, 1, 1.1},
		{0, 0, 0.1},
	} {
		if _, err := Score(tc.base, tc.count, tc.recency); err == nil {
			t.Errorf("Score(%v, %d, %v) accepted invalid input", tc.base, tc.count, tc.recency)
		}
	}
}
