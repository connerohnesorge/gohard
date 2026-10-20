package server

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/repro"
	"github.com/connerohnesorge/gohard/internal/span"
	"github.com/connerohnesorge/gohard/internal/stdlib"
	"github.com/connerohnesorge/gohard/internal/trace"
	"github.com/connerohnesorge/gohard/internal/trace/waveform"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// Simulation in the editor (#25, #35, #36). The server owns the run
// state (which testbench is running, the seed, trace and repro of its
// last run); a Simulator computes the testbenches and runs one as a
// Task, so a run goes in bounded slices on the server loop like every
// other piece of work. Sim slices have the lowest priority and yield
// while an editor request or the diagnostics are waiting; a newer
// edit (ContentModified) or a $/cancelRequest stops a run at the next
// slice boundary.

// Simulator runs testbenches for the editor. Like an Analyzer, every
// method is a pure function of its arguments.
type Simulator interface {
	// Testbenches returns the testbench candidates of the snapshot
	// (#35), sorted by name.
	Testbenches(snap *Snapshot) Task[[]Testbench]
	// Simulate runs the testbench tb at seed on the text of snap, the
	// way gohard check --sim --tb tb --seed seed does.
	Simulate(snap *Snapshot, tb string, seed uint64) Task[SimResult]
}

// Testbench is one candidate of the snapshot.
type Testbench struct {
	// Name is the lower-case entity name.
	Name string
	// URI and Span locate the entity's name; URI is "" for a
	// simulation.testbenches entry that names no project entity.
	URI  string
	Span Span
	// Deps is a key of everything the run depends on (the text of the
	// files of the units it reaches and the simulation keys):
	// simulation.auto_run_on_save runs a testbench when it changes.
	Deps string
}

// SimResult is one finished run.
type SimResult struct {
	// Report is the result of gohard lsp sim on the same text: the
	// response of the run command.
	Report SimReport
	// Ran reports that the testbench was simulated (the design had no
	// errors); only then is there a run to record.
	Ran bool
	// Run are the run's own diagnostics (elaboration, simulation,
	// races), published with the source "gohard-sim".
	Run []diag.Diagnostic
	// Trace is the encoded native trace, nil when the run gave none.
	Trace []byte
	// Write reports that the trace and repro file are written (no tool
	// error), as gohard check --sim does.
	Write bool
}

// SimReport is the JSON object of gohard lsp sim (#35); field order
// is the key order. The editor's run command answers with it.
type SimReport struct {
	Testbench   string    `json:"testbench"`
	Seed        *uint64   `json:"seed"`
	Status      string    `json:"status"`
	End         string    `json:"end"`
	Trace       string    `json:"trace"`
	Repro       string    `json:"repro"`
	Diagnostics []SimDiag `json:"diagnostics"`
}

// SimDiag is one diagnostic of a SimReport.
type SimDiag struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// NewSimReport builds a report. seed is nil when nothing ran; end is
// the trace's end time, "" when there is no trace (then the trace and
// repro names are empty too). ds are normalized.
func NewSimReport(tb string, seed *uint64, status, end string,
	ds []diag.Diagnostic,
) SimReport {
	r := SimReport{Testbench: tb, Seed: seed, Status: status, End: end,
		Diagnostics: []SimDiag{}}
	if end != "" && seed != nil {
		r.Trace = trace.FileName(tb, *seed)
		r.Repro = repro.FileName(tb, *seed)
	}
	for _, d := range diag.Normalize(ds) {
		r.Diagnostics = append(r.Diagnostics, SimDiag{Path: d.Path,
			Line: d.Line, Col: d.Col, Severity: d.Severity.String(),
			Code: string(d.Code), Message: d.Message})
	}
	return r
}

// The commands of the code lenses and code actions.
const (
	CmdRun      = "gohard.runTestbench"
	CmdRepro    = "gohard.copyRepro"
	CmdWaveform = "gohard.openWaveform"
)

// SimCommands are the simulation commands, sorted.
var SimCommands = []string{CmdRepro, CmdWaveform, CmdRun}

// Code lens and code action titles (#25, #36).
const (
	titleRunning  = "◉ Running…"
	titleRepro    = "Copy repro"
	titleWaveform = "Open waveform"
)

