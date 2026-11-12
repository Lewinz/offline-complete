// Package rules loads the versioned, declarative rule packs that ship with the
// application. It never executes rule code and never contacts the network.
package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"offline-complete/internal/strictjson"
)

// RulePack is a versioned, read-only set of static completion rules.
type RulePack struct {
	SchemaVersion int
	Commands      []Command
	// Specs maps a top-level command to the opaque identifier of its upstream
	// spec module. Dynamic generators use it to load the original Kiro/Fig
	// JavaScript without storing spec source in this static pack.
	Specs  map[string]string
	Source Source
}

// Command describes a command, its options and its nested subcommands.
type Command struct {
	Name        string
	Description string
	Options     []Option
	Subcommands []Command
	Args        []Arg
	// Dynamic marks a node whose subcommands, options or arguments are
	// completed from loadSpec/generateSpec at runtime.
	Dynamic bool `json:"dynamic,omitempty"`
}

// Option is a flag or an option that consumes one or more argument values.
type Option struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Args        []Arg  `json:"args,omitempty"`
}

// Candidate is a single completable name with an optional description.
type Candidate struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InsertValue string `json:"insertValue,omitempty"`
}

// Arg describes one positional argument and its dynamic value sources.
type Arg struct {
	Name           string      `json:"name,omitempty"`
	Description    string      `json:"description,omitempty"`
	Generators     []Generator `json:"generators,omitempty"`
	Suggestions    []Candidate `json:"suggestions,omitempty"`
	IsVariadic     bool        `json:"isVariadic,omitempty"`
	IsOptional     bool        `json:"isOptional,omitempty"`
	FilterStrategy string      `json:"filterStrategy,omitempty"`
}

// Generator is a bounded, declarative dynamic value source. Scripts are run
// without a shell; templates are limited to filesystem listings.
type Generator struct {
	Script   []string `json:"script,omitempty"`
	Template []string `json:"template,omitempty"`
}

type rulePackJSON struct {
	SchemaVersion *int              `json:"schemaVersion"`
	Commands      *[]Command        `json:"commands"`
	Specs         map[string]string `json:"specs,omitempty"`
}

// maxRuleBytes bounds both direct parsing and filesystem-based rule loading.
const maxRuleBytes = 16 << 20

