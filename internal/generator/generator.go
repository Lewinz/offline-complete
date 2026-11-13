// Package generator executes completion-time value generators. The full
// path evaluates Kiro/Fig spec modules in a Node process, which supports
// custom JavaScript, postProcess, templates and script generators. The Go
// fallback keeps plain script/template generation working when Node is absent.
package generator

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
)

// Runner resolves dynamic values. The zero value is ready to use.
type Runner struct {
	Timeout       time.Duration
	MaxCandidates int
	MaxOutput     int
	// Node and Specs enable the full Kiro/Fig generator runtime. When either
	// is nil, Complete falls back to the declarative script/template subset.
	Node  *NodeRunner
	Specs *rules.SpecStore
}

// CompleteContext runs the generators attached to one argument. It prefers the
// Node spec runtime and falls back to the Go implementation for simple
// script/template generators.
func (r Runner) CompleteContext(cwd string, source []byte, context shellparse.Context, arg rules.Arg, tokens []string) []rules.Candidate {
	if r.Node != nil && len(source) > 0 {
		request := NodeRequest{
			Source:         string(source),
			Command:        context.Command,
			Subcommands:    append([]string(nil), context.Subcommands...),
			Prefix:         context.Prefix,
			CWD:            cwd,
			Tokens:         tokens,
			Slot:           context.Slot,
			Option:         context.Option,
			OptionSlot:     context.OptionSlot,
			UsedOptions:    append([]string(nil), context.UsedOptions...),
			FilterStrategy: arg.FilterStrategy,
		}
		switch context.Kind {
		case shellparse.OptionValue:
			request.Kind = "option-value"
		case shellparse.Subcommand:
			request.Kind = "subcommand"
		case shellparse.OptionName:
			request.Kind = "option"
		default:
			request.Kind = "positional"
		}
		if candidates, err := r.Node.Complete(request); err == nil {
			return filterCandidates(candidates, context.Prefix, r.candidateLimit(), arg.FilterStrategy == "fuzzy")
		}
	}
	if context.Kind == shellparse.Subcommand || context.Kind == shellparse.OptionName {
		return nil
	}
	return filterCandidates(r.Complete(cwd, arg.Generators, ""), context.Prefix, r.candidateLimit(), arg.FilterStrategy == "fuzzy")
}

func (r Runner) candidateLimit() int {
	if r.MaxCandidates > 0 {
		return r.MaxCandidates
	}
	return 100
}

func filterCandidates(candidates []rules.Candidate, prefix string, limit int, fuzzy bool) []rules.Candidate {
	seen := make(map[string]bool)
	result := make([]rules.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Name == "" || seen[candidate.Name] || !matches(candidate.Name, prefix, fuzzy) {
			continue
		}
		seen[candidate.Name] = true
		result = append(result, candidate)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func hasPrefixFold(name, prefix string) bool {
	return prefix == "" || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
}

func matches(name, prefix string, fuzzy bool) bool {
	if hasPrefixFold(name, prefix) {
		return true
	}
	if !fuzzy {
		return false
	}
	query := []rune(strings.ToLower(prefix))
	value := []rune(strings.ToLower(name))
	index := 0
	for _, r := range value {
		if index < len(query) && r == query[index] {
			index++
		}
	}
	return index == len(query)
}

// Complete runs the configured generators in cwd and returns matching
// candidates. Failures are local to one generator and never fail completion.
func (r Runner) Complete(cwd string, generators []rules.Generator, prefix string) []rules.Candidate {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 700 * time.Millisecond
	}
	maxCandidates := r.MaxCandidates
	if maxCandidates <= 0 {
		maxCandidates = 100
	}
	maxOutput := r.MaxOutput
	if maxOutput <= 0 {
		maxOutput = 64 * 1024
	}
	seen := make(map[string]bool)
	var result []rules.Candidate
	add := func(candidate rules.Candidate) {
		if len(result) >= maxCandidates || candidate.Name == "" || !hasPrefixFold(candidate.Name, prefix) || seen[candidate.Name] {
			return
		}
		seen[candidate.Name] = true
		result = append(result, candidate)
	}
	for _, generator := range generators {
		if len(generator.Script) > 0 {
			for _, candidate := range runScript(cwd, generator.Script, timeout, maxOutput) {
				add(candidate)
			}
		}
		for _, template := range generator.Template {
			for _, candidate := range listTemplate(cwd, template, prefix, maxCandidates) {
				add(candidate)
			}
		}
	}
	return result
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.Buffer.Write(p[:remaining])
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func runScript(cwd string, script []string, timeout time.Duration, maxOutput int) []rules.Candidate {
	if len(script) == 0 || script[0] == "" {
		return nil
	}
	dir := cwd
	if dir == "" {
		dir = "."
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, script[0], script[1:]...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output := &limitedBuffer{limit: maxOutput}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		// A non-zero exit can still include useful partial output; only a
		// timeout or spawn failure discards it.
		if output.Len() == 0 {
			return nil
		}
	}
	var result []rules.Candidate
	for _, line := range strings.Split(output.String(), "\n") {
		value := strings.TrimSpace(line)
		value = strings.TrimPrefix(value, "* ")
		value = strings.TrimSpace(value)
		if value == "" || strings.HasPrefix(value, "HEAD ") {
			continue
		}
		result = append(result, rules.Candidate{Name: value})
	}
	return result
}

func listTemplate(cwd, template, prefix string, limit int) []rules.Candidate {
	if template != "filepaths" && template != "folders" {
		return nil
	}
	dirPart, query := filepath.Split(prefix)
	dir := cwd
	if dir == "" {
		dir = "."
	}
	if dirPart != "" {
		if filepath.IsAbs(dirPart) {
			dir = dirPart
		} else {
			dir = filepath.Join(dir, dirPart)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := strings.ToLower(entries[i].Name()), strings.ToLower(entries[j].Name())
		if left == right {
			return entries[i].Name() < entries[j].Name()
		}
		return left < right
	})
	var result []rules.Candidate
	for _, entry := range entries {
		if len(result) >= limit {
			break
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(query, ".") {
			continue
		}
		if !hasPrefixFold(name, query) {
			continue
		}
		isDir := entry.IsDir()
		if template == "folders" && !isDir {
			continue
		}
		candidate := dirPart + name
		description := "文件"
		if isDir {
			candidate += "/"
			description = "目录"
		}
		fullPath := filepath.Join(dir, name)
		if absolute, err := filepath.Abs(fullPath); err == nil {
			fullPath = absolute
		}
		result = append(result, rules.Candidate{Name: candidate, Description: description, Path: fullPath})
	}
	return result
}
