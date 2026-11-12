package learning

import (
	"testing"
	"time"
)

func TestRecency(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		age  time.Duration
		want float64
	}{
		{"now", 0, 1},
		{"half retention", 45 * 24 * time.Hour, 0.5},
		{"retention boundary", 90 * 24 * time.Hour, 0},
		{"older", 120 * 24 * time.Hour, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Recency(now.Add(-tc.age), now)
			if err != nil || got != tc.want {
				t.Fatalf("Recency = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestRecencyRejectsInvalidTimes(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ last, now time.Time }{
		{time.Time{}, now}, {now, time.Time{}}, {now.Add(time.Nanosecond), now},
	} {
		if _, err := Recency(tc.last, tc.now); err == nil {
			t.Errorf("Recency(%v, %v) accepted invalid times", tc.last, tc.now)
		}
	}
}
