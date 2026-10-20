package cli

import (
	"bytes"
	"encoding/json"
	"maps"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/repro"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/simrun"
	"github.com/connerohnesorge/gohard/internal/trace"
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
	out := lspSimJSON{Diagnostics: []simDiag{}}
	if len(runs) > 0 {
		o := runs[0]
		out.Testbench, out.Seed = o.Testbench, &o.Seed
		out.Status = o.Status.String()
		if o.Trace != nil {
			out.End = config.Time(o.Trace.Header.End).String()
			out.Trace = trace.FileName(o.Testbench, o.Seed)
			out.Repro = repro.FileName(o.Testbench, o.Seed)
		}
	}
	ds := diag.Normalize(r.Diagnostics)
	for _, d := range ds {
		out.Diagnostics = append(out.Diagnostics, simDiag{Path: d.Path,
			Line: d.Line, Col: d.Col, Severity: d.Severity.String(),
			Code: string(d.Code), Message: d.Message})
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		panic(err) // plain strings and numbers always encode
	}
	return Result{Stdout: b.Bytes(), Exit: r.Exit()}
}

// lspSimJSON is the stdout of gohard lsp sim; field order is the key
// order.
type lspSimJSON struct {
	Testbench   string    `json:"testbench"`
	Seed        *uint64   `json:"seed"`
	Status      string    `json:"status"`
	End         string    `json:"end"`
	Trace       string    `json:"trace"`
	Repro       string    `json:"repro"`
	Diagnostics []simDiag `json:"diagnostics"`
}

type simDiag struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}
