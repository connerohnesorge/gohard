// Package cli implements the gohard commands on top of an env.Env, so
// cmd/gohard only builds the real Env and calls Run, and tests run the
// whole command line in-process (testdata/cli).
//
// Output rules (#20): everything is buffered and written once at the
// end; diagnostics go to stdout and logs to stderr; --output FILE writes
// a temp file and renames it; the exit code is always one of 0, 1, 2,
// 130 or 143.
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
)

// Version is the gohard release version. It is also the value of the
// TOOL_VERSION conditional-analysis identifier (#31) and is recorded in
// repro files (#29). Release builds set it with
// -ldflags "-X github.com/connerohnesorge/gohard/internal/cli.Version=…".
var Version = "0.0.0-dev"

// Invocation is what a command's Run receives.
type Invocation struct {
	Ctx     context.Context
	Env     env.Env
	Command *Command
	// Args are the positional arguments after the command name.
	Args []string
	// Jobs is -p N, or 0 for the job runner's default.
	Jobs   int
	values map[string][]string
	stderr *bytes.Buffer
}

// Result is what a command returns. Either Report is set, and the
// dispatcher renders it in the --format the user chose and takes the exit
// code from it, or Stdout and Exit are used as they are.
type Result struct {
	Report *diag.Report
	Stdout []byte
	Exit   int
}

func (inv *Invocation) flag(name string) *Flag {
	for _, set := range [][]Flag{inv.Command.Flags, globalFlags} {
		for i := range set {
			if set[i].Name == name {
				return &set[i]
			}
		}
	}
	panic("cli: gohard " + inv.Command.Name + " has no flag --" + name)
}

// IsSet reports whether flag name was given.
func (inv *Invocation) IsSet(name string) bool {
	inv.flag(name)
	return len(inv.values[name]) > 0
}

