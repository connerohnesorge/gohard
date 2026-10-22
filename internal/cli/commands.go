package cli

// Command is one gohard subcommand.
type Command struct {
	Name    string
	Args    string // positional synopsis for help, e.g. "[PATH...]"
	Summary string // one line for the command list
	// Subs, when set, are the allowed values of the first positional
	// argument, which is then required.
	Subs  []string
	Flags []Flag
	// Run executes the command. A nil Run means the command is not built
	// yet: the dispatcher reports cli/not-implemented and exits 2.
	Run func(inv *Invocation) Result
}

// formatFlag selects the diagnostic output format (#20).
var formatFlag = Flag{
	Name: "format", Kind: String, Value: "FORMAT", Default: "text",
	Enum:  []string{"text", "json", "sarif"},
	Usage: "diagnostic output format: text, json or sarif",
}

var strictFlag = Flag{
	Name: "strict", Kind: Bool,
	Usage: "exit 1 on any warning (hints never fail a run)",
}

var defineFlag = Flag{
	Name: "define", Kind: Strings, Value: "NAME=VALUE",
	Usage: "define a conditional-analysis identifier " +
		"(overrides defines:)",
}

var seedFlag = Flag{
	Name: "seed", Kind: String, Value: "N|random",
	Usage: "simulation seed; 0 keeps declaration order",
}

// envFlag and realEnvFlag set the std.env environment of a simulation
// (#18).
var envFlag = Flag{
	Name: "env", Kind: Strings, Value: "NAME=VALUE",
	Usage: "set a variable GETENV sees in simulation " +
		"(overrides simulation.env.vars)",
}

var realEnvFlag = Flag{
	Name: "real-env", Kind: Bool,
	Usage: "let simulation read the real clock and environment; " +
		"the run is marked non-reproducible",
}

var tbFlag = Flag{
	Name: "tb", Kind: String, Value: "NAME",
	Usage: "run only the testbench NAME",
}

// commands is every gohard command, sorted by name. Owning tickets fill
// in Run when they land (#20, #26, #28, #31, #32, #36, #1).
var commands = []Command{
	{
		Name: "check", Args: "[PATH...]",
		Summary: "parse and analyze VHDL; with --sim, run testbenches",
		Flags: []Flag{
			{
				Name: "allow-empty", Kind: Bool,
				Usage: "exit 0 when no VHDL files are found",
			},
			defineFlag,
			envFlag,
			formatFlag,
			realEnvFlag,
			{
				Name: "repro", Kind: String, Value: "FILE",
				Usage: "rerun the simulation recorded in a " +
					"repro file",
			},
			seedFlag,
			{
				Name: "sim", Kind: Bool,
				Usage: "also elaborate and simulate testbenches",
			},
			strictFlag,
			tbFlag,
		},
	},
	{
		Name: "config", Args: "check|schema",
		Summary: "check gohard.yaml or print its JSON schema",
		Subs:    []string{"check", "schema"},
		Flags:   []Flag{defineFlag, formatFlag, strictFlag},
	},
	{
		Name: "fmt", Args: "[PATH... | -]",
		Summary: "format VHDL files in place (- for stdin to " +
			"stdout), or check formatting",
		Flags: []Flag{
			{
				Name: "allow-empty", Kind: Bool,
				Usage: "exit 0 when no VHDL files are found",
			},
			{
				Name: "check", Kind: Bool,
				Usage: "write nothing; exit 1 if a file would " +
					"change",
			},
			defineFlag,
			formatFlag,
			strictFlag,
		},
	},
	{
		Name: "help", Args: "[COMMAND]",
		Summary: "show help for gohard or one command",
		// Run is set in init: runHelp reads this table.
	},
	{
		Name: "libs", Args: "detect",
		Summary: "find vendor libraries and print gohard.yaml lines",
		Subs:    []string{"detect"},
		Flags: []Flag{
			{
				Name: "version", Kind: Strings,
				Value: "[TOOL=]VERSION",
				Usage: "pick the version of a tool installed more " +
					"than once (vivado, quartus, diamond)",
			},
			{
				Name: "write", Kind: Bool,
				Usage: "add the detected lines to gohard.yaml",
			},
		},
	},
	{
		Name: "lsp", Args: "[ACTION [ARG...]]",
		Summary: "run the language server, or one language-server " +
			"action",
		Flags: []Flag{
			{
				Name: "at", Kind: String, Value: "PATH:LINE:COL",
				Usage: "position of the action (1-based, UTF-8 " +
					"columns)",
			},
			defineFlag,
			{
				Name: "dry-run", Kind: Bool,
				Usage: "rename: print the edit plan, write nothing",
			},
			envFlag,
			realEnvFlag,
			{
				Name: "record", Kind: String, Value: "DIR",
				Usage: "record every session to DIR/<n>.jsonl",
			},
			seedFlag,
			strictFlag,
			tbFlag,
		},
	},
	{
		Name: "parse", Args: "FILE",
		Summary: "print the concrete syntax tree of a file",
		Flags: []Flag{
			defineFlag,
			{
				Name: "dump", Kind: Bool,
				Usage: "print the tree in its stable text form",
			},
		},
	},
	{
		Name: "trace", Args: "export TRACE",
		Summary: "export a simulation trace to VCD or FST",
		Subs:    []string{"export"},
		Flags: []Flag{
			{
				Name: "format", Kind: String, Value: "vcd|fst",
				Usage: "export format (required)",
			},
			{
				Name: "out", Short: "o", Kind: String,
				Value: "FILE",
				Usage: "write the export to FILE (a temp file, " +
					"then rename) instead of standard output",
			},
		},
	},
	{
		Name: "version", Summary: "print the version, commit, IEEE " +
			"library and Go toolchain",
		Run: runVersion,
	},
}

func init() { lookup("help").Run = runHelp }

// lookup returns the command called name, or nil.
func lookup(name string) *Command {
	for i := range commands {
		if commands[i].Name == name {
			return &commands[i]
		}
	}
	return nil
}
