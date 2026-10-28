// Package dap is the Debug Adapter Protocol side of the simulation
// debugger (#68): `gohard dap` serves a simdbg.Session to an editor
// (Neovim's nvim-dap first) over stdio.
//
// Server is a state machine: Handle takes the body of one message
// from the client and returns the bodies to send back, in order; the
// shell (internal/cli) reads and writes the frames, which are the
// base protocol of LSP (jsonrpc2.ReadFrame, jsonrpc2.WriteFrame). The
// adapter handles one request at a time: a continue or step request
// is answered, then the run moves, then the stopped event (or the end
// of the run: output, exited and terminated) follows before the next
// request is read. A move cannot be paused; it ends at a stop or at
// the end of the run, which the run's budgets bound (P7).
//
// Requests: initialize, launch (testbench, seed, stopOnEntry),
// setBreakpoints, setExceptionBreakpoints (no filters),
// configurationDone, threads (one per process the interpreter runs),
// stackTrace, scopes (Locals and Signals of each frame), variables,
// source (the text of a bundled std/ieee file a frame is in),
// continue, next, stepIn, stepOut, stepBack, reverseContinue and
// disconnect. Any other request fails with "not supported".
package dap

import (
	"encoding/json"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/elab"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/simdbg"
	"github.com/connerohnesorge/gohard/v2/internal/stdlib"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// MaxValue is the longest value text a variables response carries;
// a longer one is cut there and ends in "...".
const MaxValue = 4096

// LaunchArgs are the arguments of a launch request gohard reads.
type LaunchArgs struct {
	// Testbench is the testbench to run, as `--tb`; empty means the
	// project's only candidate.
	Testbench string `json:"testbench"`
	// Seed is the seed, a number or a string as `--seed` takes it;
	// empty means simulation.seed.
	Seed json.RawMessage `json:"seed"`
	// StopOnEntry stops at the run's first statement.
	StopOnEntry bool `json:"stopOnEntry"`
}

// Launched is a started session.
type Launched struct {
	Session *simdbg.Session
	// Root is the absolute project root: the paths of the session are
	// relative to it.
	Root string
	// Report holds what the analysis reported; the run's own
	// diagnostics are added to it when the run ends.
	Report *diag.Report
	// Testbench and Seed name the run.
	Testbench string
	Seed      uint64
}

// Options configure a Server.
type Options struct {
	// Launch analyzes the project and starts the session a launch
	// request asks for; an error fails the request with its text.
	Launch func(LaunchArgs) (*Launched, error)
}

// Server is one debug session's protocol state.
type Server struct {
	o   Options
	seq int
	l   *Launched
	// lines1 and cols1 are the client's linesStartAt1 and
	// columnsStartAt1.
	lines1, cols1 bool
	// entry is the launch's stopOnEntry; started is set once
	// configurationDone started the run.
	entry, started bool
	done           bool
	// stop is the current stop; base numbers its frames: the frames
	// of thread i have the IDs base[i]+1 ... base[i+1].
	stop *simdbg.Stop
	base []int
}

// New returns a server.
func New(o Options) *Server {
	return &Server{o: o, lines1: true, cols1: true}
}

// Done reports that the client disconnected: the shell exits.
func (s *Server) Done() bool { return s.done }

type request struct {
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Command   string          `json:"command"`
	Arguments json.RawMessage `json:"arguments"`
}

type response struct {
	Seq        int    `json:"seq"`
	Type       string `json:"type"`
	RequestSeq int    `json:"request_seq"`
	Success    bool   `json:"success"`
	Command    string `json:"command"`
	Message    string `json:"message,omitempty"`
	Body       any    `json:"body,omitempty"`
}

type event struct {
	Seq   int    `json:"seq"`
	Type  string `json:"type"`
	Event string `json:"event"`
	Body  any    `json:"body,omitempty"`
}

// out collects the messages of one Handle call.
type out struct {
	s    *Server
	msgs [][]byte
}

func (o *out) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // plain structs, strings and numbers always encode
	}
	o.msgs = append(o.msgs, b)
}

