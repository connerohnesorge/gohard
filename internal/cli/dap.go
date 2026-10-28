package cli

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/dap"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/repro"
	"github.com/connerohnesorge/gohard/v2/internal/simdbg"
	"github.com/connerohnesorge/gohard/v2/internal/simrun"
)

func init() { lookup("dap").Run = runDAP }

// runDAP is `gohard dap` (#68): the simulation debugger over the Debug
// Adapter Protocol on stdio. It reads one frame at a time and writes
// the server's answers before it reads the next (internal/dap), so it
// needs no goroutine. It ends after a disconnect request or at the end
// of its input, with exit 0.
func runDAP(inv *Invocation) Result {
	if len(inv.Args) > 0 {
		return inv.usage("gohard dap takes no arguments")
	}
	srv := dap.New(dap.Options{Launch: func(a dap.LaunchArgs) (
		*dap.Launched, error,
	) {
		return dapLaunch(inv, a)
	}})
	fr := jsonrpc2.NewReader(inv.Env.Transport.Stdin, 0)
	for !srv.Done() {
		body, err := fr.ReadFrame()
		var skip *jsonrpc2.SkippedError
		switch {
		case errors.As(err, &skip):
			inv.Logf("dap: %s", err)
			continue
		case errors.Is(err, io.EOF):
			return Result{}
		case err != nil:
			inv.Logf("dap: %s", errReason(err))
			return Result{Exit: diag.ExitTool}
		}
		for _, m := range srv.Handle(body) {
			err := jsonrpc2.WriteFrame(inv.Env.Transport.Stdout, m)
			if err != nil {
				inv.Logf("dap: %s", errReason(err))
				return Result{Exit: diag.ExitTool}
			}
		}
	}
	return Result{}
}

// dapLaunch analyzes the project of the current directory as `gohard
// check --sim --tb TB --seed N` does and starts the debug session of
// that run: the same analysis, configuration, std.env environment and
// run directory, so the session runs exactly what check --sim runs.
// The analysis must have no error.
func dapLaunch(inv *Invocation, a dap.LaunchArgs) (*dap.Launched,
	error,
) {
	sub := *inv
	sub.Command = lookup("check")
	sub.Args = nil
	sub.values = map[string][]string{}
	var rep *diag.Report
	var in simInput
	sub.analyzed = func(r *diag.Report, i simInput) { rep, in = r, i }
	res := runCheck(&sub)
	if rep == nil {
		if res.Report == nil {
			return nil, errors.New("the project could not be analyzed")
		}
		return nil, errors.New(string(diag.RenderText(res.Report)))
	}
	rep.Diagnostics = append(rep.Diagnostics, applyEnv(&sub, &in)...)
	if in.res == nil || failedReport(rep) {
		return nil, errors.New("the design has errors:\n" +
			string(diag.RenderText(rep)))
	}
	seed, err := launchSeed(inv, a.Seed, in.cfg.Simulation.Seed)
	if err != nil {
		return nil, err
	}
	cands := simrun.Candidates(in.res, in.cfg.Simulation)
	want := a.Testbench
	if want == "" && len(cands) == 1 {
		want = cands[0]
	}
	tb, ok := findCandidate(cands, want)
	if !ok {
		list := strings.Join(cands, ", ")
		if list == "" {
			list = "none"
		}
		if want == "" {
			return nil, errors.New("launch needs a testbench, one of: " +
				list)
		}
		return nil, errors.New(diag.New(diag.SimUnknownTestbench, want,
			list).Message)
	}
	h, err := header(&sub, in, tb, seed, false)
	if err != nil {
		return nil, errors.New("hashing the sources: " + errReason(err))
	}
	o := simrun.Options{Result: in.res, Sim: in.cfg.Simulation,
		Testbench: tb, Seed: seed,
		Env: simrun.Env{ToolVersion: Version, Root: in.root,
			FS: inv.Env.FS, RunDir: repro.RunDir(h)},
		Cancelled: func() bool { return inv.Ctx.Err() != nil }}
	return &dap.Launched{Session: simdbg.Start(o), Root: in.root,
		Report: rep, Testbench: tb, Seed: seed}, nil
}

// failedReport reports an error or a tool failure: the run would not
// simulate.
func failedReport(r *diag.Report) bool {
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error || d.Class() == diag.Tool {
			return true
		}
	}
	return false
}

// launchSeed is the launch's seed: a number, a string as --seed takes
// it (random picks one through env.Rand and logs it), or def when
// absent.
func launchSeed(inv *Invocation, raw json.RawMessage, def uint64) (
	uint64, error,
) {
	if len(raw) == 0 || string(raw) == "null" {
		return def, nil
	}
	var n uint64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, errors.New("seed must be an unsigned integer or " +
			"random, not " + string(raw))
	}
	seed, random, err := env.ParseSeed(s, inv.Env.Rand)
	if err != nil {
		return 0, errors.New("seed must be an unsigned integer or " +
			"random, not " + strconv.Quote(s))
	}
	if random {
		inv.Logf("%s", simrun.SeedText(seed))
	}
	return seed, nil
}
