package agent_test

import (
	"os"
	"testing"
	"time"

	"offline-complete/internal/agent"
	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
	"offline-complete/internal/store"
)

func TestStoreRankerPromotesSelectedCandidate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	learningStore, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ranker := &agent.StoreRanker{Store: learningStore, Now: func() time.Time { return time.Now().UTC() }}
	context := shellparse.Context{Certain: true, Command: "git", Kind: shellparse.Subcommand}
	ranker.Record(context, "commit")

	candidates := []rules.Candidate{{Name: "checkout"}, {Name: "commit"}}
	ranked := ranker.Rank(context, candidates)
	if len(ranked) != 2 || ranked[0].Name != "commit" {
		t.Fatalf("ranked = %+v", ranked)
	}

	if err := learningStore.SetLearning(false); err != nil {
		t.Fatal(err)
	}
	unranked := ranker.Rank(context, candidates)
	if len(unranked) != 2 || unranked[0].Name != "checkout" {
		t.Fatalf("disabled ranker changed order: %+v", unranked)
	}
}

type recordingRanker struct {
	context   shellparse.Context
	candidate string
}

func (r *recordingRanker) Rank(shellparse.Context, []rules.Candidate) []rules.Candidate { return nil }
func (r *recordingRanker) Record(context shellparse.Context, candidate string) {
	r.context = context
	r.candidate = candidate
}