// RunTitle is the lens title of a testbench that has not run.
func RunTitle(tb string) string { return "▶ Run " + tb }

// RerunTitle is the lens title after a run at seed.
func RerunTitle(seed uint64) string {
	return "▶ Re-run · seed " + strconv.FormatUint(seed, 10)
}

// AutoRunDelay is the debounce of simulation.auto_run_on_save in
// virtual time: saves within it start one round of runs.
const AutoRunDelay = 300 * time.Millisecond

// PrioritySim is a simulation slice: it runs after everything else
// due at the same time.
const PrioritySim = PrioritySlice + 1

// SimArgs is the argument object of the simulation commands. A
// testbench is named by Testbench, else found at Position in URI (the
// last candidate entity starting before it, else the first in URI).
type SimArgs struct {
	Testbench string    `json:"testbench,omitempty"`
	URI       string    `json:"uri,omitempty"`
	Position  *Position `json:"position,omitempty"`
	Seed      *uint64   `json:"seed,omitempty"`
	Repro     string    `json:"repro,omitempty"`
}

// simJob marks work that simulates: its slices have PrioritySim.
type simJob struct {
	tb   string // the testbench, once known
	auto bool   // started by simulation.auto_run_on_save
}

// simRun is the last finished run of a testbench.
type simRun struct {
	seed  uint64
	trace string // root-relative slash path, "" without a trace
	repro string
	deps  string
	uri   string
	diags []diag.Diagnostic
}

// Results of simulation work, handled by simFinished.
type (
	simDone struct {
		tb, uri, deps string
		res           SimResult
		open          bool
	}
	simOpen struct{ tb string }
	simPlan struct{ benches []Testbench }
)

// Effects and their results.
type (
	// WriteRun asks the shell to write a run's trace and repro file
	// under Root through env.FS and to push RunWritten.
	WriteRun struct {
		Root   string
		Trace  string // root-relative slash path; "" for none
		Data   []byte
		Header repro.Header
	}
	// OpenWaveform asks the shell to run waveform.Open on Trace (an
	// OS path) through env.FS and env.Executor and to push
	// WaveformOpened for the request ID.
	OpenWaveform struct {
		ID    jsonrpc2.ID
		Root  string
		Trace string
	}
	// RunWritten is the result of WriteRun; Err is "" on success.
	RunWritten struct{ Err string }
	// WaveformOpened is the result of OpenWaveform.
	WaveformOpened struct {
		ID     jsonrpc2.ID
		Root   string
		Result waveform.Result
		Err    string
	}
	// AutoRun fires the auto_run_on_save debounce of save Gen.
	AutoRun struct{ Gen uint64 }
)

func (WriteRun) effect()     {}
func (OpenWaveform) effect() {}
func (RunWritten) body()     {}
func (WaveformOpened) body() {}
func (AutoRun) body()        {}
func (w work) pri() Priority {
	if w.sim != nil {
		return PrioritySim
	}
	return PrioritySlice
}

// Chain runs t, then the task f returns for its result.
func Chain[A, B any](t Task[A], f func(A) Task[B]) Task[B] {
	return TaskFunc[B](func(budget int) Slice[B] {
		sl := t.Run(budget)
		if sl.Done || sl.Next == nil {
			return Slice[B]{Next: f(sl.Value), Report: sl.Report}
		}
		return Slice[B]{Next: Chain(sl.Next, f), Report: sl.Report}
	})
}

// mapTask maps the result of t.
func mapTask[A, B any](t Task[A], f func(A) B) Task[B] {
	return TaskFunc[B](func(budget int) Slice[B] {
		sl := t.Run(budget)
		if sl.Done || sl.Next == nil {
			return Slice[B]{Done: true, Value: f(sl.Value),
				Report: sl.Report}
		}
		return Slice[B]{Next: mapTask(sl.Next, f), Report: sl.Report}
	})
}

// features are the analyzer's features plus the simulator's.
func (s *State) features() Features {
	f := s.an.Features()
	if s.opts.Simulator == nil {
		return f
	}
	f.Set |= FeatureCodeLens | FeatureCodeActions | FeatureCommands
	f.Commands = append(slices.Clone(f.Commands), SimCommands...)
	slices.Sort(f.Commands)
	f.Commands = slices.Compact(f.Commands)
	return f
}