func (o *out) reply(req request, body any) {
	o.s.seq++
	o.send(response{Seq: o.s.seq, Type: "response",
		RequestSeq: req.Seq, Success: true, Command: req.Command,
		Body: body})
}

func (o *out) fail(req request, msg string) {
	o.s.seq++
	o.send(response{Seq: o.s.seq, Type: "response",
		RequestSeq: req.Seq, Command: req.Command, Message: msg})
}

func (o *out) event(name string, body any) {
	o.s.seq++
	o.send(event{Seq: o.s.seq, Type: "event", Event: name, Body: body})
}

// Handle handles the body of one message and returns the bodies of
// the messages to send, in order. Responses and events of the client
// are ignored; a body that is not a request gets no answer.
func (s *Server) Handle(body []byte) [][]byte {
	o := &out{s: s}
	var req request
	if err := json.Unmarshal(body, &req); err != nil ||
		req.Type != "request" {
		return nil
	}
	if s.done {
		o.fail(req, "the session is over")
		return o.msgs
	}
	switch req.Command {
	case "initialize":
		s.initialize(o, req)
	case "launch":
		s.launch(o, req)
	case "setBreakpoints":
		s.setBreakpoints(o, req)
	case "setExceptionBreakpoints":
		o.reply(req, map[string]any{"breakpoints": []any{}})
	case "configurationDone":
		s.configurationDone(o, req)
	case "threads":
		s.threads(o, req)
	case "stackTrace":
		s.stackTrace(o, req)
	case "scopes":
		s.scopes(o, req)
	case "variables":
		s.variables(o, req)
	case "source":
		s.source(o, req)
	case "continue":
		s.move(o, req, simdbg.Continue,
			map[string]any{"allThreadsContinued": true})
	case "next":
		s.move(o, req, simdbg.Next, nil)
	case "stepIn":
		s.move(o, req, simdbg.StepIn, nil)
	case "stepOut":
		s.move(o, req, simdbg.StepOut, nil)
	case "stepBack":
		s.move(o, req, simdbg.StepBack, nil)
	case "reverseContinue":
		s.move(o, req, simdbg.ReverseContinue, nil)
	case "disconnect", "terminate":
		s.done = true
		o.reply(req, nil)
	default:
		o.fail(req, req.Command+" is not supported by gohard dap")
	}
	return o.msgs
}

func (s *Server) initialize(o *out, req request) {
	var a struct {
		LinesStartAt1   *bool `json:"linesStartAt1"`
		ColumnsStartAt1 *bool `json:"columnsStartAt1"`
	}
	_ = json.Unmarshal(req.Arguments, &a)
	if a.LinesStartAt1 != nil {
		s.lines1 = *a.LinesStartAt1
	}
	if a.ColumnsStartAt1 != nil {
		s.cols1 = *a.ColumnsStartAt1
	}
	o.reply(req, map[string]any{
		"supportsConfigurationDoneRequest": true,
		"supportsStepBack":                 true,
	})
}

// launch starts the session; the initialized event follows its
// response, so the client sends its breakpoints to a session that can
// check them.
func (s *Server) launch(o *out, req request) {
	if s.l != nil {
		o.fail(req, "the session is already launched")
		return
	}
	var a LaunchArgs
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &a); err != nil {
			o.fail(req, "launch arguments: "+err.Error())
			return
		}
	}
	l, err := s.o.Launch(a)
	if err != nil {
		o.fail(req, err.Error())
		return
	}
	s.l, s.entry = l, a.StopOnEntry
	o.reply(req, nil)
	o.output("gohard: debugging " + l.Testbench + " (seed " +
		strconv.FormatUint(l.Seed, 10) + ")\n")
	o.event("initialized", nil)
}

func (o *out) output(text string) {
	o.event("output", map[string]any{"category": "console",
		"output": text})
}

type source struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	// SourceReference names a bundled std/ieee file (bundledSource).
	SourceReference int `json:"sourceReference,omitempty"`
}

