package diag

// Exit codes (#20). No other code is ever returned.
const (
	ExitClean       = 0   // no errors
	ExitProblems    = 1   // the design has problems
	ExitTool        = 2   // no trustworthy answer: tool or setup failure
	ExitInterrupted = 130 // SIGINT / Ctrl-C
	ExitTerminated  = 143 // SIGTERM
)

// Signal records whether the run was interrupted.
type Signal uint8

// Signals that end a run early.
const (
	NoSignal Signal = iota
	SIGINT
	SIGTERM
)

// Status is the JSON "status" field.
type Status string

// Statuses, one per exit class.
const (
	StatusClean       Status = "clean"
	StatusProblems    Status = "problems"
	StatusToolError   Status = "tool_error"
	StatusInterrupted Status = "interrupted"
)

// ExitCode implements the #20 rules:
//
//   - an interrupt wins: 130 for SIGINT, 143 for SIGTERM;
//   - otherwise any Tool-class diagnostic exits 2, and 2 wins over 1 (the
//     design errors are still printed);
//   - otherwise any error, any Unchecked-class diagnostic (a run with
//     unchecked units never exits 0), or with strict any warning, exits 1;
//   - otherwise 0. Hints never fail a run.
func ExitCode(ds []Diagnostic, strict bool, sig Signal) int {
	switch sig {
	case SIGINT:
		return ExitInterrupted
	case SIGTERM:
		return ExitTerminated
	}
	code := ExitClean
	for _, d := range ds {
		switch {
		case d.Class() == Tool:
			return ExitTool
		case d.Severity == Error,
			d.Class() == Unchecked,
			strict && d.Severity == Warning:
			code = ExitProblems
		}
	}
	return code
}

// StatusOf maps an exit code to its JSON status.
func StatusOf(exit int) Status {
	switch exit {
	case ExitClean:
		return StatusClean
	case ExitProblems:
		return StatusProblems
	case ExitInterrupted, ExitTerminated:
		return StatusInterrupted
	}
	return StatusToolError
}

// Counts are the four numbers of the summary line.
type Counts struct {
	Errors   int
	Warnings int
	Hints    int
	Files    int
}

// Report is the complete result of one command run, ready to render.
type Report struct {
	// Tool and Version identify GoHard in SARIF output.
	Tool    string
	Version string
	// Diagnostics in any order; renderers normalize them.
	Diagnostics []Diagnostic
	// Files is the number of distinct files the run looked at.
	Files int
	// Incomplete lists instance paths whose static evaluation hit the
	// step limit (#34), in any order.
	Incomplete []string
	Strict     bool
	Signal     Signal
	// Source returns the text of the file at a diagnostic's path, if
	// the run read it. SARIF needs it to turn GoHard's UTF-8 byte
	// columns into UTF-16 code units; nil means no file is known.
	Source func(path string) ([]byte, bool)
}

// Exit returns the exit code of the report.
func (r *Report) Exit() int {
	return ExitCode(r.Diagnostics, r.Strict, r.Signal)
}

// Counts returns the summary counts of the normalized diagnostics.
func (r *Report) Counts() Counts {
	c := Counts{Files: r.Files}
	for _, d := range Normalize(r.Diagnostics) {
		switch d.Severity {
		case Error:
			c.Errors++
		case Warning:
			c.Warnings++
		default:
			c.Hints++
		}
	}
	return c
}

// firstTool returns the first Tool-class diagnostic in sorted order, the
// "error" of the JSON form and the SARIF notification reason.
func firstTool(ds []Diagnostic) (Diagnostic, bool) {
	for _, d := range ds {
		if d.Class() == Tool {
			return d, true
		}
	}
	return Diagnostic{}, false
}
