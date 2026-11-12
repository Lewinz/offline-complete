// Package agent connects editing snapshots to static completion and to safe
// text insertion. It does not own a shell, a socket or a user interface, and
// it never executes a completed command.
package agent

import (
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"offline-complete/internal/diag"
	"offline-complete/internal/editstate"
	"offline-complete/internal/generator"
	"offline-complete/internal/history"
	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
)

// Completer resolves a certain command context and returns static candidates.
type Completer struct {
	Pack       *rules.RulePack
	Grammar    map[string]shellparse.CommandSpec
	Ranker     Ranker
	Generators *generator.Runner
	Specs      *rules.SpecStore
	History    *history.Store
	Logger     *diag.Logger
}

// Offer is one candidate set bound to the editing version that produced it.
type Offer struct {
	Version    uint64
	Context    shellparse.Context
	Candidates []rules.Candidate
}

// Complete returns candidates for a snapshot, or false when the context is
// uncertain or no candidate matches. Static candidates are resolved here;
// dynamic candidates are supplied by the configured generator runtime.
func (c *Completer) Complete(snapshot editstate.Snapshot) (Offer, bool) {
	start := time.Now()
	if c == nil || c.Pack == nil || c.Grammar == nil {
		return Offer{}, false
	}
	if len(snapshot.Line) > 64*1024 || !utf8.ValidString(snapshot.Line) ||
		snapshot.Cursor < 0 || snapshot.Cursor > len(snapshot.Line) ||
		(snapshot.Cursor < len(snapshot.Line) && !utf8.RuneStart(snapshot.Line[snapshot.Cursor])) {
		return Offer{}, false
	}
	context := shellparse.Resolve(snapshot.Line, snapshot.Cursor, c.Grammar)
	if !context.Certain {
		return Offer{}, false
	}
	var candidates []rules.Candidate
	if context.Kind == shellparse.Positional || context.Kind == shellparse.OptionValue {
		arg, hasArg := c.Pack.ArgumentAt(context)
		if hasArg {
			for _, suggestion := range arg.Suggestions {
				if strings.HasPrefix(suggestion.Name, context.Prefix) {
					candidates = append(candidates, suggestion)
				}
			}
		}
		if c.Generators != nil && (hasArg || context.Kind == shellparse.Positional) {
			source, tokens := c.dynamicInput(snapshot)
			candidates = append(candidates, c.Generators.CompleteContext(snapshot.CWD, source, context, arg, tokens)...)
		}
	} else if context.Kind == shellparse.Subcommand || context.Kind == shellparse.OptionName {
		if c.Generators != nil {
			source, tokens := c.dynamicInput(snapshot)
			candidates = c.Generators.CompleteContext(snapshot.CWD, source, context, rules.Arg{}, tokens)
		}
		if len(candidates) == 0 {
			candidates = c.Pack.CompleteContext(context)
		}
	} else {
		candidates = c.Pack.CompleteContext(context)
	}
	if context.InlinePrefix == "" && c.History != nil {
		candidates = append(candidates, c.History.Candidates(context.Prefix, 20)...)
	}
	candidates = dedupeCandidates(candidates)
	if context.InlinePrefix != "" {
		for index := range candidates {
			if !strings.HasPrefix(candidates[index].Name, context.InlinePrefix) {
				candidates[index].Name = context.InlinePrefix + candidates[index].Name
			}
			if candidates[index].InsertValue != "" && !strings.HasPrefix(candidates[index].InsertValue, context.InlinePrefix) {
				candidates[index].InsertValue = context.InlinePrefix + candidates[index].InsertValue
			}
		}
	}
	if len(candidates) == 0 {
		if c.Logger != nil && c.Logger.Text() {
			c.Logger.Printf("event=offer_none command=%q subcommands=%q kind=%s prefix_len=%d line_len=%d ms=%.2f",
				context.Command, strings.Join(context.Subcommands, "/"), kindName(context.Kind), len(context.Prefix), len(snapshot.Line), elapsedMS(start))
			c.Logger.Printf("event=offer_text prefix=%q line=%q", truncate(context.Prefix, 200), truncate(snapshot.Line, 500))
		}
		return Offer{}, false
	}
	if c.Ranker != nil {
		candidates = c.Ranker.Rank(context, candidates)
	}
	if c.Logger != nil {
		c.Logger.Printf("event=offer command=%q subcommands=%q kind=%s candidates=%d prefix_len=%d line_len=%d ms=%.2f",
			context.Command, strings.Join(context.Subcommands, "/"), kindName(context.Kind), len(candidates), len(context.Prefix), len(snapshot.Line), elapsedMS(start))
		if c.Logger.Text() {
			c.Logger.Printf("event=offer_text prefix=%q line=%q", truncate(context.Prefix, 200), truncate(snapshot.Line, 500))
		}
	}
	return Offer{Version: snapshot.Version, Context: context, Candidates: candidates}, true
}

