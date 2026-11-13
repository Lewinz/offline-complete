package agent_test

import (
	"os"
	"testing"
	"time"

	"offline-complete/internal/agent"
	"offline-complete/internal/editstate"
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

func TestStoreRankerKeepsParentPathLast(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	learningStore, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ranker := &agent.StoreRanker{Store: learningStore, Now: func() time.Time { return time.Now().UTC() }}
	context := shellparse.Context{Certain: true, Command: "cd", Kind: shellparse.Positional}
	for i := 0; i < 5; i++ {
		ranker.Record(context, "../")
	}

	candidates := []rules.Candidate{{Name: "../"}, {Name: "alpha/"}, {Name: "beta/"}}
	ranked := ranker.Rank(context, candidates)
	if len(ranked) != 3 || ranked[0].Name != "alpha/" || ranked[1].Name != "beta/" || ranked[2].Name != "../" {
		t.Fatalf("ranked = %+v", ranked)
	}
}

func TestStoreRankerAddsRepeatedPersonalCorrections(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	learningStore, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ranker := &agent.StoreRanker{Store: learningStore, Now: func() time.Time { return now }}
	context := shellparse.Context{Certain: true, Command: "cd", Kind: shellparse.Positional, Prefix: "Do"}
	for day := 0; day < 3; day++ {
		now = now.AddDate(0, 0, day)
		ranker.RecordCorrection(context, "Downloads/", "Documents/")
	}
	personal := ranker.PersonalCandidates(context, 10)
	if len(personal) != 1 || personal[0].Name != "Downloads/" || personal[0].Description != "个人纠正" {
		t.Fatalf("personal = %+v", personal)
	}
	candidates := []rules.Candidate{{Name: "Documents/"}, personal[0]}
	ranked := ranker.Rank(context, candidates)
	if len(ranked) != 2 || ranked[0].Name != "Downloads/" {
		t.Fatalf("ranked = %+v", ranked)
	}
}

func TestCompleterAddsPersonalCandidateToExistingContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	learningStore, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ranker := &agent.StoreRanker{Store: learningStore, Now: func() time.Time { return now }}
	context := shellparse.Context{Certain: true, Command: "cd", Kind: shellparse.Positional, Prefix: "Do"}
	for day := 0; day < 3; day++ {
		now = now.AddDate(0, 0, day)
		ranker.RecordCorrection(context, "Downloads/", "Documents/")
	}
	pack := &rules.RulePack{Commands: []rules.Command{{Name: "cd", Description: "Change directory"}}}
	completer := &agent.Completer{
		Pack:    pack,
		Grammar: map[string]shellparse.CommandSpec{"cd": {Positional: true}},
		Ranker:  ranker,
	}
	line := "cd Do"
	offer, ok := completer.Complete(editstate.Snapshot{Version: 1, Line: line, Cursor: len(line)})
	if !ok || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "Downloads/" {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
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
