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

func TestWilsonLowerRewardsConsistentCorrections(t *testing.T) {
	if got := WilsonLower(0, 0, 1.645); got != 0 {
		t.Fatalf("empty WilsonLower = %v", got)
	}
	one := WilsonLower(1, 0, 1.645)
	three := WilsonLower(3, 0, 1.645)
	five := WilsonLower(5, 0, 1.645)
	if !(one < three && three < five) {
		t.Fatalf("confidence should increase: one=%v three=%v five=%v", one, three, five)
	}
	if got := WilsonLower(1, 1, 1.645); got >= 0.5 {
		t.Fatalf("mixed evidence confidence = %v", got)
	}
}

func TestScoreWithCorrection(t *testing.T) {
	plain, err := ScoreWithCorrection(1, 0, 0, 0, 0, 0)
	if err != nil || plain != 1 {
		t.Fatalf("plain = %v, %v", plain, err)
	}
	corrected, err := ScoreWithCorrection(1, 0, 0, 0.5, 1, 0)
	if err != nil || corrected <= plain {
		t.Fatalf("corrected = %v, %v", corrected, err)
	}
	penalized, err := ScoreWithCorrection(1, 0, 0, 0, 0, 0.2)
	if err != nil || penalized >= plain {
		t.Fatalf("penalized = %v, %v", penalized, err)
	}
	if _, err := ScoreWithCorrection(1, 0, 0, 1.1, 0, 0); err == nil {
		t.Fatal("accepted invalid correction confidence")
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
