package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/repro"
)

// fakeSim is a Simulator whose runs take a fixed number of slices and
// log every slice they run, so tests can see what ran when.
type fakeSim struct {
	slices int
	log    *[]string
}

// Testbenches finds "entity tb" in every document; Deps is the text.
func (f fakeSim) Testbenches(snap *Snapshot) Task[[]Testbench] {
	var out []Testbench
	for _, d := range snap.Documents() {
		if i := bytes.Index(d.Text(), []byte("entity tb")); i >= 0 {
			out = append(out, Testbench{Name: "tb", URI: d.URI,
				Span: Span{i + 7, i + 9}, Deps: string(d.Text())})
		}
	}
	return Done(out)
}

func (f fakeSim) Simulate(snap *Snapshot, tb string,
	seed uint64,
) Task[SimResult] {
	*f.log = append(*f.log, "start "+tb)
	ds := []diag.Diagnostic{
		{Path: "a.vhd", Line: 1, Col: 1, Severity: diag.Error,
			Code: diag.SimAssertError, Message: "e"},
		{Path: "a.vhd", Line: 1, Col: 2, Severity: diag.Warning,
			Code: diag.SimAssertWarning, Message: "w"},
		{Path: "a.vhd", Line: 1, Col: 3, Severity: diag.Hint,
			Code: diag.SimAssertNote, Message: "n"},
		{Severity: diag.Warning, Code: diag.SimBudgetExceeded,
			Message: "no path"},
	}
	res := SimResult{Ran: true, Run: ds, Trace: []byte("trace"),
		Write: true, Report: NewSimReport(tb, &seed, "quiet", "1 ns",
			ds)}
	var step func(n int) Task[SimResult]
	step = func(n int) Task[SimResult] {
		return TaskFunc[SimResult](func(int) Slice[SimResult] {
			*f.log = append(*f.log, "sim")
			if n >= f.slices {
				return Slice[SimResult]{Done: true, Value: res}
			}
			return Slice[SimResult]{Next: step(n + 1),
				Report: &Report{Message: "tb", Percentage: -1}}
		})
	}
	return step(1)
}

// logged is an analyzer whose hovers take three logged slices.
type logged struct {
	slow
	log *[]string
}

func (a logged) Hover(q Query) Task[*Hover] {
	var step func(n int) Task[*Hover]
	step = func(n int) Task[*Hover] {
		return TaskFunc[*Hover](func(int) Slice[*Hover] {
			*a.log = append(*a.log, "hover")
			if n >= 3 {
				return Slice[*Hover]{Done: true,
					Value: &Hover{Markdown: "h"}}
			}
			return Slice[*Hover]{Next: step(n + 1)}
		})
	}
	return step(1)
}

// simHarness drives the core with a simulator. Effects are recorded;
// LoadConfig answers with cfg.
type simHarness struct {
	*harness
	cfg     config.Config
	effects []Effect
	log     []string
}

func newSimHarness(t *testing.T, slices int) *simHarness {
	h := &simHarness{harness: &harness{t: t, exit: -1},
		cfg: config.Defaults()}
	h.s = New(Options{Analyzer: logged{slow{hover: 1, diags: 1}, &h.log},
		Simulator: fakeSim{slices, &h.log}, Version: "t"})
	return h
}

func (h *simHarness) step() bool {
	s, ev, ok := h.s.Next()
	if !ok {
		return false
	}
	s, fx := Step(s, ev)
	h.s = s
	for _, e := range fx {
		switch e := e.(type) {
		case Send:
			h.sent = append(h.sent, sent{e.Msg, h.s.Snapshot().ID,
				h.s.Now()})
		case LoadConfig:
			h.s = h.s.Push(ConfigLoaded{config.Loaded{Root: e.Folder,
				Found: config.FoundStart, Config: h.cfg}}, PriorityResult)
		case LoadWorkspace:
			h.s = h.s.Push(WorkspaceLoaded{Root: e.Root},
				PriorityResult)
		case Exit:
			h.exit = e.Code
		default:
			h.effects = append(h.effects, e)
			if o, ok := e.(OpenWaveform); ok {
				h.s = h.s.Push(WaveformOpened{ID: o.ID, Root: o.Root},
					PriorityResult)
			}
		}
	}
	return true
}