// String returns the value of a String flag, or its default.
func (inv *Invocation) String(name string) string {
	f := inv.flag(name)
	if v := inv.values[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return f.Default
}

// Strings returns every value of a Strings flag in command-line order.
func (inv *Invocation) Strings(name string) []string {
	inv.flag(name)
	return append([]string(nil), inv.values[name]...)
}

// Bool returns the value of a Bool flag.
func (inv *Invocation) Bool(name string) bool {
	return inv.String(name) == "true"
}

// Int returns the value of an Int flag (validated by the parser), or 0.
func (inv *Invocation) Int(name string) int {
	n, _ := strconv.Atoi(inv.String(name))
	return n
}

// Logf writes one log line to the buffered stderr (#20: logs go to
// stderr, diagnostics to stdout).
func (inv *Invocation) Logf(format string, args ...any) {
	fmt.Fprintf(inv.stderr, "gohard: "+format+"\n", args...)
}

// Format returns the diagnostic output format chosen with --format, or
// text for commands whose --format means something else.
func (inv *Invocation) Format() diag.Format {
	for _, f := range inv.Command.Flags {
		if f.Name == "format" {
			if df, ok := diag.ParseFormat(inv.String("format")); ok {
				return df
			}
		}
	}
	return diag.FormatText
}

// NewReport returns an empty report for this run, with --strict applied
// when the command has that flag.
func (inv *Invocation) NewReport() *diag.Report {
	r := &diag.Report{Tool: "gohard", Version: Version}
	for _, f := range inv.Command.Flags {
		if f.Name == "strict" {
			r.Strict = inv.Bool("strict")
		}
	}
	return r
}

// Fail returns a Result whose report holds the diagnostics ds.
func (inv *Invocation) Fail(ds ...diag.Diagnostic) Result {
	r := inv.NewReport()
	r.Diagnostics = ds
	return Result{Report: r}
}

// Run runs one gohard command line (args excludes the program name) and
// returns the process exit code. Cancelling ctx with an env.Interrupt
// cause ends the run with exit 130 or 143.
func Run(ctx context.Context, e env.Env, args []string) int {
	var stderr bytes.Buffer
	out, exit, target := run(ctx, e, args, &stderr)
	if target != "" {
		if err := writeOutput(e.FS, target, out); err != nil {
			outputFailed(&stderr, target, err)
			exit = diag.ExitTool
		}
		out = nil
	}
	// Stderr write errors are ignored: there is nowhere left to report
	// them. Stdout is the run's result: a closed pipe (| head) exits
	// quietly with the run's code, but any other failure means stdout
	// can't be trusted, so it is exit 2 (#20).
	if stderr.Len() > 0 {
		e.Transport.Stderr.Write(stderr.Bytes())
	}
	if len(out) > 0 {
		_, err := e.Transport.Stdout.Write(out)
		if err != nil && !errors.Is(err, env.ErrBrokenPipe) {
			var b bytes.Buffer
			outputFailed(&b, "standard output", err)
			e.Transport.Stderr.Write(b.Bytes())
			exit = diag.ExitTool
		}
	}
	return exit
}

// outputFailed logs cli/output-failed for target. The reason is the
// system's error text alone: temp-file names are random, and the same
// failure must give the same bytes (P1).
func outputFailed(w *bytes.Buffer, target string, err error) {
	d := diag.New(diag.CLIOutputFailed, target, errReason(err))
	fmt.Fprintf(w, "gohard: %s[%s]: %s\n", d.Severity, d.Code, d.Message)
}

// errReason strips the operation and file names from err.
func errReason(err error) string {
	var pe *fs.PathError
	var le *os.LinkError
	switch {
	case errors.As(err, &pe):
		return pe.Err.Error()
	case errors.As(err, &le):
		return le.Err.Error()
	}
	return err.Error()
}

// run returns the buffered stdout, the exit code and the --output target.
func run(ctx context.Context, e env.Env, args []string,
	stderr *bytes.Buffer,
) (out []byte, exit int, target string) {
	p, uerr := parseArgs(args)
	target = p.last("output")
	inv := &Invocation{
		Ctx: ctx, Env: e, Command: p.cmd, Args: p.args,
		values: p.values, stderr: stderr,
	}
	inv.Env.FS = cancelFS{FS: e.FS, ctx: ctx}
	if inv.Command == nil {
		inv.Command = &Command{Name: ""}
		if uerr == nil && p.set("help") {
			return helpText(nil), 0, target
		}
		if uerr == nil {
			uerr = &usageError{"no command given"}
		}
	}
	if uerr == nil {
		uerr = checkSubs(inv)
	}
	var res Result
	switch {
	case uerr != nil:
		res = inv.Fail(newUsage(uerr.problem))
	case p.set("help"):
		res = Result{Stdout: helpText(inv.Command)}
	case inv.Command.Run == nil:
		res = inv.Fail(diag.New(diag.CLINotImplemented,
			inv.Command.Name))
	default:
		inv.Jobs = inv.Int("jobs")
		res = safeRun(inv)
	}
	var intr env.Interrupt
	if errors.As(context.Cause(ctx), &intr) {
		return interrupted(inv, res, intr), intr.Exit, target
	}
	if res.Report == nil {
		return res.Stdout, res.Exit, target
	}
	return diag.Render(inv.Format(), res.Report), res.Report.Exit(), target
}

func checkSubs(inv *Invocation) *usageError {
	subs := inv.Command.Subs
	if len(subs) == 0 {
		return nil
	}
	want := strings.Join(subs, " or ")
	if len(inv.Args) == 0 {
		return &usageError{"gohard " + inv.Command.Name +
			" needs a subcommand: " + want}
	}
	for _, s := range subs {
		if inv.Args[0] == s {
			return nil
		}
	}
	return &usageError{"unknown subcommand gohard " + inv.Command.Name +
		" " + strconv.Quote(inv.Args[0]) + "; expected " + want}
}

// interrupted renders the outcome of an interrupted run: the report so
// far with status interrupted, or nothing for raw output (any exit other
// than 0 or 1 means stdout can't be trusted).
func interrupted(inv *Invocation, res Result, intr env.Interrupt) []byte {
	if res.Report == nil {
		return nil
	}
	// The work after the signal was abandoned (every file access fails,
	// see cancelFS), so its findings are partial and not reported: the
	// run says only that it was interrupted.
	r := *res.Report
	r.Diagnostics = []diag.Diagnostic{
		diag.New(diag.CLIInterrupted, intr.Signal),
	}
	r.Signal = diag.SIGINT
	if intr.Exit == diag.ExitTerminated {
		r.Signal = diag.SIGTERM
	}
	return diag.Render(inv.Format(), &r)
}

// safeRun runs the command and turns a panic into an internal/crash
// diagnostic (exit 2) plus a crash file named by a hash (#20).
func safeRun(inv *Invocation) (res Result) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		reason := fmt.Sprint(v)
		file := writeCrash(inv, reason, debug.Stack())
		inv.Logf("internal error: %s; crash report written to %s",
			reason, file)
		res = inv.Fail(diag.New(diag.InternalCrash, reason, file))
	}()
	return inv.Command.Run(inv)
}

// writeCrash writes .gohard/crash/<hash>.txt under the current directory
// and returns its slash path. The name hashes the version, command and
// panic value, so the same crash always lands in the same file.
func writeCrash(inv *Invocation, reason string, stack []byte) string {
	sum := sha256.Sum256([]byte(Version + "\x00" + inv.Command.Name +
		"\x00" + reason))
	rel := path.Join(".gohard", "crash", hex.EncodeToString(sum[:8])+
		".txt")
	var b bytes.Buffer
	fmt.Fprintf(&b, "gohard %s crashed in gohard %s\npanic: %s\n\n%s",
		Version, inv.Command.Name, reason, stack)
	fs := inv.Env.FS
	if c, ok := fs.(cancelFS); ok {
		fs = c.FS // a crash report is written even after a signal
	}
	if err := fs.MkdirAll(filepath.Dir(filepath.FromSlash(rel)),
		0o755); err != nil {
		return "(not written: " + err.Error() + ")"
	}
	if err := fs.WriteFile(filepath.FromSlash(rel), b.Bytes(),
		0o644); err != nil {
		return "(not written: " + err.Error() + ")"
	}
	return rel
}

// writeOutput writes data to a temp file next to target and renames it
// over target, so target is never left half-written.
func writeOutput(fs env.FS, target string, data []byte) error {
	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}
	tmp, err := fs.WriteTemp(dir, "."+base+".", data)
	if err != nil {
		return err
	}
	if err := fs.Rename(tmp, target); err != nil {
		fs.Remove(tmp)
		return err
	}
	return nil
}
