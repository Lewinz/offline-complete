package shellparse

import (
	"reflect"
	"testing"
)

func TestResolveContext(t *testing.T) {
	grammar := map[string]CommandSpec{
		"tool": {Options: map[string]bool{"--verbose": false, "--output": true}, Subcommands: map[string]CommandSpec{
			"build": {Options: map[string]bool{"--target": true}, Positional: true},
		}},
	}
	tests := []struct {
		line   string
		cursor int
		want   Context
	}{
		{"tool build --target arm file", 28, Context{Certain: true, Command: "tool", Subcommands: []string{"build"}, UsedOptions: []string{"--target"}, Slot: 0, Prefix: "file", Kind: Positional}},
		{"tool build --target ar", 22, Context{Certain: true, Command: "tool", Subcommands: []string{"build"}, UsedOptions: []string{"--target"}, Prefix: "ar", Kind: OptionValue, Option: "--target"}},
		{"tool --verbose build --ta", 25, Context{Certain: true, Command: "tool", Subcommands: []string{"build"}, Prefix: "--ta", Kind: OptionName}},
		{"tool build file", 13, Context{Certain: true, Command: "tool", Subcommands: []string{"build"}, Prefix: "fi", Kind: Positional}},
	}
	for _, tt := range tests {
		got := Resolve(tt.line, tt.cursor, grammar)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Resolve(%q, %d) = %+v, want %+v", tt.line, tt.cursor, got, tt.want)
		}
	}
}

func TestResolveAdditionalSlots(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {Subcommands: map[string]CommandSpec{
		"build": {Positional: true},
	}}}
	for _, tt := range []struct {
		line string
		want Context
	}{
		{"tool bu", Context{Certain: true, Command: "tool", Prefix: "bu", Kind: Subcommand}},
		{"tool build first second", Context{Certain: true, Command: "tool", Subcommands: []string{"build"}, Slot: 1, Prefix: "second", Kind: Positional}},
	} {
		if got := Resolve(tt.line, len(tt.line), grammar); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Resolve(%q) = %+v, want %+v", tt.line, got, tt.want)
		}
	}
}

func TestResolveUsedOptionsAreScopedToCurrentCommand(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {
		Options: map[string]bool{"--verbose": false},
		Subcommands: map[string]CommandSpec{"build": {
			Options:    map[string]bool{"--verbose": false, "--target": false},
			Positional: true,
		}},
	}}
	got := Resolve("tool --verbose build --ta", len("tool --verbose build --ta"), grammar)
	if !got.Certain || got.Kind != OptionName || len(got.UsedOptions) != 0 {
		t.Fatalf("parent option leaked into child context: %+v", got)
	}
	got = Resolve("tool --verbose build --verbose --ta", len("tool --verbose build --verbose --ta"), grammar)
	if !got.Certain || !reflect.DeepEqual(got.UsedOptions, []string{"--verbose"}) {
		t.Fatalf("child option missing or parent leaked: %+v", got)
	}
}

func TestResolveRejectsUnmodeledOptionTerminator(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {
		Options:    map[string]bool{"--": false, "--verbose": false},
		Positional: true,
	}}
	for _, line := range []string{"tool -- --verbose ", "tool -- --ver", "tool -- file"} {
		if got := Resolve(line, len(line), grammar); got.Certain {
			t.Errorf("Resolve(%q) interpreted words after an unmodeled option terminator: %+v", line, got)
		}
	}
}

func TestResolveExplicitOptionTerminator(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {
		Options: map[string]bool{"--verbose": false}, Positional: true, EndOfOptions: true,
	}}
	for _, tt := range []struct {
		line string
		want Context
	}{
		{"tool -- --verbose", Context{Certain: true, Command: "tool", Prefix: "--verbose", Kind: Positional}},
		{"tool --verbose -- -file next", Context{Certain: true, Command: "tool", UsedOptions: []string{"--verbose"}, Slot: 1, Prefix: "next", Kind: Positional}},
		{"tool -- -- ", Context{Certain: true, Command: "tool", Slot: 1, Kind: Positional}},
	} {
		if got := Resolve(tt.line, len(tt.line), grammar); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Resolve(%q) = %+v, want %+v", tt.line, got, tt.want)
		}
	}
}