type breakpoint struct {
	Verified bool    `json:"verified"`
	Line     int     `json:"line"`
	Source   *source `json:"source,omitempty"`
	Message  string  `json:"message,omitempty"`
}

func (s *Server) setBreakpoints(o *out, req request) {
	var a struct {
		Source      source `json:"source"`
		Breakpoints []struct {
			Line int `json:"line"`
		} `json:"breakpoints"`
		Lines []int `json:"lines"`
	}
	if err := json.Unmarshal(req.Arguments, &a); err != nil {
		o.fail(req, "setBreakpoints arguments: "+err.Error())
		return
	}
	lines := a.Lines
	if a.Breakpoints != nil {
		lines = nil
		for _, b := range a.Breakpoints {
			lines = append(lines, b.Line)
		}
	}
	ls := make([]int, len(lines))
	for i, l := range lines {
		ls[i] = s.lineIn(l)
	}
	out := make([]breakpoint, len(lines))
	rel, ok := s.rel(a.Source.Path)
	var verified []bool
	switch {
	case s.l == nil:
		ok = false
	case ok:
		verified = s.l.Session.SetBreakpoints(rel, ls)
	}
	for i, l := range lines {
		out[i] = breakpoint{Line: l, Source: &a.Source}
		switch {
		case s.l == nil:
			out[i].Message = "no testbench launched"
		case !ok:
			out[i].Message = "not a file of the project"
		case verified[i]:
			out[i].Verified = true
		default:
			out[i].Message = "no statement the debugger stops at " +
				"starts on this line"
		}
	}
	o.reply(req, map[string]any{"breakpoints": out})
}

// rel is p as a project path, slash-separated and relative to the
// root; ok is false outside the root.
func (s *Server) rel(p string) (string, bool) {
	if s.l == nil || p == "" {
		return "", false
	}
	if env.IsRooted(p) {
		r, err := filepath.Rel(s.l.Root, p)
		if err != nil {
			return "", false
		}
		p = r
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", false
	}
	return p, true
}

// lineIn and lineOut convert lines between the client and GoHard
// (one-based); colOut converts a column.
func (s *Server) lineIn(l int) int {
	if s.lines1 {
		return l
	}
	return l + 1
}

func (s *Server) lineOut(l int) int {
	if s.lines1 {
		return l
	}
	return l - 1
}

func (s *Server) colOut(c int) int {
	if s.cols1 {
		return c
	}
	return c - 1
}

// configurationDone starts the run: to the first statement with
// stopOnEntry, else to the first breakpoint.
func (s *Server) configurationDone(o *out, req request) {
	o.reply(req, nil)
	if s.l == nil || s.started {
		return
	}
	s.started = true
	if s.entry {
		s.result(o, s.l.Session.Entry())
		return
	}
	s.result(o, s.l.Session.Move(simdbg.Continue))
}

// move answers req, then runs m and reports where it ended.
func (s *Server) move(o *out, req request, m simdbg.Move, body any) {
	if s.l == nil {
		o.fail(req, "no testbench launched")
		return
	}
	if _, ended := s.l.Session.Outcome(); ended {
		o.fail(req, "the run has ended")
		return
	}
	o.reply(req, body)
	s.result(o, s.l.Session.Move(m))
}

// result reports where a move ended: a stopped event, or the end of
// the run (its diagnostics as output, exited and terminated). A
// cancelled move reports nothing: the shell is exiting.
func (s *Server) result(o *out, r simdbg.Result) {
	s.setStop(r.Stop)
	switch {
	case r.Cancelled:
	case r.Stop != nil:
		o.event("stopped", map[string]any{"reason": r.Reason,
			"description": stopText(r.Stop),
			"threadId":    r.Stop.Process + 1, "allThreadsStopped": true})
	case r.Ended:
		s.end(o)
	}
}

