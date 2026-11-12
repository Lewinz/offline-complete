package agent_test

import (
	"bytes"
	"strings"
	"testing"

	"offline-complete/internal/agent"
	"offline-complete/internal/editstate"
	"offline-complete/internal/rules"
	"offline-complete/internal/shellparse"
)

func TestSessionOffersSelectsAndConsumesOnce(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git co", 6); err != nil {
		t.Fatal(err)
	}
	var writer bytes.Buffer
	session, err := agent.NewSession("session-1", state, testCompleter(), &writer)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	offer, offerID, ok := session.Refresh(true)
	if !ok || offerID == 0 || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "commit" {
		t.Fatalf("offer = %+v id=%d ok=%v", offer, offerID, ok)
	}

	if err := session.Select(1, offerID, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(writer.String(), "\x05\x15git commit") {
		t.Fatalf("written = %q", writer.String())
	}
	if err := session.Select(1, offerID, 0); err == nil {
		t.Fatal("second select accepted")
	}
}

func TestSessionRejectsStaleAndUnfocusedSelections(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git co", 6); err != nil {
		t.Fatal(err)
	}
	var writer bytes.Buffer
	session, err := agent.NewSession("session-1", state, testCompleter(), &writer)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	oldOffer, oldID, ok := session.Refresh(true)
	if !ok {
		t.Fatal("initial offer missing")
	}

	if err := state.Update(2, "git co", 6); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := session.Refresh(true); !ok {
		t.Fatal("second offer missing")
	}
	if err := session.Select(1, oldID, 0); err == nil {
		t.Fatal("stale offer accepted")
	}

	if err := state.Update(3, "git co", 6); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := session.Refresh(false); ok {
		t.Fatal("unfocused refresh produced an offer")
	}
	if err := session.Select(3, oldID, 0); err == nil {
		t.Fatal("unfocused selection accepted")
	}
	_ = oldOffer
}

func TestSessionClearsOfferWhenEditingStateDisappears(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git co", 6); err != nil {
		t.Fatal(err)
	}
	var writer bytes.Buffer
	session, err := agent.NewSession("session-1", state, testCompleter(), &writer)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	if _, _, ok := session.Refresh(true); !ok {
		t.Fatal("offer missing")
	}
	if err := state.Clear(2); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := session.Refresh(true); ok {
		t.Fatal("offer survived a cleared state")
	}
}

func TestNewSessionRejectsIncompleteArguments(t *testing.T) {
	state := &editstate.State{}
	if _, err := agent.NewSession("", state, testCompleter(), &bytes.Buffer{}); err == nil {
		t.Fatal("empty session accepted")
	}
	if _, err := agent.NewSession("s", nil, testCompleter(), &bytes.Buffer{}); err == nil {
		t.Fatal("nil state accepted")
	}
	if _, err := agent.NewSession("s", state, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("nil completer accepted")
	}
	if _, err := agent.NewSession("s", state, testCompleter(), nil); err == nil {
		t.Fatal("nil writer accepted")
	}
}

func TestSessionReoffersTheSameVersionAfterRefocus(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git co", 6); err != nil {
		t.Fatal(err)
	}
	var writer bytes.Buffer
	session, err := agent.NewSession("session-1", state, testCompleter(), &writer)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	first, firstID, ok := session.Refresh(true)
	if !ok {
		t.Fatal("first offer missing")
	}
	if _, _, ok := session.Refresh(false); ok {
		t.Fatal("unfocused refresh produced an offer")
	}
	second, secondID, ok := session.Refresh(true)
	if !ok {
		t.Fatal("refocused refresh produced no offer")
	}
	if first.Version != second.Version || secondID == firstID {
		t.Fatalf("first = %+v/%d, second = %+v/%d", first, firstID, second, secondID)
	}
	if err := session.Select(second.Version, secondID, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(writer.String(), "\x05\x15git commit") {
		t.Fatalf("written = %q", writer.String())
	}
}

func TestSessionRecordsExplicitSelection(t *testing.T) {
	state := &editstate.State{}
	if err := state.Update(1, "git co", 6); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingRanker{}
	session, err := agent.NewSession("session-1", state, testCompleter(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	session.SetRecorder(recorder)

	offer, offerID, ok := session.Refresh(true)
	if !ok {
		t.Fatal("offer missing")
	}
	if err := session.Select(offer.Version, offerID, 0); err != nil {
		t.Fatal(err)
	}
	if recorder.candidate != "commit" || recorder.context.Command != "git" {
		t.Fatalf("recorded context=%+v candidate=%q", recorder.context, recorder.candidate)
	}
}

func TestSessionSelectsInsertValue(t *testing.T) {
	pack := &rules.RulePack{SchemaVersion: 1, Commands: []rules.Command{{
		Name: "tool",
		Args: []rules.Arg{{Suggestions: []rules.Candidate{{Name: "display", InsertValue: "insert"}}}},
	}}}
	completer := &agent.Completer{Pack: pack, Grammar: map[string]shellparse.CommandSpec{"tool": {Positional: true}}}
	state := &editstate.State{}
	if err := state.Update(1, "tool d", 6); err != nil {
		t.Fatal(err)
	}
	var writer bytes.Buffer
	session, err := agent.NewSession("session-1", state, completer, &writer)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	offer, offerID, ok := session.Refresh(true)
	if !ok || len(offer.Candidates) != 1 {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
	if err := session.Select(1, offerID, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(writer.String(), "tool insert") {
		t.Fatalf("writer = %q", writer.String())
	}
}
