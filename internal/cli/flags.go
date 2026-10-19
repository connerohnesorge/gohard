package cli

import (
	"github.com/connerohnesorge/gohard/internal/diag"

	"slices"
	"strconv"
	"strings"
)

// Kind is the value type of a flag.
type Kind uint8

// Flag kinds.
const (
	Bool    Kind = iota + 1 // --name or --name=true|false
	String                  // --name VALUE; given at most once
	Strings                 // --name VALUE, repeatable, kept in order
	Int                     // --name N; given at most once
)

// Flag declares one command-line flag.
type Flag struct {
	Name    string   // long name, without dashes
	Short   string   // optional one-letter name, without the dash
	Kind    Kind     //
	Value   string   // placeholder shown in help, e.g. "FILE"
	Default string   // value when the flag is absent
	Enum    []string // allowed values, if restricted
	Usage   string   // one line of help
}

// Global flags are accepted before and after the command name.
var globalFlags = []Flag{
	{
		Name: "help", Short: "h", Kind: Bool,
		Usage: "show help",
	},
	{
		Name: "jobs", Short: "p", Kind: Int, Value: "N",
		Usage: "run at most N parallel jobs " +
			"(output never depends on N)",
	},
	{
		Name: "output", Kind: String, Value: "FILE",
		Usage: "write standard output to FILE " +
			"(a temp file, then rename)",
	},
}

// parsed is the result of parsing the command line.
type parsed struct {
	cmd    *Command
	args   []string
	values map[string][]string
}

func (p *parsed) set(name string) bool { return len(p.values[name]) > 0 }

func (p *parsed) last(name string) string {
	v := p.values[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// usageError is a bad command line, reported as cli/usage.
type usageError struct{ problem string }

// parseArgs splits args into the command, its flags and its positional
// arguments. Flags may come before or after positional arguments; "--"
// ends flag parsing. Before the command name only global flags are
// accepted.
func parseArgs(args []string) (*parsed, *usageError) {
	p := &parsed{values: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			rest := args[i+1:]
			if p.cmd == nil && len(rest) > 0 {
				if err := p.setCommand(rest[0]); err != nil {
					return p, err
				}
				rest = rest[1:]
			}
			p.args = append(p.args, rest...)
			i = len(args)
		case strings.HasPrefix(a, "-") && a != "-":
			next, err := p.flag(args, i)
			if err != nil {
				return p, err
			}
			i = next
		case p.cmd == nil:
			if err := p.setCommand(a); err != nil {
				return p, err
			}
		default:
			p.args = append(p.args, a)
		}
	}
	return p, nil
}

func (p *parsed) setCommand(name string) *usageError {
	c := lookup(name)
	if c == nil {
		return &usageError{"unknown command " + strconv.Quote(name)}
	}
	p.cmd = c
	return nil
}

// flag parses the flag at args[i] and returns the index of the last
// argument it consumed.
func (p *parsed) flag(args []string, i int) (int, *usageError) {
	a := args[i]
	long := strings.HasPrefix(a, "--")
	body := strings.TrimLeft(a, "-")
	name, value, hasValue := strings.Cut(body, "=")
	attached := false
	if !long && len(name) > 1 && !hasValue {
		// -p4 is -p 4.
		name, value, hasValue, attached = name[:1], name[1:], true, true
	}
	f := p.find(name, long)
	if f == nil || !long && len(name) != 1 ||
		attached && f.Kind == Bool {
		where := ""
		if p.cmd != nil {
			where = " for gohard " + p.cmd.Name
		}
		return i, &usageError{"unknown flag " + a + where}
	}
	display := "--" + f.Name
	switch {
	case f.Kind == Bool:
		if !hasValue {
			value = "true"
		} else if value != "true" && value != "false" {
			return i, &usageError{display + " must be true or false, " +
				"not " + strconv.Quote(value)}
		}
	case !hasValue:
		if i+1 >= len(args) {
			return i, &usageError{display + " needs a value"}
		}
		i++
		value = args[i]
	}
	if f.Kind == Int {
		if n, err := strconv.Atoi(value); err != nil || n < 1 {
			return i, &usageError{display + " needs a positive " +
				"integer, not " + strconv.Quote(value)}
		}
	}
	if len(f.Enum) > 0 && !slices.Contains(f.Enum, value) {
		return i, &usageError{display + " must be one of " +
			strings.Join(f.Enum, ", ") + ", not " +
			strconv.Quote(value)}
	}
	if (f.Kind == String || f.Kind == Int) && p.set(f.Name) {
		return i, &usageError{display + " is given more than once"}
	}
	p.values[f.Name] = append(p.values[f.Name], value)
	return i, nil
}

// find looks a flag up among the global flags and, once the command is
// known, the command's flags.
func (p *parsed) find(name string, long bool) *Flag {
	sets := [][]Flag{globalFlags}
	if p.cmd != nil {
		sets = append(sets, p.cmd.Flags)
	}
	for _, set := range sets {
		for i := range set {
			f := &set[i]
			if long && f.Name == name || !long && f.Short == name &&
				name != "" {
				return f
			}
		}
	}
	return nil
}

func newUsage(problem string) diag.Diagnostic {
	return diag.New(diag.CLIUsage, problem)
}
