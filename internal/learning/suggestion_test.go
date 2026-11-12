package learning

import (
	"reflect"
	"testing"
	"time"
)

func TestSuggestionEligibleBoundedAllocations(t *testing.T) {
	observations := make([]Observation, 2000)
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range observations {
		observations[i] = Observation{Day: start.AddDate(0, 0, i).Format("2006-01-02"), Success: i == 0}
	}
	allocs := testing.AllocsPerRun(3, func() {
		eligible, err := SuggestionEligible(observations)
		if err != nil || !eligible {
			t.Fatalf("got %v, %v", eligible, err)
		}
	})
	if allocs > 10 {
		t.Fatalf("processing observations allocated %.0f times; expected bounded allocations", allocs)
	}
}

func TestSuggestionEligible(t *testing.T) {
	tests := []struct {
		name         string
		observations []Observation
		want         bool
		wantErr      bool
	}{
		{"empty", nil, false, false},
		{"two days with success", []Observation{{"2026-11-01", true}, {"2026-11-02", false}}, false, false},
		{"same day repeated", []Observation{{"2026-11-01", true}, {"2026-11-01", false}, {"2026-11-01", false}}, false, false},
		{"three days without success", []Observation{{"2026-11-01", false}, {"2026-11-02", false}, {"2026-11-03", false}}, false, false},
		{"three days with success", []Observation{{"2026-11-01", false}, {"2026-11-02", true}, {"2026-11-03", false}}, true, false},
		{"leap day", []Observation{{"2024-02-28", false}, {"2024-02-29", false}, {"2024-03-01", true}}, true, false},
		{"invalid first", []Observation{{"2025-02-29", false}, {"2026-11-01", true}, {"2026-11-02", false}, {"2026-11-03", false}}, false, true},
		{"invalid last", []Observation{{"2026-11-01", true}, {"2026-11-02", false}, {"2026-11-03", false}, {"2026-13-01", false}}, false, true},
		{"noncanonical", []Observation{{"2026-1-01", true}}, false, true},
		{"empty day", []Observation{{"", true}}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]Observation(nil), tt.observations...)
			got, err := SuggestionEligible(tt.observations)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("SuggestionEligible() = %v, %v; want %v, error %v", got, err, tt.want, tt.wantErr)
			}
			if !reflect.DeepEqual(tt.observations, before) {
				t.Fatal("observations modified")
			}
		})
	}
}