func (h *simHarness) drain() {
	for i := 0; h.step(); i++ {
		if i > 1_000_000 {
			h.t.Fatal("the core never became idle")
		}
	}
}

func (h *simHarness) start(caps string) {
	h.client(`{"jsonrpc":"2.0","id":"init","method":"initialize",`+
		`"params":{"rootUri":"file:///work","capabilities":%s}}`, caps)
	h.client(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	h.drain()
}

func (h *simHarness) command(id int, cmd string, args string) {
	h.client(`{"jsonrpc":"2.0","id":%d,"method":`+
		`"workspace/executeCommand","params":{"command":%q,`+
		`"arguments":[%s]}}`, id, cmd, args)
}

func (h *simHarness) lenses(id int) []string {
	h.client(`{"jsonrpc":"2.0","id":%d,"method":"textDocument/codeLens",`+
		`"params":{"textDocument":{"uri":%q}}}`, id, uri)
	for range 20 {
		if r := h.response(id); r != nil {
			var ls []struct {
				Command struct {
					Title string `json:"title"`
				} `json:"command"`
			}
			if err := json.Unmarshal(r.Result, &ls); err != nil {
				h.t.Fatal(err)
			}
			var out []string
			for _, l := range ls {
				out = append(out, l.Command.Title)
			}
			return out
		}
		h.step()
	}
	h.t.Fatalf("no code lens response %d", id)
	return nil
}

func (h *simHarness) response(id int) *jsonrpc2.Response {
	rs := h.responses()[fmt.Sprint(id)]
	if len(rs) == 0 {
		return nil
	}
	if len(rs) > 1 {
		h.t.Fatalf("request %d got %d responses", id, len(rs))
	}
	return rs[0]
}

// runUntil steps until the log holds n sim slices.
func (h *simHarness) runUntil(n int) {
	for range 1000 {
		if count(h.log, "sim") >= n {
			return
		}
		if !h.step() {
			break
		}
	}
	h.t.Fatalf("the run never reached %d slices: %v", n, h.log)
}

func count(xs []string, x string) int {
	n := 0
	for _, y := range xs {
		if y == x {
			n++
		}
	}
	return n
}

const tbText = "entity tb is end;\n"

// TestSimLensLabels walks the lens of #25: "▶ Run tb", "◉ Running…"
// while it runs, then "▶ Re-run · seed 42" with "Copy repro", which
// copies the gohard check --repro command of the run.
func TestSimLensLabels(t *testing.T) {
	h := newSimHarness(t, 20)
	h.start(`{"workspace":{"codeLens":{"refreshSupport":true}}}`)
	h.open(uri, tbText)
	h.drain()
	if got := h.lenses(1); !slices.Equal(got, []string{"▶ Run tb"}) {
		t.Fatalf("before a run: %q", got)
	}
	h.command(2, CmdRun, `{"testbench":"tb","seed":42}`)
	h.runUntil(2)
	if got := h.lenses(3); !slices.Equal(got,
		[]string{"◉ Running…"}) {
		t.Fatalf("while running: %q", got)
	}
	h.drain()
	if got := h.lenses(4); !slices.Equal(got,
		[]string{"▶ Re-run · seed 42", "Copy repro"}) {
		t.Fatalf("after the run: %q", got)
	}
	var rep SimReport
	if err := json.Unmarshal(h.response(2).Result, &rep); err != nil ||
		rep.Testbench != "tb" || *rep.Seed != 42 ||
		rep.Repro != ".gohard/traces/tb-42.repro.json" {
		t.Fatalf("run response %s (%v)", h.response(2).Result, err)
	}
	h.command(5, CmdRepro, `{"testbench":"tb"}`)
	h.drain()
	want := `"gohard check --repro .gohard/traces/tb-42.repro.json"`
	if got := strings.TrimSpace(string(h.response(5).Result)); got != want {
		t.Fatalf("copy repro = %s, want %s", got, want)
	}
	refresh := 0
	for _, s := range h.sent {
		if r, ok := s.msg.(*jsonrpc2.Request); ok &&
			r.Method == "workspace/codeLens/refresh" {
			refresh++
		}
	}
	if refresh < 2 {
		t.Fatalf("%d lens refreshes, want one at start and end", refresh)
	}
	// The run wrote its trace and repro file through the shell, with
	// the hash of the simulated text.
	var w *WriteRun
	for _, e := range h.effects {
		if e, ok := e.(WriteRun); ok {
			w = &e
		}
	}
	if w == nil || w.Trace != ".gohard/traces/tb-42.trace" ||
		w.Header.Testbench != "tb" || w.Header.Seed != 42 ||
		len(w.Header.Sources) != 1 ||
		w.Header.Sources[0] != (repro.File{Path: "a.vhd",
			SHA256: repro.Hash([]byte(tbText))}) {
		t.Fatalf("WriteRun = %+v", w)
	}
}

// TestSimRequestsOutrankSlices: once an editor request arrives, no
// sim slice runs until it is answered.
func TestSimRequestsOutrankSlices(t *testing.T) {
	for _, at := range []int{1, 2, 5, 13} {
		h := newSimHarness(t, 30)
		h.start(`{}`)
		h.open(uri, tbText)
		h.drain()
		h.command(1, CmdRun, `{"testbench":"tb"}`)
		h.runUntil(at)
		h.log = append(h.log, "request")
		h.hover(2, uri)
		h.drain()
		// The slice queued before the request arrived may still run
		// (the next slice boundary); after it, none until the hover
		// is answered.
		i := slices.Index(h.log, "request")
		rest := h.log[i+1:]
		if len(rest) > 0 && rest[0] == "sim" {
			rest = rest[1:]
		}
		last := slices.Index(rest, "hover") + 2
		if count(rest[:last+1], "hover") != 3 ||
			count(rest[:last+1], "sim") != 0 {
			t.Fatalf("at %d: sim slices ran before the hover finished: "+
				"%v", at, rest)
		}
		if h.response(2) == nil || h.response(1) == nil ||
			count(h.log, "sim") != 30 {
			t.Fatalf("at %d: responses missing or run cut: %v", at, h.log)
		}
	}
}

// TestSimStopsAtSliceBoundary: an edit or a cancel ends the run at the
// next slice boundary with exactly one final response, and no slice
// runs after it.
func TestSimStopsAtSliceBoundary(t *testing.T) {
	for _, c := range []struct {
		name string
		do   func(h *simHarness)
		code int64
	}{
		{"edit", func(h *simHarness) { h.change(uri, 2, tbText+"--\n") },
			jsonrpc2.CodeContentModified},
		{"cancel", func(h *simHarness) { h.cancel(1) },
			jsonrpc2.CodeRequestCancelled},
	} {
		for _, at := range []int{1, 3, 9} {
			h := newSimHarness(t, 50)
			h.start(`{}`)
			h.open(uri, tbText)
			h.drain()
			h.command(1, CmdRun, `{"testbench":"tb"}`)
			h.runUntil(at)
			c.do(h)
			h.drain()
			r := h.response(1)
			if r == nil || r.Error == nil || r.Error.Code != c.code {
				t.Fatalf("%s at %d: response %+v", c.name, at, r)
			}
			// Only the slice already queued when the message arrived
			// may still run: the run stops at the next boundary.
			if n := count(h.log, "sim"); n > at+1 {
				t.Fatalf("%s at %d: %d slices ran", c.name, at, n)
			}
			for _, e := range h.effects {
				if _, ok := e.(WriteRun); ok {
					t.Fatalf("%s: a stopped run wrote files", c.name)
				}
			}
		}
	}
}

// TestSimDiagnostics: results are published as gohard-sim diagnostics
// with the #25 severity map, next to the analysis diagnostics, each
// publish followed by its snapshot notification.
func TestSimDiagnostics(t *testing.T) {
	h := newSimHarness(t, 2)
	h.start(`{}`)
	h.open(uri, tbText)
	h.drain()
	h.command(1, CmdRun, `{"testbench":"tb"}`)
	h.drain()
	pubs := notes(h.harness, "textDocument/publishDiagnostics")
	var p struct {
		URI         string          `json:"uri"`
		Diagnostics []lspDiagnostic `json:"diagnostics"`
	}
	last := pubs[len(pubs)-1].msg.(*jsonrpc2.Notification)
	if err := json.Unmarshal(last.Params, &p); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range p.Diagnostics {
		got = append(got, fmt.Sprintf("%s %d %s", d.Source, d.Severity,
			d.Code))
	}
	want := []string{"gohard 4 lsp/test",
		"gohard-sim 1 sim/assert-error",
		"gohard-sim 2 sim/assert-warning",
		"gohard-sim 4 sim/assert-note",
		"gohard-sim 2 sim/budget-exceeded"}
	if !slices.Equal(got, want) {
		t.Fatalf("published %q\nwant %q", got, want)
	}
	for i, s := range h.sent {
		n, ok := s.msg.(*jsonrpc2.Notification)
		if !ok || n.Method != "textDocument/publishDiagnostics" {
			continue
		}
		next, ok := h.sent[i+1].msg.(*jsonrpc2.Notification)
		if !ok || next.Method != SnapshotMethod {
			t.Fatal("a publish without its snapshot notification")
		}
	}
}

// TestSimProgress: a run reports $/progress on a server-created token.
func TestSimProgress(t *testing.T) {
	h := newSimHarness(t, 5)
	h.start(`{"window":{"workDoneProgress":true}}`)
	h.open(uri, tbText)
	h.drain()
	h.command(1, CmdRun, `{"testbench":"tb"}`)
	var create *jsonrpc2.Request
	for create == nil && h.step() {
		for _, s := range h.sent {
			if r, ok := s.msg.(*jsonrpc2.Request); ok &&
				strings.Contains(string(r.Params), "gohard/") &&
				h.response(1) == nil {
				create = r
			}
		}
	}
	id, _ := create.ID.Int()
	h.client(`{"jsonrpc":"2.0","id":%d,"result":null}`, id)
	h.drain()
	var kinds []string
	for _, s := range notes(h.harness, "$/progress") {
		var p progressParams
		_ = json.Unmarshal(s.msg.(*jsonrpc2.Notification).Params, &p)
		if p.Value.Title == "gohard: simulating tb" ||
			p.Value.Kind != "begin" {
			kinds = append(kinds, p.Value.Kind)
		}
	}
	if len(kinds) < 3 || kinds[0] != "begin" ||
		kinds[len(kinds)-1] != "end" {
		t.Fatalf("progress %v", kinds)
	}
}

// TestAutoRunOnSave: off by default; when on, saves within the
// debounce start one run, and a save that changes no dependency starts
// none.
func TestAutoRunOnSave(t *testing.T) {
	save := func(h *simHarness) {
		h.client(`{"jsonrpc":"2.0","method":"textDocument/didSave",`+
			`"params":{"textDocument":{"uri":%q}}}`, uri)
	}
	h := newSimHarness(t, 2)
	h.start(`{}`)
	h.open(uri, tbText)
	h.drain()
	save(h)
	h.drain()
	if n := count(h.log, "start tb"); n != 0 {
		t.Fatalf("auto run while off: %d runs", n)
	}

	h = newSimHarness(t, 2)
	h.cfg.Simulation.AutoRunOnSave = true
	h.start(`{}`)
	h.open(uri, tbText)
	h.drain()
	save(h)
	save(h)
	save(h)
	h.drain()
	if n := count(h.log, "start tb"); n != 1 {
		t.Fatalf("three saves in the debounce: %d runs", n)
	}
	save(h)
	h.drain()
	if n := count(h.log, "start tb"); n != 1 {
		t.Fatalf("a save without changes ran again: %d runs", n)
	}
	h.change(uri, 2, tbText+"-- edited\n")
	h.drain()
	save(h)
	h.drain()
	if n := count(h.log, "start tb"); n != 2 {
		t.Fatalf("a changed dependency did not run: %d runs", n)
	}
}

// TestOpenWaveformAfterRun: with no earlier run the action runs the
// testbench, then asks the shell to open its trace; afterwards it
// opens the recorded trace without running again.
func TestOpenWaveformAfterRun(t *testing.T) {
	h := newSimHarness(t, 2)
	h.start(`{}`)
	h.open(uri, tbText)
	h.drain()
	h.command(1, CmdWaveform, `{"uri":"`+uri+`"}`)
	h.drain()
	h.command(2, CmdWaveform, `{"testbench":"tb"}`)
	h.drain()
	var opens []string
	for _, e := range h.effects {
		if o, ok := e.(OpenWaveform); ok {
			opens = append(opens, o.Trace)
		}
	}
	if count(h.log, "start tb") != 1 || len(opens) != 2 ||
		opens[0] != "/work/.gohard/traces/tb-0.trace" ||
		opens[1] != opens[0] {
		t.Fatalf("runs %v, opens %v", h.log, opens)
	}
	if h.response(1) == nil || h.response(2) == nil {
		t.Fatal("Open waveform got no response")
	}
}

// TestSimDeterministic: the same input gives the same output, and a
// State forked in the middle of a run finishes identically (P3).
func TestSimDeterministic(t *testing.T) {
	script := func(h *simHarness) {
		h.start(`{"window":{"workDoneProgress":true}}`)
		h.open(uri, tbText)
		h.command(1, CmdRun, `{"testbench":"tb","seed":7}`)
		h.hover(2, uri)
	}
	out := func(h *simHarness) string {
		var b strings.Builder
		for _, s := range h.sent {
			b.Write(Send{s.msg}.Body())
			b.WriteByte('\n')
		}
		return b.String()
	}
	a, b := newSimHarness(t, 9), newSimHarness(t, 9)
	script(a)
	script(b)
	a.drain()
	b.drain()
	if out(a) != out(b) {
		t.Fatal("two runs of one script differ")
	}
	c := newSimHarness(t, 9)
	script(c)
	for range 12 {
		c.step()
	}
	fork := &simHarness{harness: &harness{t: t, s: c.s,
		sent: slices.Clone(c.sent), exit: -1}, cfg: c.cfg}
	c.drain()
	fork.drain()
	if out(c) != out(fork) || out(c) != out(a) {
		t.Fatal("a forked State finished differently")
	}
}

// TestRequestAfterWatchedChange: a request that arrives after a
// workspace/didChangeWatchedFiles notification waits for the reread
// and is answered on the new snapshot, not with ContentModified.
func TestRequestAfterWatchedChange(t *testing.T) {
	for _, file := range []string{"x.vhd", "gohard.yaml"} {
		h := newSimHarness(t, 1)
		h.start(`{}`)
		h.open(uri, tbText)
		h.drain()
		h.client(`{"jsonrpc":"2.0","method":`+
			`"workspace/didChangeWatchedFiles","params":{"changes":`+
			`[{"uri":"file:///work/%s","type":3}]}}`, file)
		h.hover(2, uri)
		h.drain()
		r := h.response(2)
		if r == nil || r.Error != nil {
			t.Fatalf("%s: hover after the change: %+v", file, r)
		}
	}
}