func TestResolveTerminatorAllowsCursorBeforeDashArgument(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {Positional: true, EndOfOptions: true}}
	line := "tool -- --file"
	want := Context{Certain: true, Command: "tool", Kind: Positional}
	if got := Resolve(line, len("tool -- "), grammar); !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve(%q) = %+v, want %+v", line, got, want)
	}
}

func TestResolveTerminatorDoesNotGuessSubcommandSemantics(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {EndOfOptions: true, Positional: true, Subcommands: map[string]CommandSpec{"run": {Positional: true}}}}
	if got := Resolve("tool -- run", len("tool -- run"), grammar); got.Certain {
		t.Fatalf("ambiguous subcommand after terminator: %+v", got)
	}
}

func TestResolveRejectsWordsAfterCursorInWhitespace(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {Positional: true}}
	for _, line := range []string{"tool  secret", "tool \tsecret"} {
		got := Resolve(line, 5, grammar)
		if got.Certain {
			t.Errorf("Resolve(%q, 5) ignored the word after the cursor: %+v", line, got)
		}
	}
}

func TestResolveRejectsOptionHiddenAfterCursor(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {Positional: true, Options: map[string]bool{"--output": true}}}
	for _, line := range []string{"tool --output", "tool ''--output"} {
		if got := Resolve(line, 5, grammar); got.Certain {
			t.Errorf("Resolve(%q, 5) ignored option suffix: %+v", line, got)
		}
	}
}

func TestResolveRejectsUncertainContext(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {Options: map[string]bool{"--output": true}, Subcommands: map[string]CommandSpec{"build": {}}, Positional: true}}
	for _, line := range []string{
		"unknown x", "tool | other", "tool --unknown x", "tool build x", "tool 'unfinished", "tool --output --other x",
	} {
		if got := Resolve(line, len(line), grammar); got.Certain {
			t.Errorf("Resolve(%q) unexpectedly certain: %+v", line, got)
		}
	}
	if got := Resolve("tool x trailing", 6, grammar); got.Certain {
		t.Errorf("later words must not be ignored: %+v", got)
	}
}

func TestResolveInlineOptionValue(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {Options: map[string]bool{"--output": true}}}
	ctx := Resolve("tool --output=o", len("tool --output=o"), grammar)
	want := Context{Certain: true, Command: "tool", Prefix: "o", InlinePrefix: "--output=", Kind: OptionValue, Option: "--output"}
	if !reflect.DeepEqual(ctx, want) {
		t.Fatalf("ctx = %+v, want %+v", ctx, want)
	}
	ctx = Resolve("tool --output=", len("tool --output="), grammar)
	if !ctx.Certain || ctx.Kind != OptionValue || ctx.Prefix != "" || ctx.InlinePrefix != "--output=" {
		t.Fatalf("empty inline ctx = %+v", ctx)
	}
}

func TestResolveMultiArgumentOption(t *testing.T) {
	grammar := map[string]CommandSpec{"tool": {
		Options:    map[string]bool{"--pair": true},
		OptionArgs: map[string]int{"--pair": 2},
		Positional: true,
	}}
	ctx := Resolve("tool --pair a ", len("tool --pair a "), grammar)
	if !ctx.Certain || ctx.Kind != OptionValue || ctx.Option != "--pair" || ctx.OptionSlot != 1 || ctx.Prefix != "" {
		t.Fatalf("ctx = %+v", ctx)
	}
	ctx = Resolve("tool --pair a b ", len("tool --pair a b "), grammar)
	if !ctx.Certain || ctx.Kind != Positional || ctx.Slot != 0 {
		t.Fatalf("after option args ctx = %+v", ctx)
	}
}
