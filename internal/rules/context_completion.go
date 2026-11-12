package rules

import (
	"strings"

	"offline-complete/internal/shellparse"
)

// CompleteContext returns static candidates only for a certain, explicitly
// resolved context. It does not establish shell command identity or perform
// insertion; callers must validate those separately.
func (p *RulePack) CompleteContext(ctx shellparse.Context) []Candidate {
	if p == nil || !ctx.Certain || (ctx.Kind != shellparse.Subcommand && ctx.Kind != shellparse.OptionName) {
		return nil
	}
	commands := p.Commands
	var current *Command
	path := append([]string{ctx.Command}, ctx.Subcommands...)
	for _, name := range path {
		current = nil
		for i := range commands {
			if commands[i].Name == name {
				current = &commands[i]
				break
			}
		}
		if current == nil {
			return nil
		}
		commands = current.Subcommands
	}
	var result []Candidate
	if ctx.Kind == shellparse.Subcommand {
		for _, command := range current.Subcommands {
			if strings.HasPrefix(command.Name, ctx.Prefix) {
				result = append(result, Candidate{Name: command.Name, Description: command.Description})
			}
		}
	} else {
		used := make(map[string]bool, len(ctx.UsedOptions))
		for _, name := range ctx.UsedOptions {
			used[name] = true
		}
		for _, option := range current.Options {
			if !used[option.Name] && strings.HasPrefix(option.Name, ctx.Prefix) {
				result = append(result, Candidate{Name: option.Name, Description: option.Description})
			}
		}
	}
	return result
}

// Grammar builds the shell grammar used by the context resolver. Options with
// declared arguments consume the following word; commands with subcommands
// reject positionals, and leaf commands accept them.
func (p *RulePack) Grammar() map[string]shellparse.CommandSpec {
	if p == nil {
		return nil
	}
	grammar := make(map[string]shellparse.CommandSpec, len(p.Commands))
	for _, command := range p.Commands {
		grammar[command.Name] = grammarForCommand(command)
	}
	return grammar
}

func grammarForCommand(command Command) shellparse.CommandSpec {
	spec := shellparse.CommandSpec{
		Options:      make(map[string]bool, len(command.Options)),
		OptionArgs:   make(map[string]int, len(command.Options)),
		Subcommands:  make(map[string]shellparse.CommandSpec, len(command.Subcommands)),
		Positional:   len(command.Subcommands) == 0 || len(command.Args) > 0 || command.Dynamic,
		EndOfOptions: len(command.Subcommands) == 0,
	}
	for _, option := range command.Options {
		spec.Options[option.Name] = len(option.Args) > 0
		if len(option.Args) > 1 {
			spec.OptionArgs[option.Name] = len(option.Args)
		}
	}
	for _, child := range command.Subcommands {
		spec.Subcommands[child.Name] = grammarForCommand(child)
	}
	return spec
}