func (c *Completer) dynamicInput(snapshot editstate.Snapshot) ([]byte, []string) {
	var source []byte
	if c.Specs != nil {
		if id := c.Pack.Specs[snapshotCommand(snapshot)]; id != "" {
			if loaded, err := c.Specs.Load(id); err == nil {
				source = loaded
			}
		}
	}
	lexed := shellparse.Lex(snapshot.Line, snapshot.Cursor)
	tokens := append([]string(nil), lexed.Words...)
	if lexed.Certain && lexed.Current >= len(tokens) {
		tokens = append(tokens, "")
	}
	return source, tokens
}

func snapshotCommand(snapshot editstate.Snapshot) string {
	lexed := shellparse.Lex(snapshot.Line, snapshot.Cursor)
	if !lexed.Certain || len(lexed.Words) == 0 {
		return ""
	}
	return lexed.Words[0]
}

func dedupeCandidates(candidates []rules.Candidate) []rules.Candidate {
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

func kindName(kind shellparse.ContextKind) string {
	switch kind {
	case shellparse.Subcommand:
		return "subcommand"
	case shellparse.OptionName:
		return "option"
	case shellparse.OptionValue:
		return "option-value"
	case shellparse.Positional:
		return "positional"
	default:
		return "unknown"
	}
}

func elapsedMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

// Apply returns the snapshot that results from replacing the current word with
// candidate. The version is preserved; callers must only write the result after
// verifying that the version, session and focus are still current.
func Apply(snapshot editstate.Snapshot, candidate string) (editstate.Snapshot, error) {
	line, cursor, err := shellparse.ReplaceWord(snapshot.Line, snapshot.Cursor, candidate)
	if err != nil {
		return editstate.Snapshot{}, err
	}
	return editstate.Snapshot{Version: snapshot.Version, Line: line, Cursor: cursor}, nil
}

// WriteLineReplacement rewrites the caller's editing line through the PTY.
// It assumes emacs key bindings: Ctrl-E moves to the end and Ctrl-U kills the
// whole line when issued there. The line must contain no control characters,
// so a replacement cannot execute a command or inject a newline. The caller
// must still verify explicit selection, session, focus and version first; this
// function only writes bytes.
func WriteLineReplacement(w io.Writer, line string, cursor int) error {
	if w == nil {
		return errors.New("nil writer")
	}
	if len(line) > 64*1024 || !utf8.ValidString(line) || cursor < 0 || cursor > len(line) ||
		(cursor < len(line) && !utf8.RuneStart(line[cursor])) {
		return errors.New("invalid editing line")
	}
	for _, r := range line {
		if r < 0x20 || r == 0x7f {
			return errors.New("control character in editing line")
		}
	}

	var output strings.Builder
	output.Grow(len(line) + 8)
	output.WriteString("\x05\x15") // end of line, then backward kill
	output.WriteString(line)
	left := utf8.RuneCountInString(line[cursor:])
	for i := 0; i < left; i++ {
		output.WriteString("\x1b[D")
	}
	_, err := io.WriteString(w, output.String())
	return err
}
