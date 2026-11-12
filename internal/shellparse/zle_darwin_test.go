//go:build darwin

package shellparse

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// This checks an actual ZLE cursor, not only zsh's string length operator.
func TestZLEMultibyteCursor(t *testing.T) {
	for _, tc := range []struct {
		name, locale, input, result string
		cursor, byteOffset          int
	}{
		{"end", "en_US.UTF-8", "aé界é\x18", "RESULT:5:aé界é", 5, 9},
		{"beginning", "en_US.UTF-8", "aé界é\x01\x18", "RESULT:0:aé界é", 0, 0},
		{"middle", "en_US.UTF-8", "aé界é\x01\x06\x06\x06\x18", "RESULT:3:aé界é", 3, 6},
		{"combining", "en_US.UTF-8", "aé界é\x01\x06\x06\x06\x06\x18", "RESULT:4:aé界é", 4, 7},
		{"other-locale", "ja_JP.UTF-8", "aé界é\x01\x06\x06\x18", "RESULT:2:aé界é", 2, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testZLEMultibyteCursor(t, tc.locale, tc.input, tc.result)
			if got, err := ZshCursorToByte("aé界é", tc.cursor); err != nil || got != tc.byteOffset {
				t.Fatalf("ZLE cursor %d: byte offset = %d, %v; want %d", tc.cursor, got, err, tc.byteOffset)
			}
			if got, err := ByteCursorToZsh("aé界é", tc.byteOffset); err != nil || got != tc.cursor {
				t.Fatalf("byte offset %d: cursor = %d, %v; want %d", tc.byteOffset, got, err, tc.cursor)
			}
		})
	}
}

func testZLEMultibyteCursor(t *testing.T, locale, input, result string) {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not available")
	}
	script := `setopt multibyte
function capture_cursor { zle -I; print -r -- "RESULT:${CURSOR}:${BUFFER}"; zle accept-line }
zle -N capture_cursor
bindkey '^X' capture_cursor
value=''
vared -p 'READY>' value`
	cmd := exec.Command(zsh, "-f", "-i", "-c", script)
	cmd.Env = append(os.Environ(), "LC_ALL="+locale, "TERM=dumb")
	master, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	output := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(master)
		var all bytes.Buffer
		for {
			b, err := reader.ReadByte()
			if err != nil {
				output <- all.String()
				return
			}
			all.WriteByte(b)
			if strings.HasSuffix(all.String(), "READY>") {
				_, _ = io.WriteString(master, input)
			}
		}
	}()
	select {
	case got := <-output:
		if !strings.Contains(got, result) {
			t.Fatalf("unexpected ZLE output: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ZLE did not complete")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}
