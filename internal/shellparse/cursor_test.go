package shellparse

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestZshCursorByteOffsets(t *testing.T) {
	line := "aé界é"
	for _, tc := range []struct{ chars, bytes int }{
		{0, 0}, {1, 1}, {2, 3}, {3, 6}, {4, 7}, {5, 9},
	} {
		got, err := ZshCursorToByte(line, tc.chars)
		if err != nil || got != tc.bytes {
			t.Errorf("characters %d: got %d, %v; want byte offset %d", tc.chars, got, err, tc.bytes)
		}
		back, err := ByteCursorToZsh(line, tc.bytes)
		if err != nil || back != tc.chars {
			t.Errorf("bytes %d: got %d, %v; want character offset %d", tc.bytes, back, err, tc.chars)
		}
	}
}

func TestZshMultibyteStringCounts(t *testing.T) {
	path, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not available")
	}
	cmd := exec.Command(path, "-f", "-c", `setopt multibyte; value='aé界é'; print -r -- ${#value}`)
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "5" {
		t.Fatalf("zsh multibyte string length = %q, want 5", got)
	}
}

func TestZshCursorEmptyLine(t *testing.T) {
	if got, err := ZshCursorToByte("", 0); err != nil || got != 0 {
		t.Fatalf("empty character cursor: %d, %v", got, err)
	}
	if got, err := ByteCursorToZsh("", 0); err != nil || got != 0 {
		t.Fatalf("empty byte cursor: %d, %v", got, err)
	}
}

func TestZshCursorRejectsInvalidOffsetsAndEncoding(t *testing.T) {
	for _, tc := range []struct {
		line string
		pos  int
	}{
		{"é", -1}, {"é", 2}, {"\xff", 0},
	} {
		if _, err := ZshCursorToByte(tc.line, tc.pos); err == nil {
			t.Errorf("character offset %d in %q accepted", tc.pos, tc.line)
		}
	}
	for _, tc := range []struct {
		line string
		pos  int
	}{
		{"é", -1}, {"é", 1}, {"é", 3}, {"\xff", 0},
	} {
		if _, err := ByteCursorToZsh(tc.line, tc.pos); err == nil {
			t.Errorf("byte offset %d in %q accepted", tc.pos, tc.line)
		}
	}
}