// stopText describes where the run stopped: "at 30 ns, delta 0,
// statement 120".
func stopText(st *simdbg.Stop) string {
	return "at " + config.Time(st.Time).String() + ", delta " +
		strconv.FormatUint(uint64(st.Delta), 10) + ", statement " +
		strconv.FormatInt(st.Event, 10)
}

// end reports the end of the run: the report of `gohard check --sim`
// for this testbench as output, the exit code it would have, and the
// end of the session.
func (s *Server) end(o *out) {
	rep := *s.l.Report
	status := "not run"
	if out, ok := s.l.Session.Outcome(); ok {
		rep.Diagnostics = append(slices.Clone(rep.Diagnostics),
			out.Diagnostics...)
		if out.Status.Done() {
			status = out.Status.String()
		}
		if out.Trace != nil {
			status += " at " +
				config.Time(out.Trace.Header.End).String()
		}
	}
	o.output("gohard: simulated " + s.l.Testbench + " (seed " +
		strconv.FormatUint(s.l.Seed, 10) + "): " + status + "\n")
	o.output(string(diag.RenderText(&rep)))
	o.event("exited", map[string]any{"exitCode": rep.Exit()})
	o.event("terminated", nil)
}

// setStop makes st the current stop and numbers its frames.
func (s *Server) setStop(st *simdbg.Stop) {
	s.stop, s.base = st, nil
	if st == nil {
		return
	}
	n := 0
	for _, th := range st.Threads {
		s.base = append(s.base, n)
		n += len(th.Frames)
	}
	s.base = append(s.base, n)
}

// frame returns the thread and the frame of frame ID id.
func (s *Server) frame(id int) (*simdbg.Thread, *elab.Frame) {
	if s.stop == nil {
		return nil, nil
	}
	for i := range s.stop.Threads {
		if k := id - 1 - s.base[i]; k >= 0 && id-1 < s.base[i+1] {
			th := &s.stop.Threads[i]
			return th, &th.Frames[k]
		}
	}
	return nil, nil
}

func (s *Server) threads(o *out, req request) {
	ts := []map[string]any{}
	if s.stop != nil {
		for _, th := range s.stop.Threads {
			ts = append(ts, map[string]any{"id": th.Process + 1,
				"name": th.Path})
		}
	}
	o.reply(req, map[string]any{"threads": ts})
}

type stackFrame struct {
	ID     int     `json:"id"`
	Name   string  `json:"name"`
	Source *source `json:"source,omitempty"`
	Line   int     `json:"line"`
	Column int     `json:"column"`
}

func (s *Server) stackTrace(o *out, req request) {
	var a struct {
		ThreadID   int `json:"threadId"`
		StartFrame int `json:"startFrame"`
		Levels     int `json:"levels"`
	}
	_ = json.Unmarshal(req.Arguments, &a)
	if s.stop == nil {
		o.fail(req, "the run is not stopped")
		return
	}
	th := s.stop.Thread(a.ThreadID - 1)
	if th == nil {
		o.fail(req, "no thread "+strconv.Itoa(a.ThreadID))
		return
	}
	i := slices.IndexFunc(s.stop.Threads, func(t simdbg.Thread) bool {
		return t.Process == th.Process
	})
	fs := []stackFrame{}
	for k, f := range th.Frames {
		if k < a.StartFrame || a.Levels > 0 && len(fs) == a.Levels {
			continue
		}
		sf := stackFrame{ID: s.base[i] + k + 1, Name: f.Name,
			Line: s.lineOut(f.Src.Line), Column: s.colOut(f.Src.Col)}
		switch {
		case f.Src.Path != "":
			sf.Source = &source{Name: path.Base(f.Src.Path),
				Path: filepath.Join(s.l.Root,
					filepath.FromSlash(f.Src.Path))}
		case f.Src.Bundled != "":
			sf.Source = bundledSource(f.Src.Bundled)
		}
		fs = append(fs, sf)
	}
	o.reply(req, map[string]any{"stackFrames": fs,
		"totalFrames": len(th.Frames)})
}

