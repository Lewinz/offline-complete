// Package strictjson contains validation shared by local JSON protocols.
package strictjson

import (
	"errors"
	"strconv"
)

// ValidateSurrogates rejects unpaired UTF-16 escapes in syntactically valid
// JSON. Callers must check json.Valid first; malformed input is not supported.
func ValidateSurrogates(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		value, _ := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return errors.New("unpaired low surrogate in JSON")
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return errors.New("unpaired high surrogate in JSON")
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return errors.New("unpaired high surrogate in JSON")
			}
			i += 6
		}
	}
	return nil
}
