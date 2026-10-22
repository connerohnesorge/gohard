package cli

import (
	"bytes"
	"encoding/json"
	"maps"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/simrun"
)

func init() {
	// Listed with the other actions; lspAction runs it through
	// runLSPSim, the code path of gohard check --sim.
	server.RegisterAction(server.Action{Name: "sim",
		Method: "gohard/sim", Summary: "run one testbench (--tb NAME)",
		Params: func(string, server.Position, []string) (any, error) {
			return nil, nil
		}})
}

// runLSPSim is `gohard lsp sim --tb NAME [--seed N|random]` (#35): it
// runs one testbench exactly as gohard check --sim --tb NAME does
// (the same runCheck code path, so the same analysis, trace and repro
// files) and prints the outcome as one JSON object. The exit code
// follows #20 as under check --sim: 1 for an error diagnostic (or a
// warning with --strict), 2 for a tool error.
func runLSPSim(inv *Invocation) Result {
	if len(inv.Args) != 1 {
		return inv.usage("gohard lsp sim takes no arguments")
	}
	if !inv.IsSet("tb") {
		return inv.usage("gohard lsp sim needs --tb NAME")
	}
	sub := *inv
	sub.Command = lookup("check")
	sub.Args = nil
	sub.values = maps.Clone(inv.values)
	sub.values["sim"] = []string{"true"}
	var runs []simrun.Outcome
	sub.simRuns = &runs
	res := runCheck(&sub)
	r := res.Report
	r.Strict = inv.Bool("strict")
	var out server.SimReport
	if len(runs) > 0 {
		o := runs[0]
		end := ""
		if o.Trace != nil {
			end = config.Time(o.Trace.Header.End).String()
		}
		out = server.NewSimReport(o.Testbench, &o.Seed,
			o.Status.String(), end, r.Diagnostics)
		out.Asserts = simAsserts(o.Asserts)
	} else {
		out = server.NewSimReport("", nil, "", "", r.Diagnostics)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		panic(err) // plain strings and numbers always encode
	}
	return Result{Stdout: b.Bytes(), Exit: r.Exit()}
}

// simAsserts are a run's assert counts as the status JSON shows them.
func simAsserts(a sim.Asserts) *server.SimAsserts {
	return &server.SimAsserts{Note: a.Note, Warning: a.Warning,
		Error: a.Error, Failure: a.Failure}
}