// running reports whether a run of tb is in progress.
func (s *State) running(tb string) bool {
	for _, id := range sortedKeys(s.work) {
		if j := s.work[id].sim; j != nil && j.tb == tb {
			return true
		}
	}
	return false
}

// outranked reports whether work other than simulation is waiting:
// then a sim slice yields (editor requests outrank simulation).
func (s *State) outranked(id uint64) bool {
	for _, k := range sortedKeys(s.work) {
		w := s.work[k]
		if k != id && w.sim == nil && w.parked == nil {
			return true
		}
	}
	return false
}

func (s *State) refreshLenses(o *out) {
	if !s.lensRefresh || s.phase != phaseRunning {
		return
	}
	s.nextOut++
	o.send(&jsonrpc2.Request{ID: jsonrpc2.IntID(s.nextOut),
		Method: "workspace/codeLens/refresh"})
}

// pick finds the testbench a SimArgs names.
func pick(snap *Snapshot, enc span.Encoding, bs []Testbench,
	a SimArgs,
) (Testbench, bool) {
	if a.Testbench != "" {
		n := strings.ToLower(a.Testbench)
		for _, b := range bs {
			if b.Name == n {
				return b, true
			}
		}
		if _, after, ok := strings.Cut(n, "."); ok {
			for _, b := range bs {
				if b.Name == after {
					return b, true
				}
			}
		}
		return Testbench{}, false
	}
	d, ok := snap.Document(a.URI)
	if !ok {
		return Testbench{}, false
	}
	off := -1
	if a.Position != nil {
		off = offset(d.index, *a.Position, enc)
	}
	var best Testbench
	found := false
	for _, b := range bs {
		if docKey(b.URI) != d.key {
			continue
		}
		if !found || b.Span.Start <= off && b.Span.Start > best.Span.Start {
			best, found = b, true
		}
	}
	return best, found
}

func simArgs(args []json.RawMessage) SimArgs {
	var a SimArgs
	if len(args) > 0 {
		_ = json.Unmarshal(args[0], &a)
	}
	return a
}

// simCommand returns the runner of a simulation command.
func (s *State) simCommand(cmd string, args []json.RawMessage,
) (runner, *simJob, bool) {
	sim := s.opts.Simulator
	if sim == nil || !slices.Contains(SimCommands, cmd) {
		return nil, nil, false
	}
	a := simArgs(args)
	snap, runs, enc := s.snap, s.runs, s.enc
	ident := func(v any) any { return v }
	if cmd == CmdRepro && a.Repro != "" {
		return runTask(Done[any](repro.CopyCommand(a.Repro)), ident),
			nil, true
	}
	missing := func() any {
		what := a.Testbench
		if what == "" {
			what = "at the position in " + a.URI
		}
		return failure{"no testbench " + what}
	}
	job := &simJob{tb: strings.ToLower(a.Testbench)}
	t := Chain(sim.Testbenches(snap), func(bs []Testbench) Task[any] {
		b, ok := pick(snap, enc, bs, a)
		if !ok {
			return Done(missing())
		}
		seed := snap.Config.Simulation.Seed
		if a.Seed != nil {
			seed = *a.Seed
		}
		last, ran := runs[b.Name]
		switch cmd {
		case CmdRepro:
			name := repro.FileName(b.Name, seed)
			if ran && a.Seed == nil {
				name = last.repro
			}
			return Done[any](repro.CopyCommand(name))
		case CmdWaveform:
			if ran && last.trace != "" && a.Seed == nil {
				return Done[any](simOpen{b.Name})
			}
		}
		open := cmd == CmdWaveform
		return mapTask(sim.Simulate(snap, b.Name, seed),
			func(r SimResult) any {
				return simDone{tb: b.Name, uri: b.URI, deps: b.Deps,
					res: r, open: open}
			})
	})
	if cmd == CmdRepro {
		job = nil
	}
	return runTask(t, ident), job, true
}

