package agent_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"offline-complete/internal/agent"
	"offline-complete/internal/editstate"
	"offline-complete/internal/generator"
	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
)

func testCompleter() *agent.Completer {
	pack := &rules.RulePack{
		SchemaVersion: 1,
		Commands: []rules.Command{
			{
				Name: "git",
				Subcommands: []rules.Command{
					{
						Name:    "commit",
						Options: []rules.Option{{Name: "--message", Description: "Commit message"}},
					},
				},
			},
		},
	}
	grammar := map[string]shellparse.CommandSpec{
		"git": {
			Subcommands: map[string]shellparse.CommandSpec{
				"commit": {
					Options:      map[string]bool{"--message": true},
					Positional:   true,
					EndOfOptions: true,
				},
			},
		},
	}
	return &agent.Completer{Pack: pack, Grammar: grammar}
}

func TestCompleterProducesStaticCandidates(t *testing.T) {
	completer := testCompleter()
	offer, ok := completer.Complete(editstate.Snapshot{Version: 4, Line: "git co", Cursor: 6})
	if !ok {
		t.Fatal("no offer")
	}
	if offer.Version != 4 || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "commit" {
		t.Fatalf("offer = %+v", offer)
	}

	offer, ok = completer.Complete(editstate.Snapshot{Version: 5, Line: "git commit --m", Cursor: 14})
	if !ok || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "--message" {
		t.Fatalf("option offer = %+v ok=%v", offer, ok)
	}
}

func TestCompleterRejectsUncertainOrEmptyContext(t *testing.T) {
	completer := testCompleter()
	cases := []editstate.Snapshot{
		{Version: 1, Line: "git unknown", Cursor: 11},
		{Version: 1, Line: "git $(echo x)", Cursor: 13},
		{Version: 1, Line: "git co", Cursor: 99},
		{Version: 1, Line: "git co", Cursor: -1},
	}
	for _, snapshot := range cases {
		if _, ok := completer.Complete(snapshot); ok {
			t.Errorf("accepted snapshot %+v", snapshot)
		}
	}
}

func TestApplyReplacesTheCurrentWord(t *testing.T) {
	snapshot := editstate.Snapshot{Version: 9, Line: "git co", Cursor: 6}
	edited, err := agent.Apply(snapshot, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if edited.Version != 9 || edited.Line != "git commit" || edited.Cursor != 10 {
		t.Fatalf("edited = %+v", edited)
	}

	snapshot = editstate.Snapshot{Version: 9, Line: "git co --all", Cursor: 6}
	edited, err = agent.Apply(snapshot, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if edited.Line != "git commit --all" || edited.Cursor != 10 {
		t.Fatalf("mid-line edited = %+v", edited)
	}
}

func TestWriteLineReplacementDoesNotInjectControlCharacters(t *testing.T) {
	for _, line := range []string{"git\ncommit", "git\x1b[D", "git\tcommit", "git\x7f"} {
		if err := agent.WriteLineReplacement(&bytes.Buffer{}, line, 0); err == nil {
			t.Errorf("accepted line %q", line)
		}
	}
	if err := agent.WriteLineReplacement(nil, "git commit", 0); err == nil {
		t.Fatal("nil writer accepted")
	}
}

func TestWriteLineReplacementMovesCursorFromTheEnd(t *testing.T) {
	var output bytes.Buffer
	if err := agent.WriteLineReplacement(&output, "git commit", 3); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.HasPrefix(got, "\x05\x15git commit") {
		t.Fatalf("prefix = %q", got)
	}
	if count := strings.Count(got, "\x1b[D"); count != 7 {
		t.Fatalf("left arrows = %d, want 7", count)
	}
}

func twoCandidateCompleter() *agent.Completer {
	pack := &rules.RulePack{
		SchemaVersion: 1,
		Commands: []rules.Command{
			{
				Name: "git",
				Subcommands: []rules.Command{
					{Name: "commit"},
					{Name: "checkout"},
				},
			},
		},
	}
	return &agent.Completer{
		Pack: pack,
		Grammar: map[string]shellparse.CommandSpec{
			"git": {
				Subcommands: map[string]shellparse.CommandSpec{
					"commit":   {Positional: true},
					"checkout": {Positional: true},
				},
			},
		},
	}
}

func TestCompleterRunsPositionalGeneratorsWithSnapshotCWD(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	pack := &rules.RulePack{
		SchemaVersion: 1,
		Commands: []rules.Command{{
			Name: "tool",
			Subcommands: []rules.Command{{
				Name: "open",
				Args: []rules.Arg{{
					Name:        "path",
					Suggestions: []rules.Candidate{{Name: "manual", Description: "fixed"}},
					Generators:  []rules.Generator{{Template: []string{"filepaths"}}},
				}},
			}},
		}},
	}
	grammar := map[string]shellparse.CommandSpec{
		"tool": {Subcommands: map[string]shellparse.CommandSpec{"open": {Positional: true}}},
	}
	completer := &agent.Completer{
		Pack:       pack,
		Grammar:    grammar,
		Generators: &generator.Runner{},
	}
	line := "tool open "
	offer, ok := completer.Complete(editstate.Snapshot{
		Version: 3,
		Line:    line,
		Cursor:  len(line),
		CWD:     dir,
	})
	if !ok || len(offer.Candidates) != 2 {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
	if offer.Candidates[0].Name != "manual" || offer.Candidates[1].Name != "project/" {
		t.Fatalf("candidates = %#v", offer.Candidates)
	}
}

func TestCompleterRunsGitBranchGeneratorInCWD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "file"}, {"commit", "-qm", "initial"}, {"branch", "feature-test"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	pack := &rules.RulePack{
		SchemaVersion: 1,
		Commands: []rules.Command{{
			Name: "git",
			Subcommands: []rules.Command{{
				Name: "checkout",
				Args: []rules.Arg{{
					Name:       "branch",
					Generators: []rules.Generator{{Script: []string{"git", "branch", "--no-color"}}},
				}},
			}},
		}},
	}
	grammar := map[string]shellparse.CommandSpec{
		"git": {Subcommands: map[string]shellparse.CommandSpec{"checkout": {Positional: true}}},
	}
	completer := &agent.Completer{Pack: pack, Grammar: grammar, Generators: &generator.Runner{}}
	line := "git checkout feature"
	offer, ok := completer.Complete(editstate.Snapshot{Version: 2, Line: line, Cursor: len(line), CWD: dir})
	if !ok || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "feature-test" {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
}

func TestCompleterPreservesInlineOptionPrefix(t *testing.T) {
	pack := &rules.RulePack{SchemaVersion: 1, Commands: []rules.Command{{
		Name: "tool",
		Options: []rules.Option{{
			Name: "--output",
			Args: []rules.Arg{{Name: "path", Suggestions: []rules.Candidate{{Name: "out.txt"}}}},
		}},
	}}}
	completer := &agent.Completer{Pack: pack, Grammar: pack.Grammar()}
	line := "tool --output=o"
	offer, ok := completer.Complete(editstate.Snapshot{Version: 1, Line: line, Cursor: len(line)})
	if !ok || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "--output=out.txt" {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
}
