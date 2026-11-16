// Package introspect derives completion candidates for commands that are not
// covered by the shipped rule pack. It runs a command's own help once, parses
// the subcommands and options it advertises, and caches the result per
// executable version. It never runs a command's normal operation: only help
// flags are attempted.
package introspect

import (
	"strings"
	"time"

	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
)

// Option is one flag or option discovered from help output.
type Option struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	TakesValue  bool   `json:"takesValue,omitempty"`
	// PathValue marks options whose value is likely a filesystem path (for
	// example "--output FILE" or "--target DIR").
	PathValue bool `json:"pathValue,omitempty"`
}

// Node is one command level discovered from help output.
type Node struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Subcommands []*Node  `json:"subcommands,omitempty"`
	Options     []Option `json:"options,omitempty"`
	// Values holds literal positional alternatives such as "[zsh|all]".
	Values []rules.Candidate `json:"values,omitempty"`
	// AcceptsPaths records a path-like positional placeholder such as
	// "[file]" or "<directory>".
	AcceptsPaths bool `json:"acceptsPaths,omitempty"`
}

// Spec is the parsed help for one executable.
type Spec struct {
	Command     string    `json:"command"`
	Path        string    `json:"path"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt"`
	Root        *Node     `json:"root,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// Grammar returns the shell grammar for the discovered command.
func (s *Spec) Grammar() map[string]shellparse.CommandSpec {
	if s == nil || s.Root == nil || s.Command == "" {
		return nil
	}
	global := s.Root.Options
	return map[string]shellparse.CommandSpec{
		s.Command: s.Root.grammar(global, true),
	}
}

func (n *Node) grammar(global []Option, root bool) shellparse.CommandSpec {
	options := n.Options
	if !root {
		options = mergeOptions(global, n.Options)
	}
	spec := shellparse.CommandSpec{
		Options:      make(map[string]bool, len(options)),
		OptionArgs:   make(map[string]int),
		Subcommands:  make(map[string]shellparse.CommandSpec, len(n.Subcommands)),
		Positional:   len(n.Subcommands) == 0 || len(n.Values) > 0 || n.AcceptsPaths,
		EndOfOptions: len(n.Subcommands) == 0,
	}
	for _, option := range options {
		spec.Options[option.Name] = option.TakesValue
		if option.TakesValue {
			spec.OptionArgs[option.Name] = 1
		}
	}
	for _, child := range n.Subcommands {
		spec.Subcommands[child.Name] = child.grammar(global, false)
	}
	return spec
}

// NodeAt returns the command node addressed by a resolved subcommand path.
func (s *Spec) NodeAt(path []string) (*Node, bool) {
	if s == nil || s.Root == nil {
		return nil, false
	}
	node := s.Root
	for _, name := range path {
		var next *Node
		for _, child := range node.Subcommands {
			if child.Name == name {
				next = child
				break
			}
		}
		if next == nil {
			return nil, false
		}
		node = next
	}
	return node, true
}

// Candidates returns candidates that can be derived from a discovered spec.
// Filesystem candidates are intentionally left to the caller.
func (s *Spec) Candidates(ctx shellparse.Context) []rules.Candidate {
	if s == nil || !ctx.Certain {
		return nil
	}
	node, ok := s.NodeAt(ctx.Subcommands)
	if !ok {
		return nil
	}
	switch ctx.Kind {
	case shellparse.Subcommand:
		result := make([]rules.Candidate, 0, len(node.Subcommands))
		for _, child := range node.Subcommands {
			if hasPrefixFold(child.Name, ctx.Prefix) {
				result = append(result, rules.Candidate{Name: child.Name, Description: child.Description})
			}
		}
		return dedupe(result)
	case shellparse.OptionName:
		used := make(map[string]bool, len(ctx.UsedOptions))
		for _, name := range ctx.UsedOptions {
			used[name] = true
		}
		result := make([]rules.Candidate, 0, len(node.Options)+len(s.Root.Options))
		for _, option := range mergeOptions(s.Root.Options, node.Options) {
			if used[option.Name] || !hasPrefixFold(option.Name, ctx.Prefix) {
				continue
			}
			result = append(result, rules.Candidate{Name: option.Name, Description: option.Description})
		}
		return dedupe(result)
	case shellparse.Positional:
		result := make([]rules.Candidate, 0, len(node.Values))
		for _, value := range node.Values {
			if hasPrefixFold(value.Name, ctx.Prefix) {
				result = append(result, value)
			}
		}
		return dedupe(result)
	default:
		return nil
	}
}

// WantsPaths reports whether filesystem candidates should be offered for the
// current context. It is true for path-like positional placeholders and for
// options whose value is declared as a file or directory.
func (s *Spec) WantsPaths(ctx shellparse.Context) bool {
	if s == nil || !ctx.Certain {
		return false
	}
	node, ok := s.NodeAt(ctx.Subcommands)
	if !ok {
		return false
	}
	switch ctx.Kind {
	case shellparse.Positional:
		return len(node.Values) == 0 && node.AcceptsPaths
	case shellparse.OptionValue:
		option, ok := s.optionAt(ctx.Subcommands, ctx.Option)
		return ok && option.PathValue
	default:
		return false
	}
}

func (s *Spec) optionAt(path []string, name string) (Option, bool) {
	node, ok := s.NodeAt(path)
	if !ok {
		return Option{}, false
	}
	for _, option := range mergeOptions(s.Root.Options, node.Options) {
		if option.Name == name {
			return option, true
		}
	}
	return Option{}, false
}

func mergeOptions(groups ...[]Option) []Option {
	result := make([]Option, 0)
	index := make(map[string]int)
	for _, group := range groups {
		for _, option := range group {
			if option.Name == "" {
				continue
			}
			if existing, exists := index[option.Name]; exists {
				// A more specific declaration wins over an inherited one.
				if option.Description != "" {
					result[existing].Description = option.Description
				}
				result[existing].TakesValue = result[existing].TakesValue || option.TakesValue
				result[existing].PathValue = result[existing].PathValue || option.PathValue
				continue
			}
			index[option.Name] = len(result)
			result = append(result, option)
		}
	}
	return result
}

func (n *Node) addOption(option Option) {
	if option.Name == "" {
		return
	}
	for i := range n.Options {
		if n.Options[i].Name != option.Name {
			continue
		}
		if option.Description != "" {
			n.Options[i].Description = option.Description
		}
		n.Options[i].TakesValue = n.Options[i].TakesValue || option.TakesValue
		n.Options[i].PathValue = n.Options[i].PathValue || option.PathValue
		return
	}
	n.Options = append(n.Options, option)
}

func (n *Node) addValues(values []rules.Candidate) {
	for _, value := range values {
		if value.Name == "" {
			continue
		}
		duplicate := false
		for _, existing := range n.Values {
			if existing.Name == value.Name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			n.Values = append(n.Values, value)
		}
	}
}

func dedupe(candidates []rules.Candidate) []rules.Candidate {
	if len(candidates) < 2 {
		return candidates
	}
	seen := make(map[string]bool, len(candidates))
	result := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Name == "" || seen[candidate.Name] {
			continue
		}
		seen[candidate.Name] = true
		result = append(result, candidate)
	}
	return result
}

func hasPrefixFold(name, prefix string) bool {
	return prefix == "" || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
}
