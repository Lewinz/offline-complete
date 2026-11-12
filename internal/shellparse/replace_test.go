package shellparse

import (
	"os/exec"
	"strings"
	"testing"
)

func TestReplaceWordUsesWholeRawWord(t *testing.T) {
	for _, tc := range []struct {
		line       string
		cursor     int
		value      string
		want       string
		wantCursor int
	}{
		{"git 'old name' tail", 8, "it's new", "git 'it'\\''s new' tail", 17},
		{"git café tail", 7, "新 名", "git '新 名' tail", 13},
		{"git  tail", 4, "a b", "git 'a b' tail", 9},
		{"git ", 4, "$(evil)", "git '$(evil)'", 13},
		{"git ''", 6, "", "git ''", 6},
		{"git a\\ b tail", 7, "new", "git new tail", 7},
		{"git abc   ", 10, "x", "git abc   x", 11},
	} {
		got, cursor, err := ReplaceWord(tc.line, tc.cursor, tc.value)
		if err != nil || got != tc.want || cursor != tc.wantCursor {
			t.Errorf("ReplaceWord(%q, %d, %q) = %q, %d, %v; want %q, %d", tc.line, tc.cursor, tc.value, got, cursor, err, tc.want, tc.wantCursor)
		}
	}
}

func TestReplaceWordPreservesOtherArgumentsInZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	line, _, err := ReplaceWord("print -rNC1 -- before 'old value' after", 25, "$(printf injected); it's safe")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(zsh, "-f", "-c", line).Output()
	if err != nil || string(out) != "before\x00$(printf injected); it's safe\x00after\x00" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestReplaceWordRejectsUncertainOrUnsafeInput(t *testing.T) {
	for _, tc := range []struct {
		line   string
		cursor int
		value  string
	}{
		{"git 'unfinished", 8, "x"},
		{"git $(command)", 4, "x"},
		{"git café", 8, "x"},
		{"git a\\ b", 6, "x"},
		{"git x", 5, "bad\nvalue"},
		{"git x", 5, strings.Repeat("a", 65536)},
	} {
		if got, _, err := ReplaceWord(tc.line, tc.cursor, tc.value); err == nil {
			t.Errorf("accepted (%q, %d, %q): %q", tc.line, tc.cursor, tc.value, got)
		}
	}
}
