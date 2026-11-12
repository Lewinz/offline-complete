package rules_test

import (
	"reflect"
	"testing"

	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
)

func TestCompleteContextSelectsOnlyMatchingKindAndScope(t *testing.T) {
	pack, err := rules.FromJSON([]byte(`{"schemaVersion":1,"commands":[{"name":"git","options":[{"name":"--global","description":"global"}],"subcommands":[{"name":"remote","description":"remotes","options":[{"name":"--verbose","description":"details"},{"name":"--version"}],"subcommands":[{"name":"remove","description":"remove remote"},{"name":"rename","description":"rename remote"}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	grammar := map[string]shellparse.CommandSpec{"git": {Options: map[string]bool{"--global": false}, Subcommands: map[string]shellparse.CommandSpec{"remote": {Options: map[string]bool{"--verbose": false, "--version": false}, Subcommands: map[string]shellparse.CommandSpec{"remove": {}, "rename": {}}}}}}
	for _, tc := range []struct {
		line string
		want []rules.Candidate
	}{
		{"git remote re", []rules.Candidate{{Name: "remove", Description: "remove remote"}, {Name: "rename", Description: "rename remote"}}},
		{"git remote --verbose --ver", []rules.Candidate{{Name: "--version"}}},
		{"git --global --g", nil},
		{"git unknown --v", nil},
	} {
		t.Run(tc.line, func(t *testing.T) {
			ctx := shellparse.Resolve(tc.line, len(tc.line), grammar)
			got := pack.CompleteContext(ctx)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v (context %#v)", got, tc.want, ctx)
			}
		})
	}
}

func TestCompleteContextRejectsUncertainOrUnsupportedContexts(t *testing.T) {
	pack, err := rules.FromJSON([]byte(`{"schemaVersion":1,"commands":[{"name":"tool","options":[{"name":"--output"}],"subcommands":[{"name":"run"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []shellparse.Context{
		{Command: "tool", Kind: shellparse.OptionName, Prefix: "--"},
		{Certain: true, Command: "tool", Kind: shellparse.OptionValue, Prefix: "--"},
		{Certain: true, Command: "tool", Kind: shellparse.Positional},
		{Certain: true, Command: "tool", Subcommands: []string{"unknown"}, Kind: shellparse.OptionName, Prefix: "--"},
	} {
		if got := pack.CompleteContext(ctx); len(got) != 0 {
			t.Fatalf("context %#v: unexpected candidates %#v", ctx, got)
		}
	}
}

func TestGrammarBuildsSubcommandsAndLeafPositionals(t *testing.T) {
	pack, err := rules.FromJSON([]byte(testRules))
	if err != nil {
		t.Fatal(err)
	}
	grammar := pack.Grammar()
	git, ok := grammar["git"]
	if !ok || git.Positional || len(git.Subcommands) != 1 {
		t.Fatalf("git grammar = %+v", git)
	}
	commit, ok := git.Subcommands["commit"]
	if !ok || !commit.Positional || !commit.EndOfOptions || len(commit.Options) != 1 || commit.Options["--message"] {
		t.Fatalf("commit grammar = %+v", commit)
	}
}

func TestOptionArgumentIsResolvedForOptionValue(t *testing.T) {
	pack, err := rules.FromJSON([]byte(`{"schemaVersion":1,"commands":[{"name":"tool","options":[{"name":"--output","args":[{"name":"path","suggestions":[{"name":"out.txt"}]}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	grammar := pack.Grammar()
	ctx := shellparse.Resolve("tool --output o", len("tool --output o"), grammar)
	if !ctx.Certain || ctx.Kind != shellparse.OptionValue || ctx.Option != "--output" {
		t.Fatalf("context = %+v", ctx)
	}
	arg, ok := pack.ArgumentAt(ctx)
	if !ok || arg.Name != "path" || len(arg.Suggestions) != 1 || arg.Suggestions[0].Name != "out.txt" {
		t.Fatalf("arg = %#v ok=%v", arg, ok)
	}
}
