package introspect

import (
	"regexp"
	"strings"

	"offline-complete/internal/rules"
)

type helpSection uint8

const (
	sectionUnknown helpSection = iota
	sectionCommands
	sectionOptions
	sectionOther
)

var (
	ansiPattern         = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	headingPattern      = regexp.MustCompile(`(?i)^(?:(?:the|available|additional|global|common|management|main|sub)\s+)*(commands|subcommands|options|flags)(?:\s+are)?\s*:?\s*$`)
	otherHeadingPattern = regexp.MustCompile(`(?i)^(?:usage|examples?|description|aliases|notes?|see also|learn more|environment|files|exit status|diagnostics|author|reporting bugs|history|configuration|arguments)\s*:?\s*$`)
	separatorPattern    = regexp.MustCompile(`\s{2,}|\t+`)
	pathTokenPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	optionNamePattern   = regexp.MustCompile(`^-{1,2}[A-Za-z0-9][A-Za-z0-9_-]*$`)
	bracketPattern      = regexp.MustCompile(`\[[^\]]*\]|<[^>]*>`)
	typePattern         = regexp.MustCompile(`(?i)^(?:string|strings|stringarray|stringslice|int|int8|int16|int32|int64|uint|uint8|uint16|uint32|uint64|float|float32|float64|bool|duration|time|path|file|dir|directory|name|value|level|count|number|size|seconds|milliseconds|bytes|port|address|url|uri|token|regexp|pattern)$`)
	pathHintPattern     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:file|files|path|paths|dir|dirs|directory|directories|folder|folders)(?:$|[^a-z0-9])`)
)

// ParseHelp converts command help output into a completion spec. It accepts
// several common layouts (Cobra, clap, urfave/cli, go's command list and plain
// indented tables) and deliberately ignores prose outside command sections.
func ParseHelp(command string, output []byte) *Spec {
	spec := &Spec{Command: command, Root: &Node{Name: command}}
	section := sectionUnknown
	text := ansiPattern.ReplaceAllString(string(output), "")
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if match := headingPattern.FindStringSubmatch(trimmed); match != nil {
			switch strings.ToLower(match[1]) {
			case "commands", "subcommands":
				section = sectionCommands
			case "options", "flags":
				section = sectionOptions
			}
			continue
		}
		if otherHeadingPattern.MatchString(trimmed) {
			section = sectionOther
			continue
		}
		switch section {
		case sectionCommands:
			parseCommandLine(spec, line, true)
		case sectionOptions:
			parseOptionLine(spec.Root, line)
		case sectionOther:
			// Usage blocks, examples and prose are not command tables.
		default:
			if strings.HasPrefix(trimmed, "-") {
				parseOptionLine(spec.Root, line)
				continue
			}
			parseCommandLine(spec, line, false)
		}
	}
	return spec
}

func parseCommandLine(spec *Spec, line string, inSection bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "-") {
		return
	}
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	if !inSection && indent == 0 {
		return
	}
	usage, desc, ok := splitEntry(trimmed)
	if !ok {
		return
	}
	fields := strings.Fields(usage)
	if len(fields) == 0 {
		return
	}
	name := strings.Trim(fields[0], ",;:")
	if !inSection && isStopWord(name) {
		return
	}
	paths, options, values, acceptsPaths := parseCommandUsage(usage)
	if len(paths) == 0 {
		return
	}
	for _, path := range paths {
		spec.addPath(path, desc, options, values, acceptsPaths)
	}
}

func splitEntry(trimmed string) (usage, desc string, ok bool) {
	if location := separatorPattern.FindStringIndex(trimmed); location != nil {
		usage = strings.TrimSpace(trimmed[:location[0]])
		desc = strings.TrimSpace(trimmed[location[1]:])
		return usage, desc, usage != ""
	}
	if index := strings.Index(trimmed, " - "); index > 0 {
		return strings.TrimSpace(trimmed[:index]), strings.TrimSpace(trimmed[index+3:]), true
	}
	if strings.ContainsAny(trimmed, " \t") {
		return "", "", false
	}
	return trimmed, "", true
}

func parseCommandUsage(usage string) (paths [][]string, options []Option, values []rules.Candidate, acceptsPaths bool) {
	groups := bracketPattern.FindAllString(usage, -1)
	remaining := bracketPattern.ReplaceAllString(usage, " ")
	// In command usage, commas denote aliases: "build, b" means both names.
	remaining = strings.ReplaceAll(remaining, ",", "|")
	fields := strings.Fields(remaining)
	var segments [][]string
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if strings.HasPrefix(field, "-") {
			names := optionNames(field)
			takesValue := strings.Contains(field, "=")
			pathValue := pathHintPattern.MatchString(valueAfterEquals(field))
			if !takesValue && index+1 < len(fields) && !strings.HasPrefix(fields[index+1], "-") &&
				(looksLikeType(fields[index+1]) || isPlaceholder(fields[index+1])) {
				takesValue = true
				pathValue = pathHintPattern.MatchString(fields[index+1])
			}
			for _, name := range names {
				options = append(options, Option{Name: name, TakesValue: takesValue, PathValue: pathValue})
			}
			continue
		}
		if !validSegment(field) {
			break
		}
		segments = append(segments, splitAlternatives(field))
	}
	for _, group := range groups {
		groupOptions, groupValues, groupPaths := parseGroup(group)
		options = append(options, groupOptions...)
		values = append(values, groupValues...)
		acceptsPaths = acceptsPaths || groupPaths
	}
	paths = buildPaths(segments, 64)
	return paths, options, values, acceptsPaths
}

func parseGroup(group string) (options []Option, values []rules.Candidate, acceptsPaths bool) {
	inner := strings.TrimSpace(strings.Trim(group, "[]<>()"))
	if inner == "" {
		return nil, nil, false
	}
	if !strings.HasPrefix(inner, "-") && pathHintPattern.MatchString(inner) {
		acceptsPaths = true
	}
	fields := strings.Fields(inner)
	if strings.HasPrefix(inner, "-") {
		valueToken := ""
		for _, field := range fields {
			if !strings.HasPrefix(field, "-") {
				valueToken = field
				break
			}
		}
		for _, field := range fields {
			for _, name := range optionNames(field) {
				option := Option{Name: name}
				if strings.Contains(field, "=") {
					option.TakesValue = true
					option.PathValue = pathHintPattern.MatchString(valueAfterEquals(field))
				}
				if valueToken != "" && (looksLikeType(valueToken) || isPlaceholder(valueToken) || pathHintPattern.MatchString(valueToken)) {
					option.TakesValue = true
					if pathHintPattern.MatchString(valueToken) {
						option.PathValue = true
					}
				}
				options = append(options, option)
			}
		}
		return options, nil, acceptsPaths
	}
	if strings.Contains(inner, "|") {
		parts := splitAlternatives(inner)
		if len(parts) > 1 && allPathTokens(parts) {
			for _, part := range parts {
				values = append(values, rules.Candidate{Name: part})
			}
		}
	}
	// A single token such as "[service]" is a placeholder, not a literal
	// value. It only affects whether file candidates are useful.
	return options, values, acceptsPaths
}

func parseOptionLine(root *Node, line string) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "-") {
		return
	}
	usage, desc, ok := splitEntry(trimmed)
	if !ok {
		return
	}
	fields := strings.Fields(usage)
	if len(fields) == 0 {
		return
	}
	valueToken := ""
	for _, field := range fields {
		if !strings.HasPrefix(field, "-") {
			valueToken = field
			break
		}
	}
	for _, field := range fields {
		for _, name := range optionNames(field) {
			option := Option{Name: name, Description: desc}
			if strings.Contains(field, "=") {
				option.TakesValue = true
				option.PathValue = pathHintPattern.MatchString(valueAfterEquals(field))
			}
			if valueToken != "" && (looksLikeType(valueToken) || isPlaceholder(valueToken) || pathHintPattern.MatchString(valueToken)) {
				option.TakesValue = true
				if pathHintPattern.MatchString(valueToken) {
					option.PathValue = true
				}
			}
			root.addOption(option)
		}
	}
}

func (s *Spec) addPath(path []string, desc string, options []Option, values []rules.Candidate, acceptsPaths bool) {
	if s.Root == nil {
		s.Root = &Node{Name: s.Command}
	}
	if len(path) > 0 && path[0] == s.Command {
		path = path[1:]
	}
	if len(path) == 0 {
		return
	}
	node := s.Root
	for _, name := range path {
		if name == "" {
			continue
		}
		var child *Node
		for _, candidate := range node.Subcommands {
			if candidate.Name == name {
				child = candidate
				break
			}
		}
		if child == nil {
			child = &Node{Name: name}
			node.Subcommands = append(node.Subcommands, child)
		}
		node = child
	}
	if desc != "" {
		node.Description = desc
	}
	for _, option := range options {
		node.addOption(option)
	}
	node.addValues(values)
	if acceptsPaths {
		node.AcceptsPaths = true
	}
}

func optionNames(field string) []string {
	field = strings.TrimSpace(field)
	field = strings.TrimRight(field, ",;:")
	if index := strings.IndexAny(field, "=[< "); index > 0 {
		field = field[:index]
	}
	parts := strings.FieldsFunc(field, func(r rune) bool { return r == ',' || r == '|' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimRight(part, ",;:")
		if optionNamePattern.MatchString(part) {
			result = append(result, part)
		}
	}
	return result
}

func valueAfterEquals(field string) string {
	if index := strings.IndexByte(field, '='); index >= 0 {
		return strings.Trim(field[index+1:], "[]<>() ")
	}
	return ""
}

func splitAlternatives(field string) []string {
	parts := strings.FieldsFunc(field, func(r rune) bool { return r == '|' || r == ',' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "[]<>()")
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func buildPaths(segments [][]string, limit int) [][]string {
	if len(segments) == 0 {
		return nil
	}
	paths := [][]string{{}}
	for _, alternatives := range segments {
		if len(alternatives) == 0 {
			return nil
		}
		next := make([][]string, 0, len(paths)*len(alternatives))
		for _, prefix := range paths {
			for _, alternative := range alternatives {
				path := append(append([]string{}, prefix...), alternative)
				next = append(next, path)
				if limit > 0 && len(next) >= limit {
					break
				}
			}
			if limit > 0 && len(next) >= limit {
				break
			}
		}
		paths = next
		if len(paths) == 0 {
			return nil
		}
	}
	return paths
}

func validSegment(field string) bool {
	parts := splitAlternatives(field)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !pathTokenPattern.MatchString(part) {
			return false
		}
	}
	return true
}

func allPathTokens(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !pathTokenPattern.MatchString(part) {
			return false
		}
	}
	return true
}

func looksLikeType(value string) bool {
	value = strings.Trim(value, "[]<>() ")
	return typePattern.MatchString(value) || isPlaceholder(value) || strings.HasSuffix(value, "...")
}

func isPlaceholder(value string) bool {
	value = strings.Trim(value, "[]<>() ")
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' {
			return false
		}
	}
	return strings.ContainsAny(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

func isStopWord(name string) bool {
	switch strings.ToLower(name) {
	case "use", "see", "note", "notes", "the", "this", "that", "these", "those",
		"you", "your", "for", "and", "with", "from", "when", "where", "all", "any",
		"example", "examples", "usage", "arguments", "options", "flags", "commands",
		"subcommands", "description", "default", "defaults":
		return true
	}
	return false
}