// bundledSource is the source of the bundled std/ieee file at bundle
// path p (nil when there is none). The file is not on disk, so the
// source has no path, only a sourceReference, the file's place in
// stdlib.Files plus one, whose text the client reads with a source
// request.
func bundledSource(p string) *source {
	i := slices.IndexFunc(stdlib.Files(), func(f stdlib.File) bool {
		return f.Path == p
	})
	if i < 0 {
		return nil
	}
	return &source{Name: path.Base(p), SourceReference: i + 1}
}

// source answers with the text of the bundled file a sourceReference
// names (bundledSource).
func (s *Server) source(o *out, req request) {
	var a struct {
		Source *struct {
			SourceReference int `json:"sourceReference"`
		} `json:"source"`
		SourceReference int `json:"sourceReference"`
	}
	_ = json.Unmarshal(req.Arguments, &a)
	ref := a.SourceReference
	if a.Source != nil && a.Source.SourceReference != 0 {
		ref = a.Source.SourceReference
	}
	if fs := stdlib.Files(); ref >= 1 && ref <= len(fs) {
		if b, ok := stdlib.Source(fs[ref-1].Path); ok {
			o.reply(req, map[string]any{"content": string(b)})
			return
		}
	}
	o.fail(req, "no source "+strconv.Itoa(ref))
}

// Each frame f has two scopes: its locals, variables reference 2f-1,
// and the signals its process sees, 2f.
func (s *Server) scopes(o *out, req request) {
	var a struct {
		FrameID int `json:"frameId"`
	}
	_ = json.Unmarshal(req.Arguments, &a)
	if _, f := s.frame(a.FrameID); f == nil {
		o.fail(req, "no frame "+strconv.Itoa(a.FrameID))
		return
	}
	o.reply(req, map[string]any{"scopes": []map[string]any{
		{"name": "Locals", "presentationHint": "locals",
			"variablesReference": 2*a.FrameID - 1, "expensive": false},
		{"name": "Signals", "variablesReference": 2 * a.FrameID,
			"expensive": false},
	}})
}

type variable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variablesReference"`
}

func (s *Server) variables(o *out, req request) {
	var a struct {
		Ref int `json:"variablesReference"`
	}
	_ = json.Unmarshal(req.Arguments, &a)
	th, f := s.frame((a.Ref + 1) / 2)
	if f == nil {
		o.fail(req, "no variables "+strconv.Itoa(a.Ref))
		return
	}
	vs := []variable{}
	add := func(name string, v elab.Value) {
		val := v.Value
		if len(val) > MaxValue {
			val = val[:MaxValue] + "..."
		}
		vs = append(vs, variable{Name: name, Value: val, Type: v.Type})
	}
	if a.Ref%2 == 1 {
		for _, v := range f.Vars {
			add(v.Name, v)
		}
	} else {
		scope := s.scopeOf(th.Path)
		for _, v := range s.stop.Signals {
			if name, ok := inScope(scope, v.Name); ok {
				add(name, v)
			}
		}
	}
	o.reply(req, map[string]any{"variables": vs})
}

// scopeOf returns the hierarchy scopes a process at path sees
// signals of, innermost first: the blocks and generates it is in, up
// to and including its design entity instance.
func (s *Server) scopeOf(p string) []string {
	var out []string
	sc := s.l.Session.Model().Scopes
	for {
		i := strings.LastIndexByte(p, '.')
		if i < 0 {
			return out
		}
		p = p[:i]
		out = append(out, p)
		k := slices.IndexFunc(sc, func(x trace.Scope) bool {
			return x.Path == p
		})
		if k < 0 || sc[k].Kind == trace.Instance {
			return out
		}
	}
}

// inScope returns the name of the signal at path relative to the
// instance of scope (the last one) when path is declared in one of
// the scopes.
func inScope(scope []string, p string) (string, bool) {
	i := strings.LastIndexByte(p, '.')
	if i < 0 || len(scope) == 0 || !slices.Contains(scope, p[:i]) {
		return "", false
	}
	return p[len(scope[len(scope)-1])+1:], true
}
