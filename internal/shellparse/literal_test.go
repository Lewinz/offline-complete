package shellparse

import (
	"os/exec"
	"strings"
	"testing"
)

func TestZshLiteralRoundTrip(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	for _, value := range []string{"", "plain", "a b", "it's", "café/文", "$(printf injected)", "`printf injected`", "!$*;|&<>\\\"", "-leading"} {
		t.Run(value, func(t *testing.T) {
			literal, err := ZshLiteral(value)
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(zsh, "-f", "-c", "print -rn -- "+literal).Output()
			if err != nil || string(out) != value {
				t.Fatalf("literal %q: got %q, err %v; want %q", literal, out, err, value)
			}
			lexed := Lex(literal, len(literal))
			if !lexed.Certain || len(lexed.Words) != 1 || lexed.Words[0] != value {
				t.Fatalf("literal %q does not round-trip through Lex: %+v", literal, lexed)
			}
		})
	}
}

func TestZshLiteralRoundTripWithRCQuotes(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	for _, value := range []string{"'", "'start", "end'", "a''b"} {
		literal, err := ZshLiteral(value)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(zsh, "-f", "-o", "rcquotes", "-c", "print -rn -- "+literal).Output()
		if err != nil || string(out) != value {
			t.Errorf("value %q, literal %q: got %q, err %v", value, literal, out, err)
		}
	}
}

func TestZshWordLiteralRoundTrip(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	for _, test := range []struct {
		value string
		bare  bool
	}{
		{"commit", true},
		{"--message", true},
		{"foo/bar", true},
		{"a:b@c+d,e", true},
		{"", false},
		{"a b", false},
		{"it's", false},
		{"~root", false},
		{"=ls", false},
		{"#comment", false},
		{"$HOME", false},
		{"*.go", false},
		{"it's", false},
	} {
		literal, err := ZshWordLiteral(test.value)
		if err != nil {
			t.Fatalf("value %q: %v", test.value, err)
		}
		if test.bare && literal != test.value {
			t.Errorf("value %q was quoted as %q", test.value, literal)
		}
		if !test.bare && literal == test.value && test.value != "" {
			t.Errorf("value %q was not quoted", test.value)
		}
		out, err := exec.Command(zsh, "-f", "-c", "print -rn -- "+literal).Output()
		if err != nil || string(out) != test.value {
			t.Fatalf("literal %q: got %q, err %v; want %q", literal, out, err, test.value)
		}
	}
}

func TestZshWordLiteralRejectsUnsafeInput(t *testing.T) {
	for _, value := range []string{"\x00", "line\nbreak", "\x1b[31m", string([]byte{0xff}), strings.Repeat("a", 65537)} {
		if literal, err := ZshWordLiteral(value); err == nil {
			t.Errorf("accepted %q as %q", value, literal)
		}
	}
}

func TestZshLiteralRejectsUnsafeInput(t *testing.T) {
	for _, value := range []string{"\x00", "line\nbreak", "tab\t", "\x1b[31m", string([]byte{0xff}), strings.Repeat("a", 65537)} {
		if literal, err := ZshLiteral(value); err == nil {
			t.Errorf("accepted %q as %q", value, literal)
		}
	}
}
