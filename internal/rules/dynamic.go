package rules

import "offline-complete/internal/shellparse"

// CommandAt returns the command node selected by ctx. It does not execute
// generators or inspect the filesystem.
func (p *RulePack) CommandAt(ctx shellparse.Context) *Command {
	if p == nil || !ctx.Certain {
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
	return current
}

// ArgumentAt returns the positional argument definition for ctx. It returns
// false when the current slot is not modeled or no argument is declared.
func (p *RulePack) ArgumentAt(ctx shellparse.Context) (Arg, bool) {
	command := p.CommandAt(ctx)
	if command == nil {
		return Arg{}, false
	}
	if ctx.Kind == shellparse.OptionValue {
		for _, option := range command.Options {
			if option.Name == ctx.Option {
				return argAt(option.Args, ctx.OptionSlot)
			}
		}
		return Arg{}, false
	}
	return argAt(command.Args, ctx.Slot)
}

func argAt(args []Arg, slot int) (Arg, bool) {
	if len(args) == 0 || slot < 0 {
		return Arg{}, false
	}
	if slot < len(args) {
		return args[slot], true
	}
	last := args[len(args)-1]
	if last.IsVariadic {
		return last, true
	}
	return Arg{}, false
}
