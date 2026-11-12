// Package shellparse provides conservative analysis of shell editing buffers.
package shellparse

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexed is a lexical result only: it does not establish the command's identity,
// option semantics, argument slots, or whether a word is an alias or assignment.
// Current is the word at the byte cursor, or the insertion index between words.
// Prefix is its decoded literal content before the cursor. Uncertain results
// contain no words and must not be used to produce completion context.
type WordRange struct {
	Start int
	End   int
}

type Lexed struct {
	Certain bool
	Words   []string
	Ranges  []WordRange // Raw UTF-8 byte offsets, including quotes and escapes.
	Current int
	Prefix  string
}

// Lex recognizes a deliberately small literal subset of zsh syntax. The byte
// cursor must be on a UTF-8 boundary and outside an escape pair. Expansions,
// operators and incomplete quoting are rejected rather than interpreted.
func Lex(line string, cursor int) Lexed {
	if len(line) > 65536 || !utf8.ValidString(line) || cursor < 0 || cursor > len(line) || !utf8.ValidString(line[:cursor]) {
		return Lexed{}
	}
	result := Lexed{Words: []string{}, Current: -1}
	for i := 0; i < len(line); {
		if line[i] == ' ' || line[i] == '\t' {
			if i == cursor && result.Current < 0 {
				result.Current = len(result.Words)
			}
			i++
			continue
		}
		start := i
		var value strings.Builder
		var prefix string
		foundCursor := false
		quote := byte(0)
		for i < len(line) {
			if i == cursor {
				prefix, foundCursor = value.String(), true
			}
			c := line[i]
			if quote == 0 && (c == ' ' || c == '\t') {
				break
			}
			if c == '\'' && quote != '"' {
				// RC_QUOTES changes two consecutive quotes inside single quotes
				// from closing/reopening into a literal quote.
				if quote == '\'' && i+1 < len(line) && line[i+1] == '\'' {
					return Lexed{}
				}
				if quote == 0 {
					quote = '\''
				} else {
					quote = 0
				}
				i++
				continue
			}
			if c == '"' && quote != '\'' {
				if quote == 0 {
					quote = '"'
				} else {
					quote = 0
				}
				i++
				continue
			}
			if c == '\\' && quote != '\'' {
				if i+1 >= len(line) || cursor == i+1 {
					return Lexed{}
				}
				next := line[i+1]
				if quote == '"' && next != '"' && next != '\\' && next != '$' && next != '`' {
					return Lexed{}
				}
				if next == '\n' || next == '\r' {
					return Lexed{}
				}
				r, n := utf8.DecodeRuneInString(line[i+1:])
				if unicode.IsControl(r) {
					return Lexed{}
				}
				value.WriteRune(r)
				i += n + 1
				continue
			}
			if quote != '\'' && strings.ContainsRune("$`!", rune(c)) {
				return Lexed{}
			}
			if quote == 0 && strings.ContainsRune("|&;<>()[ ]{}*?~#^", rune(c)) {
				return Lexed{}
			}
			r, n := utf8.DecodeRuneInString(line[i:])
			if unicode.IsControl(r) {
				return Lexed{}
			}
			value.WriteRune(r)
			i += n
		}
		if quote != 0 {
			return Lexed{}
		}
		if i == cursor {
			prefix, foundCursor = value.String(), true
		}
		if foundCursor && cursor >= start && result.Current < 0 {
			result.Current, result.Prefix = len(result.Words), prefix
		}
		result.Words = append(result.Words, value.String())
		result.Ranges = append(result.Ranges, WordRange{Start: start, End: i})
	}
	if result.Current < 0 {
		result.Current = len(result.Words)
	}
	result.Certain = true
	return result
}
