package introspect

import (
	"sort"
	"testing"
)

func childNames(node *Node) []string {
	if node == nil {
		return nil
	}
	result := make([]string, 0, len(node.Subcommands))
	for _, child := range node.Subcommands {
		result = append(result, child.Name)
	}
	sort.Strings(result)
	return result
}

func findNode(t *testing.T, root *Node, path ...string) *Node {
	t.Helper()
	node := root
	for _, name := range path {
		var next *Node
		for _, child := range node.Subcommands {
			if child.Name == name {
				next = child
				break
			}
		}
		if next == nil {
			t.Fatalf("missing node %q in path %v from %v", name, path, childNames(node))
		}
		node = next
	}
	return node
}

func optionNamesFor(node *Node) map[string]Option {
	result := make(map[string]Option, len(node.Options))
	for _, option := range node.Options {
		result[option.Name] = option
	}
	return result
}

func TestParsePlainIndentedTable(t *testing.T) {
	help := `dotfiles - manage this Mac's shared configuration

  status                         Show links and OrbStack status
  apply [zsh|all] [--backup]     Apply Zsh links; back up conflicts if requested
  module list                    List available modules
  skills init|list|status        Initialize or inspect shared skills
  skills apply [--target DIR]    Link skills into agent directories
  mcp apply                      Apply shared MCP servers to supported agents
  doctor                         Check dependencies
  orbstack install|start|status  Manage OrbStack
  db up|down|stop|status         Manage local databases
  db logs [service]              Show recent database logs

Use Git directly to sync this repository. Database data stays in local volumes.
`
	spec := ParseHelp("dotfiles", []byte(help))
	got := childNames(spec.Root)
	want := []string{"apply", "db", "doctor", "mcp", "module", "orbstack", "skills", "status"}
	if len(got) != len(want) {
		t.Fatalf("top-level commands = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("top-level commands = %v, want %v", got, want)
		}
	}
	if got := childNames(findNode(t, spec.Root, "skills")); !equalStrings(got, []string{"apply", "init", "list", "status"}) {
		t.Fatalf("skills = %v", got)
	}
	if got := childNames(findNode(t, spec.Root, "db")); !equalStrings(got, []string{"down", "logs", "status", "stop", "up"}) {
		t.Fatalf("db = %v", got)
	}
	if got := childNames(findNode(t, spec.Root, "mcp")); !equalStrings(got, []string{"apply"}) {
		t.Fatalf("mcp = %v", got)
	}
	apply := findNode(t, spec.Root, "apply")
	if option, ok := optionNamesFor(apply)["--backup"]; !ok || option.TakesValue {
		t.Fatalf("apply options = %#v", apply.Options)
	}
	if len(apply.Values) != 2 || apply.Values[0].Name != "zsh" || apply.Values[1].Name != "all" {
		t.Fatalf("apply values = %#v", apply.Values)
	}
	if apply.AcceptsPaths {
		t.Fatal("an option name such as --backup must not imply positional paths")
	}
	link := findNode(t, spec.Root, "skills", "apply")
	option, ok := optionNamesFor(link)["--target"]
	if !ok || !option.TakesValue || !option.PathValue {
		t.Fatalf("skills apply options = %#v", link.Options)
	}
	logs := findNode(t, spec.Root, "db", "logs")
	if len(logs.Values) != 0 || logs.AcceptsPaths {
		t.Fatalf("db logs should not invent values: %#v", logs)
	}
	if findNode(t, spec.Root, "status").Description == "" {
		t.Fatal("status description missing")
	}
}

func TestParseCobraSections(t *testing.T) {
	help := `Usage:
  tool [command]

Available Commands:
  alpha       Alpha command
  beta        Beta command

Flags:
  -h, --help               help for tool
      --namespace string   Namespace
`
	spec := ParseHelp("tool", []byte(help))
	if got := childNames(spec.Root); !equalStrings(got, []string{"alpha", "beta"}) {
		t.Fatalf("commands = %v", got)
	}
	options := optionNamesFor(spec.Root)
	for _, name := range []string{"-h", "--help"} {
		option, ok := options[name]
		if !ok || option.TakesValue {
			t.Fatalf("option %s = %#v", name, option)
		}
	}
	if option, ok := options["--namespace"]; !ok || !option.TakesValue {
		t.Fatalf("--namespace = %#v", option)
	}
}

func TestParseClapSections(t *testing.T) {
	help := `Usage: app [OPTIONS] [COMMAND]

Commands:
  build    Build the project
  run      Run it

Options:
  -v, --verbose        Verbose
      --output <FILE>  Output file
`
	spec := ParseHelp("app", []byte(help))
	if got := childNames(spec.Root); !equalStrings(got, []string{"build", "run"}) {
		t.Fatalf("commands = %v", got)
	}
	options := optionNamesFor(spec.Root)
	verbose, ok := options["--verbose"]
	if !ok || verbose.TakesValue {
		t.Fatalf("--verbose = %#v", verbose)
	}
	output, ok := options["--output"]
	if !ok || !output.TakesValue || !output.PathValue {
		t.Fatalf("--output = %#v", output)
	}
}

func TestParseGoCommandListAndIgnoreProse(t *testing.T) {
	help := `Usage:
    go <command> [arguments]

The commands are:
    bug         start a bug report
    build       compile packages and dependencies

Use "go help <command>" for more information about a command.
`
	spec := ParseHelp("go", []byte(help))
	if got := childNames(spec.Root); !equalStrings(got, []string{"bug", "build"}) {
		t.Fatalf("commands = %v", got)
	}
	for _, child := range spec.Root.Subcommands {
		if child.Name == "use" || child.Name == "go" {
			t.Fatalf("prose leaked into commands: %v", childNames(spec.Root))
		}
	}
}

func TestSpecCandidatesAndGrammar(t *testing.T) {
	spec := ParseHelp("dotfiles", []byte(`  status    Show links
  apply [zsh|all] [--backup]  Apply
  skills init|list|status  Skills
  skills apply [--target DIR]  Link
  db up|down|stop|status  Database
`))
	if specCount(spec) == 0 {
		t.Fatal("empty spec")
	}
	grammar := spec.Grammar()["dotfiles"]
	if !grammar.Subcommands["skills"].Subcommands["apply"].Positional {
		t.Fatal("skills apply should accept positionals")
	}
	if grammar.Subcommands["skills"].Positional {
		t.Fatal("skills should be subcommand-only")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
