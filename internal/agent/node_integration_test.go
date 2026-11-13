package agent_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"offline-complete/internal/agent"
	"offline-complete/internal/editstate"
	"offline-complete/internal/generator"
	"offline-complete/internal/rules"
)

func TestCompleterUsesEncryptedSpecWithNodeRuntime(t *testing.T) {
	node := generator.NewNodeRunner()
	if !node.Available() {
		t.Skip("node is unavailable")
	}
	defer node.Close()
	data, err := rules.LoadFile(filepath.Join("..", "..", "rules", "base.bin"))
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	specs := &rules.SpecStore{Dir: filepath.Join("..", "..", "rules", "specs.enc")}
	completer := &agent.Completer{
		Pack:       pack,
		Grammar:    pack.Grammar(),
		Specs:      specs,
		Generators: &generator.Runner{Node: node},
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "file"}, {"commit", "-qm", "initial"}, {"branch", "feature-test"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	line := "git checkout fe"
	offer, ok := completer.Complete(editstate.Snapshot{Version: 2, Line: line, Cursor: len(line), CWD: dir})
	if !ok || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "feature-test" {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
}

func TestCompleterCompletesAutojumpAliasJ(t *testing.T) {
	node := generator.NewNodeRunner()
	if !node.Available() {
		t.Skip("node is unavailable")
	}
	defer node.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	autojumpDir := filepath.Join(home, "Library", "autojump")
	if err := os.MkdirAll(autojumpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := "100\t/Users/example/projects/alpha\n50\t/Users/example/docs/notes\n"
	if err := os.WriteFile(filepath.Join(autojumpDir, "autojump.txt"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	packData, err := rules.LoadFile(filepath.Join("..", "..", "rules", "base.bin"))
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rules.FromJSON(packData)
	if err != nil {
		t.Fatal(err)
	}
	specs := &rules.SpecStore{Dir: filepath.Join("..", "..", "rules", "specs.enc")}
	node.Specs = specs
	completer := &agent.Completer{Pack: pack, Grammar: pack.Grammar(), Specs: specs, Generators: &generator.Runner{Node: node}}
	line := "j al"
	offer, ok := completer.Complete(editstate.Snapshot{Version: 1, Line: line, Cursor: len(line)})
	if !ok || len(offer.Candidates) != 1 {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
	candidate := offer.Candidates[0]
	if candidate.Name != "alpha" || candidate.Description != "/Users/example/projects/alpha" {
		t.Fatalf("candidate = %#v", candidate)
	}
}

func TestCompleterLoadsCrossSpecSubcommands(t *testing.T) {
	node := generator.NewNodeRunner()
	if !node.Available() {
		t.Skip("node is unavailable")
	}
	defer node.Close()
	data, err := rules.LoadFile(filepath.Join("..", "..", "rules", "base.bin"))
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	specs := &rules.SpecStore{Dir: filepath.Join("..", "..", "rules", "specs.enc")}
	node.Specs = specs
	completer := &agent.Completer{Pack: pack, Grammar: pack.Grammar(), Specs: specs, Generators: &generator.Runner{Node: node}}
	line := "aws s3 l"
	offer, ok := completer.Complete(editstate.Snapshot{Version: 1, Line: line, Cursor: len(line)})
	if !ok || len(offer.Candidates) == 0 || offer.Candidates[0].Name != "ls" {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
}
