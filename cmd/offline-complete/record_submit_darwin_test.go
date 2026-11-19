//go:build darwin

package main

import (
	"bytes"
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

func TestRecordSubmitPreferencesAnOfferedCandidateWithinRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	state := &editstate.State{}
	line := "git fetch upstream main"
	if err := state.UpdateWithCWD(1, line, len(line), repo); err != nil {
		t.Fatal(err)
	}
	pack := &rules.RulePack{SchemaVersion: 1, Commands: []rules.Command{{
		Name: "git",
		Subcommands: []rules.Command{{
			Name: "fetch",
			Args: []rules.Arg{
				{Suggestions: []rules.Candidate{{Name: "upstream"}}},
				{Suggestions: []rules.Candidate{{Name: "dev"}, {Name: "main"}}},
			},
		}},
	}}}
	learningDir := t.TempDir()
	if err := os.Chmod(learningDir, 0o700); err != nil {
		t.Fatal(err)
	}
	learningStore, err := store.Open(learningDir)
	if err != nil {
		t.Fatal(err)
	}
	ranker := &agent.StoreRanker{Store: learningStore, Now: time.Now}
	completer := &agent.Completer{Pack: pack, Grammar: pack.Grammar(), Ranker: ranker}
	session, err := agent.NewSession("session-1", state, completer, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := session.Refresh(true); !ok {
		t.Fatal("offer missing for typed branch")
	}

	runtime := &agentRuntime{state: state, completer: completer, completion: session}
	runtime.recordSubmit()

	scope := agent.DirectoryScope(repo)
	if scope == "" {
		t.Fatal("repository scope missing")
	}
	context := shellparse.Context{
		Certain:     true,
		Command:     "git",
		Subcommands: []string{"fetch"},
		Kind:        shellparse.Positional,
		Slot:        1,
	}
	ranked := ranker.Rank(context, scope, []rules.Candidate{{Name: "dev"}, {Name: "main"}})
	if len(ranked) != 2 || ranked[0].Name != "main" {
		t.Fatalf("ranked = %+v", ranked)
	}
}
