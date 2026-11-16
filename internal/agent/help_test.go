package agent

import (
	"testing"

	"offline-complete/internal/editstate"
	"offline-complete/internal/introspect"
	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
)

func editstateSnapshot(line string) editstate.Snapshot {
	return editstate.Snapshot{Version: 1, Line: line, Cursor: len(line)}
}

type fakeHelp struct {
	spec    *introspect.Spec
	ensured int
	looked  int
	cleared int
}

func (f *fakeHelp) Ensure(command, cwd string) (*introspect.Spec, bool) {
	f.ensured++
	if f.spec == nil {
		return nil, false
	}
	return f.spec, true
}

func (f *fakeHelp) Lookup(command, cwd string) (*introspect.Spec, bool) {
	f.looked++
	if f.spec == nil {
		return nil, false
	}
	return f.spec, true
}

func (f *fakeHelp) Clear(command string) {
	f.cleared++
}

func helpSpecForTest() *introspect.Spec {
	return &introspect.Spec{
		Command: "tool",
		Root: &introspect.Node{
			Name: "tool",
			Subcommands: []*introspect.Node{
				{
					Name:        "build",
					Description: "Build things",
					Options: []introspect.Option{
						{Name: "--target", TakesValue: true, PathValue: true},
					},
				},
				{Name: "status", Description: "Show status"},
			},
			Options: []introspect.Option{{Name: "--verbose"}},
		},
	}
}

func newHelpCompleter(help HelpProvider) *Completer {
	return &Completer{
		Pack:    &rules.RulePack{SchemaVersion: 1},
		Grammar: map[string]shellparse.CommandSpec{},
		Help:    help,
	}
}

func TestCompleterUsesHelpSpecForUnknownCommand(t *testing.T) {
	help := &fakeHelp{spec: helpSpecForTest()}
	completer := newHelpCompleter(help)

	offer, ok := completer.Complete(editstateSnapshot("tool "))
	if !ok {
		t.Fatal("no offer for unknown command with help spec")
	}
	if len(offer.Candidates) != 2 || offer.Candidates[0].Name != "build" || offer.Candidates[1].Name != "status" {
		t.Fatalf("candidates = %#v", offer.Candidates)
	}
	if offer.Context.Kind != shellparse.Subcommand {
		t.Fatalf("kind = %v", offer.Context.Kind)
	}
	if !offer.HelpDerived {
		t.Fatal("help-derived offer was not marked")
	}
	if help.ensured != 1 {
		t.Fatalf("Ensure calls = %d", help.ensured)
	}

	offer, ok = completer.Complete(editstateSnapshot("tool bu"))
	if !ok || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "build" {
		t.Fatalf("filtered candidates = %#v ok=%v", offer.Candidates, ok)
	}

	offer, ok = completer.Complete(editstateSnapshot("tool build --"))
	if !ok {
		t.Fatal("no option offer")
	}
	found := false
	for _, candidate := range offer.Candidates {
		if candidate.Name == "--target" {
			found = true
		}
	}
	if !found {
		t.Fatalf("options = %#v", offer.Candidates)
	}
}

func TestCompleterDoesNotUseHelpForKnownCommand(t *testing.T) {
	help := &fakeHelp{spec: helpSpecForTest()}
	completer := newHelpCompleter(help)
	completer.Pack.Commands = []rules.Command{{
		Name:        "git",
		Subcommands: []rules.Command{{Name: "status"}},
	}}
	completer.Grammar["git"] = shellparse.CommandSpec{
		Subcommands: map[string]shellparse.CommandSpec{"status": {Positional: true}},
	}
	offer, ok := completer.Complete(editstateSnapshot("git "))
	if !ok || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "status" {
		t.Fatalf("static candidates = %#v ok=%v", offer.Candidates, ok)
	}
	if help.ensured != 0 {
		t.Fatalf("help was consulted for a known command: %d", help.ensured)
	}
}

func TestCompleterWithoutHelpSpecKeepsSilence(t *testing.T) {
	completer := newHelpCompleter(&fakeHelp{})
	if _, ok := completer.Complete(editstateSnapshot("unknown ")); ok {
		t.Fatal("unknown command produced an offer without a spec")
	}
}

func TestCompleterSkipsWrapperCommands(t *testing.T) {
	help := &fakeHelp{spec: helpSpecForTest()}
	completer := newHelpCompleter(help)
	for _, line := range []string{"sudo ", "env ", "xargs ", "oc "} {
		if _, ok := completer.Complete(editstateSnapshot(line)); ok {
			t.Fatalf("wrapper %q produced an offer", line)
		}
	}
	if help.ensured != 0 {
		t.Fatalf("help was consulted for wrappers: %d", help.ensured)
	}
}

func TestCompleterRefreshHelpClearsOnlyHelpCommands(t *testing.T) {
	help := &fakeHelp{spec: helpSpecForTest()}
	completer := newHelpCompleter(help)
	if !completer.RefreshHelp(editstateSnapshot("tool ")) {
		t.Fatal("help command refresh was rejected")
	}
	if help.cleared != 1 || help.ensured != 1 {
		t.Fatalf("cleared=%d ensured=%d", help.cleared, help.ensured)
	}
	completer.Grammar["git"] = shellparse.CommandSpec{Positional: true}
	if completer.RefreshHelp(editstateSnapshot("git ")) {
		t.Fatal("static command refresh was accepted")
	}
	if help.cleared != 1 {
		t.Fatalf("static command cleared the help cache: %d", help.cleared)
	}
}