// simFinished handles the result of finished simulation work; it
// reports false for other results.
func (s *State) simFinished(o *out, w work, res any) bool {
	switch r := res.(type) {
	case simDone:
		s.simDone(o, w, r)
	case simOpen:
		s.openWaveform(o, w, s.runs[r.tb])
	case simPlan:
		s.autoStart(o, r.benches)
	default:
		return false
	}
	return true
}

func (s *State) simDone(o *out, w work, d simDone) {
	rep := d.res.Report
	if !d.res.Ran {
		if w.isReq {
			if d.open {
				s.fail(o, w.reqID, jsonrpc2.CodeRequestFailed,
					d.tb+" did not run: the design has errors")
				return
			}
			s.respond(o, w.reqID, rep)
		}
		return
	}
	seed := uint64(0)
	if rep.Seed != nil {
		seed = *rep.Seed
	}
	run := simRun{seed: seed, deps: d.deps, uri: d.uri,
		diags: d.res.Run, repro: repro.FileName(d.tb, seed)}
	if d.res.Write && s.root != "" {
		if d.res.Trace != nil {
			run.trace = rep.Trace
		}
		o.do(WriteRun{Root: s.root, Trace: run.trace, Data: d.res.Trace,
			Header: s.reproHeader(d.tb, seed)})
	}
	s.runs = cloneSet(s.runs, d.tb, run)
	s.log(o, LogInfo, "simulated "+d.tb+" (seed "+
		strconv.FormatUint(seed, 10)+"): "+rep.Status)
	s.publishAll(o)
	s.refreshLenses(o)
	switch {
	case !w.isReq:
	case d.open:
		s.openWaveform(o, w, run)
	default:
		s.respond(o, w.reqID, rep)
	}
}

// openWaveform exports the trace of run to FST and opens it (#36), in the
// shell; the response follows WaveformOpened.
func (s *State) openWaveform(o *out, w work, run simRun) {
	if run.trace == "" || s.root == "" {
		s.fail(o, w.reqID, jsonrpc2.CodeRequestFailed,
			"the run left no trace to open")
		return
	}
	o.do(OpenWaveform{ID: w.reqID, Root: s.root,
		Trace: filepath.Join(s.root, filepath.FromSlash(run.trace))})
}

// waveformOpened answers the Open waveform request.
func (s *State) waveformOpened(o *out, e WaveformOpened) {
	if s.phase == phaseExited {
		return
	}
	if e.Err != "" {
		s.fail(o, e.ID, jsonrpc2.CodeRequestFailed,
			"open waveform: "+e.Err)
		return
	}
	r := e.Result
	if rel, err := filepath.Rel(e.Root, r.FST); err == nil {
		r.FST = filepath.ToSlash(rel)
	}
	msg := r.Message()
	o.notify("window/showMessage", struct {
		Type    int    `json:"type"`
		Message string `json:"message"`
	}{LogInfo, msg})
	s.respond(o, e.ID, struct {
		FST     string `json:"fst"`
		Viewer  string `json:"viewer"`
		Message string `json:"message"`
	}{r.FST, r.Viewer, msg})
}

// reproHeader is the repro header of a run (#29) on the snapshot's
// text: the hashes are of the text that was simulated.
func (s *State) reproHeader(tb string, seed uint64) repro.Header {
	c := s.snap.Config
	h := repro.Header{Version: s.opts.Version, Commit: s.opts.Commit,
		IEEE: stdlib.IEEE(), Go: s.opts.Go, Standard: c.Standard,
		Testbench: tb, Seed: seed, Defines: map[string]string{},
		Models: map[string]string{}}
	sm := c.Simulation
	h.Budgets.Time = sm.TimeBudget.String()
	h.Budgets.Delta, h.Budgets.Step = sm.DeltaBudget, sm.StepBudget
	for _, d := range c.Defines {
		h.Defines[d.Name] = d.Value
	}
	for _, m := range sm.Models {
		h.Models[m.Unit] = m.Path
	}
	seen := map[string]bool{}
	for _, d := range s.snap.Documents() {
		p := d.Rel
		if p == "" || p == ".." || strings.HasPrefix(p, "../") ||
			seen[p] || d.Open && !workspace.IsVHDL(d.Path) &&
			d.LanguageID != "vhdl" {
			continue
		}
		seen[p] = true
		h.Sources = append(h.Sources, repro.File{Path: p,
			SHA256: repro.Hash(d.Text())})
	}
	slices.SortFunc(h.Sources, func(a, b repro.File) int {
		return strings.Compare(a.Path, b.Path)
	})
	if s.file != nil && s.file.Present {
		h.Config = &repro.File{Path: repro.ConfigFile,
			SHA256: repro.Hash(s.file.Source)}
	}
	return h
}