// FromJSON parses one complete JSON document. Unknown fields, missing required
// fields, trailing data and unsupported schema versions are rejected.
func FromJSON(data []byte) (*RulePack, error) {
	if len(data) > maxRuleBytes {
		return nil, errors.New("rule data exceeds size limit")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("rule data is not valid UTF-8")
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid rule JSON")
	}
	if err := strictjson.ValidateSurrogates(data); err != nil {
		return nil, err
	}
	if err := validateFields(json.NewDecoder(bytes.NewReader(data)), "pack", 0); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var raw rulePackJSON
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	if raw.SchemaVersion == nil {
		return nil, errors.New("missing schemaVersion")
	}
	if raw.Commands == nil {
		return nil, errors.New("missing commands")
	}
	if *raw.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported rule schema version: %d", *raw.SchemaVersion)
	}
	if err := validateCommands(*raw.Commands); err != nil {
		return nil, err
	}
	for command, spec := range raw.Specs {
		if strings.TrimSpace(command) == "" || strings.TrimSpace(spec) == "" ||
			strings.IndexFunc(command, unicode.IsControl) >= 0 ||
			strings.IndexFunc(spec, unicode.IsControl) >= 0 ||
			strings.ContainsAny(spec, `/\`) || spec == "." || spec == ".." {
			return nil, fmt.Errorf("invalid spec mapping for %q", command)
		}
	}
	return &RulePack{SchemaVersion: *raw.SchemaVersion, Commands: *raw.Commands, Specs: raw.Specs}, nil
}

func validateCommands(commands []Command) error {
	seen := make(map[string]bool, len(commands))
	for _, command := range commands {
		if strings.TrimSpace(command.Name) == "" || strings.IndexFunc(command.Name, unicode.IsControl) >= 0 || seen[command.Name] {
			return fmt.Errorf("invalid or duplicate command name: %q", command.Name)
		}
		seen[command.Name] = true
		options := make(map[string]bool, len(command.Options))
		for _, option := range command.Options {
			if strings.TrimSpace(option.Name) == "" || strings.IndexFunc(option.Name, unicode.IsControl) >= 0 || options[option.Name] {
				return fmt.Errorf("invalid or duplicate option name: %q", option.Name)
			}
			options[option.Name] = true
			if err := validateArgs(option.Args); err != nil {
				return err
			}
		}
		if err := validateArgs(command.Args); err != nil {
			return err
		}
		if err := validateCommands(command.Subcommands); err != nil {
			return err
		}
	}
	return nil
}

func validateArgs(args []Arg) error {
	for _, arg := range args {
		if strings.IndexFunc(arg.Name, unicode.IsControl) >= 0 {
			return errors.New("invalid argument name")
		}
		for _, generator := range arg.Generators {
			if len(generator.Script) == 0 && len(generator.Template) == 0 {
				return errors.New("argument generator has no source")
			}
			for _, value := range append(append([]string{}, generator.Script...), generator.Template...) {
				if value == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 {
					return errors.New("invalid argument generator value")
				}
			}
		}
		suggestions := make(map[string]bool, len(arg.Suggestions))
		for _, suggestion := range arg.Suggestions {
			if strings.TrimSpace(suggestion.Name) == "" || strings.IndexFunc(suggestion.Name, unicode.IsControl) >= 0 {
				return fmt.Errorf("invalid argument suggestion: %q", suggestion.Name)
			}
			// Upstream specs occasionally repeat a suggestion. Keep the first
			// occurrence rather than rejecting the whole pack.
			suggestions[suggestion.Name] = true
		}
	}
	return nil
}

// Limit container nesting before decoding into recursive Go structures.
const maxRuleNesting = 128

// validateFields walks valid JSON, rejecting duplicate and unknown field names.
// Schema names are case-sensitive even though encoding/json matches them loosely.
func validateFields(decoder *json.Decoder, kind string, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("null is not a valid rule value")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= maxRuleNesting {
		return errors.New("rule JSON nesting exceeds limit")
	}
	switch delim {
	case '{':
		keys := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key := keyToken.(string) // JSON syntax has already been validated.
			folded := foldFieldName(key)
			if keys[folded] {
				return fmt.Errorf("duplicate rule field: %q", key)
			}
			keys[folded] = true
			if kind == "specs" {
				if err := validateFields(decoder, "scalar", depth+1); err != nil {
					return err
				}
				continue
			}
			next, ok := ruleFieldKind(kind, key)
			if !ok {
				return fmt.Errorf("unknown rule field: %q", key)
			}
			if err := validateFields(decoder, next, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateFields(decoder, kind, depth+1); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}

func ruleFieldKind(kind, key string) (string, bool) {
	switch kind {
	case "pack":
		switch key {
		case "schemaVersion":
			return "scalar", true
		case "commands":
			return "command", true
		case "specs":
			return "specs", true
		}
	case "command":
		switch key {
		case "name", "description", "dynamic":
			return "scalar", true
		case "options":
			return "option", true
		case "subcommands":
			return "command", true
		case "args":
			return "arg", true
		}
	case "option":
		switch key {
		case "name", "description":
			return "scalar", true
		case "args":
			return "arg", true
		}
	case "arg":
		switch key {
		case "name", "description", "isVariadic", "isOptional", "filterStrategy":
			return "scalar", true
		case "generators":
			return "generator", true
		case "suggestions":
			return "option", true
		}
	case "generator":
		switch key {
		case "script", "template":
			return "scalar", true
		}
	}
	return "", false
}

// JSON struct field matching is case-insensitive, including Unicode simple folds.
func foldFieldName(name string) string {
	var result strings.Builder
	for _, r := range name {
		min := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < min {
				min = next
			}
		}
		result.WriteRune(min)
	}
	return result.String()
}

func ensureEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON data")
		}
		return err
	}
	return nil
}

// Complete returns subcommands and options for the given command context that
// start with prefix. An unknown command or subcommand yields no candidates.
func (p *RulePack) Complete(context []string, prefix string) []Candidate {
	commands := p.Commands
	var current *Command
	for _, name := range context {
		var found *Command
		for i := range commands {
			if commands[i].Name == name {
				found = &commands[i]
				break
			}
		}
		if found == nil {
			return nil
		}
		current = found
		commands = found.Subcommands
	}

	candidates := make([]Candidate, 0, len(commands))
	for _, command := range commands {
		candidates = append(candidates, Candidate{
			Name:        command.Name,
			Description: command.Description,
		})
	}
	if current != nil {
		for _, option := range current.Options {
			candidates = append(candidates, Candidate{Name: option.Name, Description: option.Description})
		}
	}

	result := candidates[:0]
	for _, candidate := range candidates {
		if len(candidate.Name) >= len(prefix) && candidate.Name[:len(prefix)] == prefix {
			result = append(result, candidate)
		}
	}
	return result
}
