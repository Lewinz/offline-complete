package agent

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"offline-complete/internal/diag"
	"offline-complete/internal/learning"
	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
	"offline-complete/internal/store"
)

// Ranker reorders matching candidates and records explicit selections. Scope
// is the git repository root for the current working directory, or empty
// outside a repository. A nil ranker preserves base order and records nothing.
type Ranker interface {
	Rank(shellparse.Context, string, []rules.Candidate) []rules.Candidate
	Record(shellparse.Context, string, string)
}

// PersonalSource contributes candidates learned from manual corrections.
type PersonalSource interface {
	PersonalCandidates(shellparse.Context, string, int) []rules.Candidate
}

// CorrectionRecorder records a manual correction without requiring the
// correction to have been in the original candidate list.
type CorrectionRecorder interface {
	RecordCorrection(shellparse.Context, string, string, string)
}

// StoreRanker applies the local learning store. Selection learning only sees
// the validated context and candidate name; manual corrections are stored
// locally so they can be replayed as personal candidates.
type StoreRanker struct {
	Store  *store.Store
	Now    func() time.Time
	Logger *diag.Logger
}

// Rank keeps base order when learning is disabled, the store is unavailable,
// or any score is invalid.
func (r *StoreRanker) Rank(context shellparse.Context, scope string, candidates []rules.Candidate) []rules.Candidate {
	if r == nil || r.Store == nil || !r.Store.Learning() || len(candidates) == 0 {
		return candidates
	}
	now := r.now()
	normal := make([]rules.Candidate, 0, len(candidates))
	parents := make([]rules.Candidate, 0, 1)
	for _, candidate := range candidates {
		// Upstream Kiro filepath specs append "../" after the sorted entries.
		// Learning must not promote it above real directories just because it
		// was selected repeatedly.
		if isParentPath(candidate.Name) {
			parents = append(parents, candidate)
			continue
		}
		normal = append(normal, candidate)
	}
	if len(normal) == 0 {
		return candidates
	}
	inputs := make([]learning.RankInput, len(normal))
	for index, candidate := range normal {
		selection, ok := r.Store.Lookup(contextKey(context, scope, candidate.Name))
		recency := 0.0
		count := uint64(0)
		if ok {
			count = selection.Count
			if value, err := learning.Recency(selection.Last, now); err == nil {
				recency = value
			}
		}
		confidence := 0.0
		correctionRecency := 0.0
		correctionPenalty := 0.0
		if correction, ok := r.Store.Correction(contextScopeKey(context, scope), candidate.Name); ok {
			confidence = learning.WilsonLower(correction.Wins, correction.Losses, 1.645)
			if value, err := learning.Recency(correction.Last, now); err == nil {
				correctionRecency = value
			}
			correctionPenalty = 0.15 * math.Min(math.Log1p(float64(correction.Losses)), 3)
		}
		inputs[index] = learning.RankInput{
			Base:                 1,
			SelectedCount:        count,
			Recency:              recency,
			CorrectionConfidence: confidence,
			CorrectionRecency:    correctionRecency,
			CorrectionPenalty:    correctionPenalty,
		}
	}
	order, err := learning.Rank(inputs)
	if err != nil {
		return candidates
	}
	ranked := make([]rules.Candidate, 0, len(candidates))
	for _, original := range order {
		ranked = append(ranked, normal[original])
	}
	return append(ranked, parents...)
}

func isParentPath(name string) bool {
	trimmed := strings.TrimSuffix(strings.TrimSpace(name), "/")
	return trimmed == ".." || strings.HasSuffix(trimmed, "/..")
}

// Record stores one explicit selection. Failures are ignored so a broken data
// file cannot block completion.
func (r *StoreRanker) Record(context shellparse.Context, scope, candidate string) {
	if r == nil || r.Store == nil || isParentPath(candidate) {
		return
	}
	if err := r.Store.Record(contextKey(context, scope, candidate), r.now()); err != nil {
		r.Logger.Printf("event=learning_record_error err=%q", err)
		return
	}
	if r.Logger.Text() {
		r.Logger.Printf("event=learning_recorded command=%q subcommands=%q candidate=%q", context.Command, strings.Join(context.Subcommands, "/"), candidate)
	} else {
		r.Logger.Printf("event=learning_recorded command=%q subcommands=%q candidate_len=%d", context.Command, strings.Join(context.Subcommands, "/"), len(candidate))
	}
}

// PersonalCandidates exposes values learned from repeated manual corrections.
// They are merged into the normal candidate list before ranking.
func (r *StoreRanker) PersonalCandidates(context shellparse.Context, scope string, limit int) []rules.Candidate {
	if r == nil || r.Store == nil || limit <= 0 {
		return nil
	}
	learned := r.Store.PersonalCandidates(contextScopeKey(context, scope), context.Prefix, limit)
	result := make([]rules.Candidate, 0, len(learned))
	for _, candidate := range learned {
		result = append(result, rules.Candidate{
			Name:        candidate.Value,
			Description: "个人纠正",
		})
	}
	return result
}

// RecordCorrection records a value the user submitted instead of the offered
// candidate. rejected may be empty when no candidate was offered.
func (r *StoreRanker) RecordCorrection(context shellparse.Context, scope, candidate, rejected string) {
	if r == nil || r.Store == nil || isParentPath(candidate) {
		return
	}
	if err := r.Store.RecordCorrection(contextScopeKey(context, scope), candidate, rejected, r.now()); err != nil {
		r.Logger.Printf("event=correction_record_error err=%q", err)
		return
	}
	if r.Logger.Text() {
		r.Logger.Printf("event=correction_recorded command=%q subcommands=%q candidate=%q rejected=%q",
			context.Command, strings.Join(context.Subcommands, "/"), candidate, rejected)
	} else {
		r.Logger.Printf("event=correction_recorded command=%q subcommands=%q candidate_len=%d rejected_len=%d",
			context.Command, strings.Join(context.Subcommands, "/"), len(candidate), len(rejected))
	}
}

func (r *StoreRanker) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func contextScopeKey(context shellparse.Context, scope string) string {
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
	base := strings.Join([]string{
		context.Command,
		strings.Join(context.Subcommands, "/"),
		kind,
		strconv.Itoa(slot),
	}, "\x00")
	if scope == "" {
		return base
	}
	return "repo\x00" + scope + "\x00" + base
}

func contextKey(context shellparse.Context, scope, candidate string) string {
	return contextScopeKey(context, scope) + "\x00" + candidate
}

// DirectoryScope returns the git repository root containing cwd, or an empty
// scope outside a repository. Learning and corrections are isolated per root,
// so branch/value preferences from one repository do not leak into another.
// It only inspects .git entries and never runs git.
func DirectoryScope(cwd string) string {
	if cwd == "" {
		return ""
	}
	path, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	for depth := 0; depth < 64; depth++ {
		info, err := os.Stat(filepath.Join(path, ".git"))
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return ""
}