// didSaveSim starts the auto_run_on_save debounce for a saved VHDL
// file.
func (s *State) didSaveSim(path string) {
	if s.opts.Simulator == nil || !s.loaded ||
		!s.snap.Config.Simulation.AutoRunOnSave || !workspace.IsVHDL(path) {
		return
	}
	s.saves++
	*s = s.PushAt(s.now+AutoRunDelay, AutoRun{Gen: s.saves},
		PrioritySlice)
}

// autoRun plans the runs of the latest save once the debounce passed.
func (s *State) autoRun(gen uint64) {
	if gen != s.saves || s.phase != phaseRunning || !s.loaded {
		return
	}
	t := mapTask(s.opts.Simulator.Testbenches(s.snap),
		func(bs []Testbench) any { return simPlan{bs} })
	s.startWork(work{method: "gohard/autoRun", sim: &simJob{auto: true},
		run: runTask(t, func(v any) any { return v })})
}

// autoStart runs every candidate whose dependencies changed since its
// last run (or that never ran), at simulation.seed.
func (s *State) autoStart(o *out, bs []Testbench) {
	sim, snap := s.opts.Simulator, s.snap
	for _, b := range bs {
		if r, ok := s.runs[b.Name]; ok && r.deps == b.Deps ||
			s.running(b.Name) {
			continue
		}
		b := b
		t := mapTask(sim.Simulate(snap, b.Name,
			snap.Config.Simulation.Seed), func(r SimResult) any {
			return simDone{tb: b.Name, uri: b.URI, deps: b.Deps, res: r}
		})
		s.startWork(work{method: "gohard/sim",
			sim: &simJob{tb: b.Name, auto: true},
			run: runTask(t, func(v any) any { return v })})
	}
	s.refreshLenses(o)
}

// decorate adds the simulation lenses and code actions to the
// analyzer's result.
func (s *State) decorate(method string, q Query, run runner) runner {
	sim := s.opts.Simulator
	if sim == nil || method != "textDocument/codeLens" &&
		method != "textDocument/codeAction" {
		return run
	}
	runs := s.runs
	busy := map[string]bool{}
	for _, id := range sortedKeys(s.work) {
		if j := s.work[id].sim; j != nil && j.tb != "" {
			busy[j.tb] = true
		}
	}
	c := conv{snap: q.Snap, enc: s.enc}
	lens := method == "textDocument/codeLens"
	return thenRunner{run, func(res any) runner {
		if _, ok := res.(failure); ok {
			return runTask(Done(res), func(v any) any { return v })
		}
		var items []json.RawMessage
		_ = json.Unmarshal(marshal(res), &items)
		t := mapTask(sim.Testbenches(q.Snap), func(bs []Testbench) any {
			for _, b := range bs {
				if b.URI == "" || docKey(b.URI) != docKey(q.URI) {
					continue
				}
				r := c.rng(q.URI, b.Span)
				last, ran := runs[b.Name]
				if lens {
					items = append(items, simLenses(r, b, last, ran,
						busy[b.Name])...)
				} else {
					items = append(items, simActions(b, last, ran)...)
				}
			}
			if items == nil {
				items = []json.RawMessage{}
			}
			return items
		})
		return runTask(t, func(v any) any { return v })
	}}
}

type lspCommand struct {
	Title     string    `json:"title"`
	Command   string    `json:"command"`
	Arguments []SimArgs `json:"arguments"`
}

