package shellparse

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ZshLiteral encodes a complete literal word, not a fragment for insertion
// into an existing quote. The caller must independently validate the edited
// word's boundaries, session, focus and version before replacing anything.
func ZshLiteral(value string) (string, error) {
	if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 || len(value) > 65536 {
		return "", errors.New("unsafe literal")
	}
	quoted := "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	if len(quoted) > 65536 {
		return "", errors.New("literal too long")
	}
	return quoted, nil
}

// ZshWordLiteral returns value as a bare word when it has no shell meaning,
// and otherwise returns ZshLiteral. Bare words keep completion results
// readable (for example --message instead of '--message'); the conservative
// fallback still quotes whitespace, quotes, expansions and glob characters.
func ZshWordLiteral(value string) (string, error) {
	if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 || len(value) > 65536 {
		return "", errors.New("unsafe literal")
	}
	if isBareWord(value) {
		return value, nil
	}
	return ZshLiteral(value)
}

func isBareWord(value string) bool {
	if value == "" {
		return false
	}
	switch value[0] {
	case '~', '=', '%', '#':
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_./:@+,-", r):
		default:
			return false
		}
	}
	return true
}
