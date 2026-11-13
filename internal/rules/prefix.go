package rules

import "strings"

func hasPrefixFold(name, prefix string) bool {
	return prefix == "" || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
}
