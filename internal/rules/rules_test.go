package rules_test

import (
	"strings"
	"testing"

	"offline-complete/internal/rules"
)

const testRules = `{
  "schemaVersion": 1,
  "commands": [{"name":"git", "description":"Version control", "options":[{"name":"--version","description":"Show version"}],
    "subcommands":[{"name":"commit","description":"Record changes", "options":[{"name":"--message","description":"Commit message"}]}]}]
}`

func names(candidates []rules.Candidate) []string {
	result := make([]string, len(candidates))
	for i, candidate := range candidates {
		result[i] = candidate.Name
	}
	return result
}

func TestFromJSONRejectsOversizedDocument(t *testing.T) {
	input := `{"schemaVersion":1,"commands":[]}` + strings.Repeat(" ", 16<<20)
	if _, err := rules.FromJSON([]byte(input)); err == nil {
		t.Fatal("accepted a rule document larger than the loader limit")
	}
}

func TestFromJSONAcceptsDocumentAtSizeLimit(t *testing.T) {
	const document = `{"schemaVersion":1,"commands":[]}`
	input := document + strings.Repeat(" ", (16<<20)-len(document))
	if _, err := rules.FromJSON([]byte(input)); err != nil {
		t.Fatalf("rejected rule document at size limit: %v", err)
	}
}

func TestRejectsExcessivelyNestedRules(t *testing.T) {
	input := `{"schemaVersion":1,"commands":[` + strings.Repeat(`{"name":"x","subcommands":[`, 70) + `{"name":"leaf"}` + strings.Repeat(`]}`, 70) + `]}`
	if _, err := rules.FromJSON([]byte(input)); err == nil {
		t.Fatal("accepted excessively nested rule document")
	}
}

func TestAcceptsRuleNearNestingLimit(t *testing.T) {
	input := `{"schemaVersion":1,"commands":[` + strings.Repeat(`{"name":"x","subcommands":[`, 62) + `{"name":"leaf"}` + strings.Repeat(`]}`, 62) + `]}`
	if _, err := rules.FromJSON([]byte(input)); err != nil {
		t.Fatalf("rejected rule at nesting limit: %v", err)
	}
}

func TestSelectsCandidatesInTheGivenCommandContext(t *testing.T) {
	pack, err := rules.FromJSON([]byte(testRules))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}

	if got := names(pack.Complete([]string{"git"}, "com")); !equal(got, []string{"commit"}) {
		t.Fatalf("root candidates = %v", got)
	}
	commit := pack.Complete([]string{"git", "commit"}, "--m")
	if len(commit) != 1 || commit[0].Name != "--message" || commit[0].Description != "Commit message" {
		t.Fatalf("commit candidates = %+v", commit)
	}
	if got := pack.Complete([]string{"git", "unknown"}, ""); len(got) != 0 {
		t.Fatalf("unknown subcommand produced %v", got)
	}
	if got := pack.Complete([]string{"unknown"}, ""); len(got) != 0 {
		t.Fatalf("unknown command produced %v", got)
	}
}

func TestRejectsControlCharactersInCandidateNames(t *testing.T) {
	for _, input := range []string{
		`{"schemaVersion":1,"commands":[{"name":"git\u0000oops"}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","subcommands":[{"name":"co\u001bmmit"}]}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","options":[{"name":"--ver\n sion"}]}]}`,
	} {
		if _, err := rules.FromJSON([]byte(input)); err == nil {
			t.Errorf("accepted control character in candidate name: %s", input)
		}
	}
}

func TestRejectsInvalidUTF8InRuleData(t *testing.T) {
	data := []byte(`{"schemaVersion":1,"commands":[{"name":"git","description":"`)
	data = append(data, 0xff)
	data = append(data, []byte(`"}]}`)...)
	if _, err := rules.FromJSON(data); err == nil {
		t.Fatal("accepted invalid UTF-8 in a rule description")
	}
}

