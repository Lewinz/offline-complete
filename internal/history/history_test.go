package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCandidatesSkipPureTraversalTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zsh_history")
	data := "cd ..\ncd ../sub2api/gateway\ncd ...\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	store := Load(path)
	got := store.Candidates("..", 10)
	if len(got) != 1 || got[0].Name != "../sub2api/gateway" {
		t.Fatalf("candidates = %#v", got)
	}
}

func TestLoadParsesZshExtendedHistoryAndReturnsRecentCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zsh_history")
	if err := os.WriteFile(path, []byte(": 1700000000:0;git checkout feature\n: 1700000001:0;git status\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := Load(path)
	if len(store.Entries) != 2 {
		t.Fatalf("entries = %#v", store.Entries)
	}
	got := store.Candidates("fe", 10)
	if len(got) != 1 || got[0].Name != "feature" {
		t.Fatalf("candidates = %#v", got)
	}
	if got := store.Candidates("FE", 10); len(got) != 1 || got[0].Name != "feature" {
		t.Fatalf("case-insensitive candidates = %#v", got)
	}
	if got := store.Candidates("", 10); len(got) != 0 {
		t.Fatalf("empty prefix returned %#v", got)
	}
}
