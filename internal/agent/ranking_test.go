package agent_test

import (
	"os"
	"path/filepath"
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
	ranker.Record(context, "", "commit")

	candidates := []rules.Candidate{{Name: "checkout"}, {Name: "commit"}}
	ranked := ranker.Rank(context, "", candidates)
	if len(ranked) != 2 || ranked[0].Name != "commit" {
		t.Fatalf("ranked = %+v", ranked)
	}

	if err := learningStore.SetLearning(false); err != nil {
		t.Fatal(err)
	}
	unranked := ranker.Rank(context, "", candidates)
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
		ranker.Record(context, "", "../")
	}

	candidates := []rules.Candidate{{Name: "../"}, {Name: "alpha/"}, {Name: "beta/"}}
	ranked := ranker.Rank(context, "", candidates)
	if len(ranked) != 3 || ranked[0].Name != "alpha/" || ranked[1].Name != "beta/" || ranked[2].Name != "../" {
		t.Fatalf("ranked = %+v", ranked)
	}
}

func TestStoreRankerIsolatesRepositoryScopes(t *testing.T) {
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
	ranker.Record(context, "/repo/a", "commit")

	candidates := []rules.Candidate{{Name: "checkout"}, {Name: "commit"}}
	if ranked := ranker.Rank(context, "/repo/a", candidates); ranked[0].Name != "commit" {
		t.Fatalf("repo a ranked = %+v", ranked)
	}
	if ranked := ranker.Rank(context, "/repo/b", candidates); ranked[0].Name != "checkout" {
		t.Fatalf("repo b ranked = %+v", ranked)
	}
}

func TestDirectoryScopeFindsRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := agent.DirectoryScope(nested); got != resolved {
		t.Fatalf("scope = %q, want %q", got, resolved)
	}
	if got := agent.DirectoryScope(t.TempDir()); got != "" {
		t.Fatalf("non-repository scope = %q", got)
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
		ranker.RecordCorrection(context, "", "Downloads/", "Documents/")
	}
	personal := ranker.PersonalCandidates(context, "", 10)
	if len(personal) != 1 || personal[0].Name != "Downloads/" || personal[0].Description != "个人纠正" {
		t.Fatalf("personal = %+v", personal)
	}
	candidates := []rules.Candidate{{Name: "Documents/"}, personal[0]}
	ranked := ranker.Rank(context, "", candidates)
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
		ranker.RecordCorrection(context, "", "Downloads/", "Documents/")
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

func (r *recordingRanker) Rank(shellparse.Context, string, []rules.Candidate) []rules.Candidate {
	return nil
}
func (r *recordingRanker) Record(context shellparse.Context, scope, candidate string) {
	r.context = context
	r.candidate = candidate
}