func simLenses(r Range, b Testbench, last simRun, ran, busy bool,
) []json.RawMessage {
	type lens struct {
		Range   Range      `json:"range"`
		Command lspCommand `json:"command"`
	}
	args := SimArgs{Testbench: b.Name, URI: b.URI}
	title := RunTitle(b.Name)
	switch {
	case busy:
		title = titleRunning
	case ran:
		title = RerunTitle(last.seed)
		seed := last.seed
		args.Seed = &seed
	}
	out := []json.RawMessage{marshal(lens{r, lspCommand{title, CmdRun,
		[]SimArgs{args}}})}
	if ran && !busy {
		out = append(out, marshal(lens{r, lspCommand{titleRepro,
			CmdRepro, []SimArgs{{Testbench: b.Name,
				Repro: last.repro}}}}))
	}
	return out
}

func simActions(b Testbench, last simRun, ran bool) []json.RawMessage {
	type action struct {
		Title   string     `json:"title"`
		Command lspCommand `json:"command"`
	}
	a := SimArgs{Testbench: b.Name, URI: b.URI}
	title := titleWaveform + " (" + b.Name + ")"
	out := []json.RawMessage{marshal(action{title,
		lspCommand{title, CmdWaveform, []SimArgs{a}}})}
	if ran {
		t := titleRepro + " (" + b.Name + ")"
		out = append(out, marshal(action{t, lspCommand{t, CmdRepro,
			[]SimArgs{{Testbench: b.Name, Repro: last.repro}}}}))
	}
	return out
}

// thenRunner runs r, then the runner f builds from its result.
type thenRunner struct {
	r runner
	f func(any) runner
}

func (t thenRunner) step(budget int) (runner, any, bool, *Report) {
	next, res, done, rep := t.r.step(budget)
	if !done {
		return thenRunner{next, t.f}, nil, false, rep
	}
	return t.f(res), nil, false, rep
}

// simDiagnostics are the gohard-sim diagnostics of the last runs, by
// URI, converted for publishing.
func (s *State) simDiagnostics() map[string][]lspDiagnostic {
	byRel := map[string]*Document{}
	for _, d := range s.snap.Documents() {
		if d.Rel != "" && byRel[d.Rel] == nil {
			byRel[d.Rel] = d
		}
	}
	out := map[string][]lspDiagnostic{}
	for _, tb := range sortedKeys(s.runs) {
		r := s.runs[tb]
		for _, dg := range r.diags {
			d := byRel[dg.Path]
			if dg.Path == "" {
				d, _ = s.snap.Document(r.uri)
				dg.Line = 0
			}
			if d == nil {
				continue
			}
			l := s.toLSPDiagnostic(d.index, dg)
			l.Source = "gohard-sim"
			out[d.URI] = append(out[d.URI], l)
		}
	}
	return out
}

// simAction is a `gohard lsp` action that sends one simulation command
// for the testbench at --at (#26, #36).
func simAction(name, cmd, summary, synopsis string) Action {
	return Action{Name: name, Method: "workspace/executeCommand",
		Summary: summary, Args: synopsis,
		Params: func(uri string, pos Position, args []string) (any,
			error,
		) {
			a := SimArgs{URI: uri, Position: &pos}
			switch {
			case synopsis != "" && len(args) == 1:
				seed, err := strconv.ParseUint(args[0], 10, 64)
				if err != nil {
					return nil, errors.New("gohard lsp " + name +
						": the seed must be an unsigned integer, not " +
						strconv.Quote(args[0]))
				}
				a.Seed = &seed
			case len(args) > 0 && synopsis == "":
				return nil, noArgs(name, args)
			case len(args) > 1:
				return nil, errors.New("gohard lsp " + name +
					" takes at most one argument, the seed")
			}
			return struct {
				Command   string    `json:"command"`
				Arguments []SimArgs `json:"arguments"`
			}{cmd, []SimArgs{a}}, nil
		}}
}

func init() {
	RegisterAction(simAction("run", CmdRun,
		"run the testbench at the position, as its code lens does",
		"[SEED]"))
	RegisterAction(simAction("waveform", CmdWaveform,
		"Open waveform: run the testbench, export FST, open a viewer",
		"[SEED]"))
	RegisterAction(simAction("repro", CmdRepro,
		"Copy repro: the gohard check --repro command of the run", ""))
}
