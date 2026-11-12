package learning

import "sort"

// RankInput holds scores for a candidate already admitted by the caller's
// matching and rule constraints. Inputs must be in base-ranking order.
type RankInput struct {
	Base          float64
	SelectedCount uint64
	Recency       float64
}

// Rank returns candidate indices in descending learned-score order, preserving
// base order on ties. If any score is invalid, it returns the entire original
// order and an error. It does not modify inputs or validate selection events.
func Rank(inputs []RankInput) ([]int, error) {
	order := make([]int, len(inputs))
	scores := make([]float64, len(inputs))
	for i := range order {
		order[i] = i
	}
	for i, input := range inputs {
		score, err := Score(input.Base, input.SelectedCount, input.Recency)
		if err != nil {
			return order, err
		}
		scores[i] = score
	}
	sort.SliceStable(order, func(i, j int) bool {
		return scores[order[i]] > scores[order[j]]
	})
	return order, nil
}