func TestRejectsUnpairedJSONSurrogates(t *testing.T) {
	for _, description := range []string{`\ud800`, `\udc00`, `\ud800x`, `\ud800A`} {
		input := `{"schemaVersion":1,"commands":[{"name":"git","description":"` + description + `"}]}`
		if _, err := rules.FromJSON([]byte(input)); err == nil {
			t.Errorf("accepted unpaired surrogate %s", description)
		}
	}
}

func TestAcceptsPairedSurrogatesAndLiteralEscapes(t *testing.T) {
	input := `{"schemaVersion":1,"commands":[{"name":"git","description":"🚀 \\ud800"}]}`
	pack, err := rules.FromJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if got := pack.Commands[0].Description; got != "🚀 \\ud800" {
		t.Fatalf("description = %q", got)
	}
}

func TestAcceptsUTF16SurrogatePair(t *testing.T) {
	input := `{"schemaVersion":1,"commands":[{"name":"git","description":"` + `\u` + `d83d` + `\u` + `de80"}]}`
	pack, err := rules.FromJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if pack.Commands[0].Description != "🚀" {
		t.Fatalf("description = %q", pack.Commands[0].Description)
	}
}

func TestRejectsNullRuleFieldValues(t *testing.T) {
	for _, input := range []string{
		`{"schemaVersion":1,"commands":[{"name":"git","description":null}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","options":null}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","subcommands":null}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","options":[{"name":"--help","description":null}]}]}`,
	} {
		if _, err := rules.FromJSON([]byte(input)); err == nil {
			t.Errorf("accepted null field: %s", input)
		}
	}
}

func TestRejectsIncorrectlyCasedRuleFields(t *testing.T) {
	for _, input := range []string{
		`{"SchemaVersion":1,"commands":[]}`,
		`{"schemaVersion":1,"Commands":[]}`,
		`{"schemaVersion":1,"commands":[{"Name":"git"}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","Options":[{"name":"--help"}]}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","options":[{"name":"--help","Description":"help"}]}]}`,
	} {
		if _, err := rules.FromJSON([]byte(input)); err == nil {
			t.Errorf("accepted incorrectly cased field: %s", input)
		}
	}
}

func TestRejectsDuplicateRuleFields(t *testing.T) {
	for _, input := range []string{
		`{"schemaVersion":1,"schemaVersion":1,"commands":[]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","name":"other"}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","na` + `\u` + `006de":"other"}]}`,
		`{"schemaVersion":1,"commands":[{"name":"git","Name":"other"}]}`,
	} {
		if _, err := rules.FromJSON([]byte(input)); err == nil {
			t.Errorf("accepted duplicate field: %s", input)
		}
	}
}

func TestRejectsMissingEmptyAndDuplicateRuleNames(t *testing.T) {
	for _, commands := range []string{
		`[{}]`,
		`[null]`,
		`[{"name":"git","options":[{}]}]`,
		`[{"name":"git","subcommands":[{"name":""}]}]`,
		`[{"name":"git"},{"name":"git"}]`,
		`[{"name":"git","options":[{"name":"--help"},{"name":"--help"}]}]`,
		`[{"name":"git","subcommands":[{"name":"status"},{"name":"status"}]}]`,
	} {
		input := `{"schemaVersion":1,"commands":` + commands + `}`
		if _, err := rules.FromJSON([]byte(input)); err == nil {
			t.Errorf("accepted invalid names: %s", input)
		}
	}
}

func TestRejectsUnsupportedSchemaAndMalformedRuleData(t *testing.T) {
	cases := []string{
		`{"schemaVersion":2,"commands":[]}`,
		`{"schemaVersion":1,"commands":[{"name":3}]}`,
		`{"schemaVersion":1,"commands":[],"extra":true}`,
		`{"commands":[]}`,
		`{"schemaVersion":1}`,
		`{"schemaVersion":1,"commands":[]} trailing`,
	}
	for _, input := range cases {
		if _, err := rules.FromJSON([]byte(input)); err == nil {
			t.Errorf("FromJSON accepted %q", input)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
