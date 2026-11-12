package shellparse

import (
	"reflect"
	"testing"
)

func TestLiteralWordsAndCursor(t *testing.T) {
	cases := []struct {
		line   string
		cursor int
		words  []string
		index  int
		prefix string
	}{
		{"git commit --amend", 18, []string{"git", "commit", "--amend"}, 2, "--amend"},
		{"git  status", 4, []string{"git", "status"}, 1, ""},
		{"git 'hello world' a\\ b", 17, []string{"git", "hello world", "a b"}, 1, "hello world"},
		{"git café", 7, []string{"git", "café"}, 1, "caf"},
		{"git status", 7, []string{"git", "status"}, 1, "sta"},
	}
	for _, tc := range cases {
		got := Lex(tc.line, tc.cursor)
		if !got.Certain || !reflect.DeepEqual(got.Words, tc.words) || got.Current != tc.index || got.Prefix != tc.prefix {
			t.Errorf("Lex(%q, %d) = %+v", tc.line, tc.cursor, got)
		}
	}
}

func TestAmbiguousShellSyntaxIsNotCertain(t *testing.T) {
	for _, line := range []string{
		"git | grep x", "git; echo x", "git $(whoami)", "git $HOME", "git *.go", "git #comment", "git 'unfinished", "git \\", "git \"$HOME\"", "git >file", "git\nstatus", "git ~user", "git {a,b}", "git !foo", "git \"!foo\"", "git ^foo",
	} {
		if got := Lex(line, len(line)); got.Certain {
			t.Errorf("Lex(%q) unexpectedly certain: %+v", line, got)
		}
	}
}

func TestAdjacentSingleQuotesAreUncertain(t *testing.T) {
	for _, line := range []string{"git 'a''b'", "git 'a''"} {
		if got := Lex(line, len(line)); got.Certain {
			t.Errorf("Lex(%q) unexpectedly certain: %+v", line, got)
		}
	}
}

func TestInvalidCursorAndEncodingAreNotCertain(t *testing.T) {
	for _, tc := range []struct {
		line   string
		cursor int
	}{
		{"git", -1}, {"git", 4}, {"git café", 8}, {"git \\", 5}, {"git \xff", 5},
	} {
		if got := Lex(tc.line, tc.cursor); got.Certain {
			t.Errorf("Lex(%q, %d) unexpectedly certain: %+v", tc.line, tc.cursor, got)
		}
	}
}
