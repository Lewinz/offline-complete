package learning

import (
	"errors"
	"time"
)

// Observation contains only an already-validated observation's session day
// and whether its associated command exited successfully. The caller must
// verify the association, eligibility, privacy and retention before use.
type Observation struct {
	Day     string
	Success bool
}

// SuggestionEligible checks the threshold for observations of one candidate.
// Invalid days fail the entire calculation, including after the threshold is met.
// It does not authorize creating or persisting a suggestion.
func SuggestionEligible(observations []Observation) (bool, error) {
	var days [3]string
	count := 0
	success := false
	for _, observation := range observations {
		date, err := time.Parse("2006-01-02", observation.Day)
		var formatted [10]byte
		if err != nil || string(date.AppendFormat(formatted[:0], "2006-01-02")) != observation.Day || date.Year() == 0 {
			return false, errors.New("invalid observation day")
		}
		if count < len(days) {
			seen := false
			for _, day := range days[:count] {
				seen = seen || day == observation.Day
			}
			if !seen {
				days[count] = observation.Day
				count++
			}
		}
		success = success || observation.Success
	}
	return count == len(days) && success, nil
}
