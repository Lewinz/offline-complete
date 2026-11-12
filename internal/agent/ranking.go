package agent

import (
	"strconv"
	"strings"
	"time"

	"offline-complete/internal/diag"
	"offline-complete/internal/learning"
	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
	"offline-complete/internal/store"
)

// Ranker reorders matching candidates and records explicit selections. A nil
// ranker preserves base order and records nothing.
type Ranker interface {
	Rank(shellparse.Context, []rules.Candidate) []rules.Candidate
	Record(shellparse.Context, string)
}

// StoreRanker applies the local learning store. It never sees free text,
// argument values or paths: only the validated context and candidate name.
type StoreRanker struct {
	Store  *store.Store
	Now    func() time.Time
	Logger *diag.Logger
}

// Rank keeps base order when learning is disabled, the store is unavailable,
// or any score is invalid.
func (r *StoreRanker) Rank(context shellparse.Context, candidates []rules.Candidate) []rules.Candidate {
	if r == nil || r.Store == nil || !r.Store.Learning() || len(candidates) == 0 {
		return candidates
	}
	now := r.now()
	inputs := make([]learning.RankInput, len(candidates))
	for index, candidate := range candidates {
		selection, ok := r.Store.Lookup(contextKey(context, candidate.Name))
		recency := 0.0
		count := uint64(0)
		if ok {
			count = selection.Count
			if value, err := learning.Recency(selection.Last, now); err == nil {
				recency = value
			}
		}
		inputs[index] = learning.RankInput{
			Base:          1,
			SelectedCount: count,
			Recency:       recency,
		}
	}
	order, err := learning.Rank(inputs)
	if err != nil {
		return candidates
	}
	ranked := make([]rules.Candidate, len(candidates))
	for index, original := range order {
		ranked[index] = candidates[original]
	}
	return ranked
}

// Record stores one explicit selection. Failures are ignored so a broken data
// file cannot block completion.
func (r *StoreRanker) Record(context shellparse.Context, candidate string) {
	if r == nil || r.Store == nil {
		return
	}
	if err := r.Store.Record(contextKey(context, candidate), r.now()); err != nil {
		r.Logger.Printf("event=learning_record_error err=%q", err)
		return
	}
	if r.Logger.Text() {
		r.Logger.Printf("event=learning_recorded command=%q subcommands=%q candidate=%q", context.Command, strings.Join(context.Subcommands, "/"), candidate)
	} else {
		r.Logger.Printf("event=learning_recorded command=%q subcommands=%q candidate_len=%d", context.Command, strings.Join(context.Subcommands, "/"), len(candidate))
	}
}

func (r *StoreRanker) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func contextKey(context shellparse.Context, candidate string) string {
	kind := "sub"
	switch context.Kind {
	case shellparse.OptionName:
		kind = "opt"
	case shellparse.Positional:
		kind = "pos"
	}
	slot := 0
	if context.Kind == shellparse.Positional {
		slot = context.Slot
	}
	return strings.Join([]string{
		context.Command,
		strings.Join(context.Subcommands, "/"),
		kind,
		strconv.Itoa(slot),
		candidate,
	}, "\x00")
}
