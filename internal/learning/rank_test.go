package learning

import (
	"math"
	"reflect"
	"testing"
)

func TestRankUsesLearningAndKeepsTiesStable(t *testing.T) {
	candidates := []RankInput{
		{Base: 0.5},
		{Base: 0.4, SelectedCount: 1, Recency: 1},
		{Base: 0.5},
	}
	original := append([]RankInput(nil), candidates...)
	got, err := Rank(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{1, 0, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(candidates, original) {
		t.Fatalf("Rank modified input: %v", candidates)
	}
}

func TestRankInvalidLearningFallsBackToWholeBaseOrder(t *testing.T) {
	candidates := []RankInput{
		{Base: 0.1, SelectedCount: 20, Recency: 1},
		{Base: 0.9},
		{Base: 0.5, SelectedCount: 1, Recency: math.NaN()},
	}
	got, err := Rank(candidates)
	if err == nil {
		t.Fatal("expected invalid ranking input error")
	}
	if want := []int{0, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback order = %v, want %v", got, want)
	}
}
