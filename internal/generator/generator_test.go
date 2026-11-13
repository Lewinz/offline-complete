package generator

import (
	"os"
	"path/filepath"
	"testing"

	"offline-complete/internal/rules"
)

func TestTemplateGeneratorsAreBoundedAndFiltered(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := Runner{MaxCandidates: 10}
	got := runner.Complete(dir, []rules.Generator{{Template: []string{"filepaths"}}}, "s")
	if len(got) != 1 || got[0].Name != "src/" || got[0].Description != "目录" {
		t.Fatalf("filepaths = %#v", got)
	}
	got = runner.Complete(dir, []rules.Generator{{Template: []string{"filepaths"}}}, "src/")
	if len(got) != 1 || got[0].Name != "src/main.go" {
		t.Fatalf("nested filepaths = %#v", got)
	}
	got = runner.Complete(dir, []rules.Generator{{Template: []string{"folders"}}}, "")
	if len(got) != 1 || got[0].Name != "src/" {
		t.Fatalf("folders = %#v", got)
	}
	got = runner.Complete(dir, []rules.Generator{{Template: []string{"filepaths"}}}, "S")
	if len(got) != 1 || got[0].Name != "src/" {
		t.Fatalf("case-insensitive filepaths = %#v", got)
	}
}

func TestScriptGeneratorRunsAnyCommandInCWD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Runner{}.Complete(dir, []rules.Generator{{Script: []string{"ls"}}}, "a")
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("ls = %#v", got)
	}
	got = Runner{}.Complete(dir, []rules.Generator{{Script: []string{"ls"}}}, "A")
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("case-insensitive ls = %#v", got)
	}
	got = Runner{}.Complete(dir, []rules.Generator{{Script: []string{"sh", "-c", "printf 'from-shell\\n'"}}}, "")
	if len(got) != 1 || got[0].Name != "from-shell" {
		t.Fatalf("sh = %#v", got)
	}
}
