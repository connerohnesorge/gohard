package cli

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// runHelp is `gohard help [COMMAND]`.
func runHelp(inv *Invocation) Result {
	switch len(inv.Args) {
	case 0:
		return Result{Stdout: helpText(nil)}
	case 1:
		c := lookup(inv.Args[0])
		if c == nil {
			return inv.usage("unknown command " +
				strconv.Quote(inv.Args[0]))
		}
		return Result{Stdout: helpText(c)}
	}
	return inv.usage("gohard help takes at most one command")
}

// usage returns a cli/usage failure (exit 2).
func (inv *Invocation) usage(problem string) Result {
	return inv.Fail(newUsage(problem))
}

const exitHelp = `Exit codes:
  0    clean (warnings and hints do not fail a run)
  1    the design has problems (any warning with --strict)
  2    tool or setup failure, or an opaque unit; wins over 1
  130  interrupted (Ctrl-C)
  143  terminated (SIGTERM)
`

// helpText is the help for c, or the overview when c is nil. It is
// plain ASCII and never depends on the terminal width (#20).
func helpText(c *Command) []byte {
	var b bytes.Buffer
	if c == nil {
		b.WriteString("gohard is a VHDL language server with a " +
			"built-in simulator.\n\n" +
			"Usage:\n  gohard <command> [flags] [arguments]\n\n" +
			"Commands:\n")
		width := 0
		for _, c := range commands {
			width = max(width, len(c.Name))
		}
		for _, c := range commands {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, c.Name, c.Summary)
		}
		b.WriteString("\nGlobal flags:\n")
		writeFlags(&b, globalFlags)
		b.WriteString("\n" + exitHelp + "\nRun 'gohard help <command>' " +
			"for the flags of one command.\n")
		return b.Bytes()
	}
	synopsis := "gohard " + c.Name
	if len(c.Flags) > 0 {
		synopsis += " [flags]"
	}
	if c.Args != "" {
		synopsis += " " + c.Args
	}
	fmt.Fprintf(&b, "Usage:\n  %s\n\n%s.\n", synopsis,
		strings.ToUpper(c.Summary[:1])+c.Summary[1:])
	if len(c.Flags) > 0 {
		b.WriteString("\nFlags:\n")
		writeFlags(&b, c.Flags)
	}
	b.WriteString("\nGlobal flags:\n")
	writeFlags(&b, globalFlags)
	return b.Bytes()
}

func writeFlags(b *bytes.Buffer, flags []Flag) {
	cols := make([]string, len(flags))
	width := 0
	for i, f := range flags {
		s := "    "
		if f.Short != "" {
			s = "-" + f.Short + ", "
		}
		s += "--" + f.Name
		if f.Value != "" {
			s += " " + f.Value
		}
		cols[i] = s
		width = max(width, len(s))
	}
	for i, f := range flags {
		usage := f.Usage
		if f.Kind == Strings {
			usage += "; repeatable"
		}
		if f.Default != "" {
			usage += " (default " + f.Default + ")"
		}
		fmt.Fprintf(b, "  %-*s  %s\n", width, cols[i], usage)
	}
}
