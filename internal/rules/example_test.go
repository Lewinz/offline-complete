package rules_test

import (
	"os"
	"testing"

	"offline-complete/internal/rules"
)

func TestBundledRulePackParses(t *testing.T) {
	data, err := os.ReadFile("../../rules/base.json")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Commands) < 4 {
		t.Fatalf("bundled pack has %d commands", len(pack.Commands))
	}
}

func TestBundledOrbRules(t *testing.T) {
	data, err := os.ReadFile("../../rules/base.json")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		context []string
		prefix  string
		want    string
	}{
		{[]string{"orb"}, "cre", "create"},
		{[]string{"orb"}, "--m", "--machine"},
		{[]string{"orb", "config"}, "se", "set"},
		{[]string{"orb", "usb"}, "li", "list"},
	} {
		found := false
		for _, candidate := range pack.Complete(test.context, test.prefix) {
			if candidate.Name == test.want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Complete(%v, %q) has no %q", test.context, test.prefix, test.want)
		}
	}
}

func TestExampleRulePackParses(t *testing.T) {
	data, err := os.ReadFile("../../examples/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	grammar := pack.Grammar()
	if _, ok := grammar["git"]; !ok {
		t.Fatal("example pack has no git grammar")
	}
}
