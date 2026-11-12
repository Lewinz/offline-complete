package shellparse

import (
	"errors"
	"unicode/utf8"
)

// ZshCursorToByte converts a character index to a UTF-8 byte offset. It is
// only applicable when zsh uses multibyte character indexing.
func ZshCursorToByte(line string, cursor int) (int, error) {
	if cursor < 0 || !utf8.ValidString(line) {
		return 0, errors.New("invalid character cursor or UTF-8")
	}
	for offset := range line {
		if cursor == 0 {
			return offset, nil
		}
		cursor--
	}
	if cursor == 0 {
		return len(line), nil
	}
	return 0, errors.New("character cursor outside line")
}

// ByteCursorToZsh converts a UTF-8 byte offset to a character index. The
// offset must be at a character boundary; zsh must use multibyte indexing.
func ByteCursorToZsh(line string, cursor int) (int, error) {
	if !utf8.ValidString(line) || cursor < 0 || cursor > len(line) || !utf8.ValidString(line[:cursor]) {
		return 0, errors.New("invalid byte cursor or UTF-8")
	}
	return utf8.RuneCountInString(line[:cursor]), nil
}
