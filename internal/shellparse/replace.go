package shellparse

import "errors"

// ReplaceWord prepares a complete-word replacement in a zsh editing buffer.
// Cursor and the returned cursor are UTF-8 byte offsets. This only transforms
// text: callers must separately verify explicit selection, word context,
// session identity, focus and buffer version before applying the result.
func ReplaceWord(line string, cursor int, value string) (string, int, error) {
	parsed := Lex(line, cursor)
	if !parsed.Certain {
		return "", 0, errors.New("uncertain shell syntax or cursor")
	}
	literal, err := ZshWordLiteral(value)
	if err != nil {
		return "", 0, err
	}
	start, end := cursor, cursor
	if parsed.Current < len(parsed.Ranges) {
		word := parsed.Ranges[parsed.Current]
		if cursor >= word.Start && cursor <= word.End {
			start, end = word.Start, word.End
		}
	}
	if len(line)-(end-start)+len(literal) > 65536 {
		return "", 0, errors.New("edited line too long")
	}
	return line[:start] + literal + line[end:], start + len(literal), nil
}
