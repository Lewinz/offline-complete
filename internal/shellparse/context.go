package shellparse

import "strings"

// CommandSpec is an explicitly supplied grammar, not an inference from a rule
// file. Option values indicate whether the option consumes one following word.
// Positional permits an arbitrary number of positional words at this level.
// Callers must establish that shell aliases, functions and wrappers do not
// change the command's meaning before using a result for completion.
type CommandSpec struct {
	Options map[string]bool
	// OptionArgs maps an option to its declared argument count. Options with
	// one argument may omit it; values greater than one continue consuming
	// until the option's arguments are exhausted.
	OptionArgs  map[string]int
	Subcommands map[string]CommandSpec
	Positional  bool
	// EndOfOptions explicitly declares that -- ends option parsing at this level.
	EndOfOptions bool
}

type ContextKind uint8

const (
	Subcommand ContextKind = iota + 1
	OptionName
	OptionValue
	Positional
)

// Context contains no prior argument values. UsedOptions contains only options
// at the current command level. Slot is its zero-based positional slot. An
// uncertain result contains no context.
type Context struct {
	Certain     bool
	Command     string
	Subcommands []string
	UsedOptions []string
	Slot        int
	// OptionSlot is the zero-based argument slot for OptionValue. Most CLI
	// options consume one value, so it is normally zero.
	OptionSlot int
	Prefix     string
	// InlinePrefix is the "--option=" part when completion is inside an
	// inline option value. Candidates must be inserted after it.
	InlinePrefix string
	Kind         ContextKind
	Option       string
}

// Resolve analyzes the literal subset accepted by Lex against a complete,
// trusted grammar. It rejects ambiguous positions, unknown prior options and
// words after the cursor's word rather than guessing their meaning.
func Resolve(line string, cursor int, grammar map[string]CommandSpec) Context {
	lexed := Lex(line, cursor)
	if !lexed.Certain || lexed.Current == 0 || lexed.Current < len(lexed.Words)-1 {
		return Context{}
	}
	// A cursor in whitespace denotes an insertion point, not the next word.
	// Do not silently treat that later word as the word being edited.
	if cursor < len(line) && (line[cursor] == ' ' || line[cursor] == '\t') && strings.Trim(line[cursor:], " \t") != "" {
		return Context{}
	}
	spec, ok := grammar[lexed.Words[0]]
	if !ok {
		return Context{}
	}
	result := Context{Command: lexed.Words[0], Prefix: lexed.Prefix}
	var pending string
	pendingSlot := 0
	pendingRemaining := 0
	endedOptions := false
	for _, word := range lexed.Words[1:lexed.Current] {
		if endedOptions {
			result.Slot++
			continue
		}
		if pending != "" {
			if strings.HasPrefix(word, "-") {
				return Context{}
			}
			pendingSlot++
			pendingRemaining--
			if pendingRemaining <= 0 {
				pending = ""
			}
			continue
		}
		// Only a grammar explicitly describing a positional-only command level
		// establishes the meaning of all words following the terminator.
		if word == "--" {
			if !spec.EndOfOptions || !spec.Positional || len(spec.Subcommands) != 0 {
				return Context{}
			}
			endedOptions = true
			continue
		}
		if name, value, inline := splitInlineOption(word); inline {
			takesValue, exists := spec.Options[name]
			if !exists || !takesValue || value == "" {
				return Context{}
			}
			result.UsedOptions = append(result.UsedOptions, name)
			continue
		}
		if strings.HasPrefix(word, "-") {
			takesValue, exists := spec.Options[word]
			if !exists {
				return Context{}
			}
			result.UsedOptions = append(result.UsedOptions, word)
			if takesValue {
				pending = word
				pendingSlot = 0
				pendingRemaining = 1
				if count, ok := spec.OptionArgs[word]; ok && count > 1 {
					pendingRemaining = count
				}
			}
			continue
		}
		child, isChild := spec.Subcommands[word]
		// A declared subcommand wins at the start of a command level even when
		// that level also accepts positional arguments.
		if isChild && result.Slot == 0 {
			result.Subcommands = append(result.Subcommands, word)
			result.UsedOptions = nil
			spec = child
			continue
		}
		if !spec.Positional || isChild {
			return Context{}
		}
		result.Slot++
	}
	// Without a modeled terminator, an empty prefix can hide an option in the
	// rest of the current word. After a terminator its role is positional.
	if !endedOptions && lexed.Prefix == "" && lexed.Current < len(lexed.Words) && strings.HasPrefix(lexed.Words[lexed.Current], "-") {
		return Context{}
	}
	if endedOptions {
		result.Kind = Positional
	} else if pending != "" {
		if strings.HasPrefix(result.Prefix, "-") {
			return Context{}
		}
		result.Kind, result.Option = OptionValue, pending
		result.OptionSlot = pendingSlot
	} else if name, value, inline := splitInlineOption(result.Prefix); inline {
		takesValue, exists := spec.Options[name]
		if !exists || !takesValue {
			return Context{}
		}
		result.Kind = OptionValue
		result.Option = name
		result.Prefix = value
		result.InlinePrefix = name + "="
	} else if strings.HasPrefix(result.Prefix, "-") {
		result.Kind = OptionName
	} else if len(spec.Subcommands) > 0 && result.Slot == 0 && !strings.HasPrefix(result.Prefix, "-") {
		result.Kind = Subcommand
	} else if spec.Positional {
		result.Kind = Positional
	} else {
		return Context{}
	}
	result.Certain = true
	return result
}

func splitInlineOption(word string) (name, value string, ok bool) {
	if len(word) < 3 || word[0] != '-' {
		return "", "", false
	}
	index := strings.IndexByte(word, '=')
	if index <= 1 || index == len(word)-1 {
		// A trailing '=' is a valid empty prefix for completion.
		if index <= 1 {
			return "", "", false
		}
	}
	if index < 0 {
		return "", "", false
	}
	name = word[:index]
	if name == "--" || strings.HasPrefix(name, "=") {
		return "", "", false
	}
	return name, word[index+1:], true
}
