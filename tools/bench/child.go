package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/connerohnesorge/gohard/internal/server"
)

// childOut is what one benchmark process reports to the driver (one
// JSON value on stdout). Times are milliseconds.
type childOut struct {
	Mode string
	// FirstUnixNano is when the first diagnostics arrived ("first").
	FirstUnixNano int64 `json:",omitempty"`
	Samples       []Sample
	Hover, Definition, Completion,
	Save []float64
	// SimBegin is the time from the run command to the run's
	// $/progress begin, SimElab to its "elaborated" report (-1: none)
	// and SimKernel to its first simulated-time report (or the
	// response of a run that finished before one).
	SimBegin, SimElab, SimKernel []float64
	Skipped                      int
	// Reused counts keystrokes whose text had a stored analysis (the
	// server reuses its latest two results), so no check ran.
	Reused int
	Notes  []string
}

type childArgs struct {
	mode, dir   string
	p           Project
	n           int
	seed        uint64
	cpuprofile  string
	keystrokeTO time.Duration
}

// runChild is one fresh process of the benchmark: "first" (cold start
// to the first published diagnostics), "cold" (the first keystroke of
// a fresh process), "warm" (keystrokes, queries and saves of a running
// server after its first analysis) or "sim" (first simulation
// progress of a warm server).
func runChild(a childArgs) (childOut, error) {
	out := childOut{Mode: a.mode}
	path := filepath.Join(a.dir, filepath.FromSlash(a.p.Target))
	text, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	uri := server.PathToURI(path)
	c := start(a.dir)
	defer c.stop()
	if err := c.initialize(server.PathToURI(a.dir)); err != nil {
		return out, err
	}
	switch a.mode {
	case "first":
		c.didOpen(uri, text)
		at, clean, err := c.firstDiagnostics(5 * time.Minute)
		if clean {
			out.Notes = append(out.Notes, "no diagnostics: first "+
				"diagnostics is the end of the first analysis")
		}
		out.FirstUnixNano = at.UnixNano()
		return out, err
	case "cold":
		c.didOpen(uri, text)
		e := Keystrokes(text, a.seed, 1)
		s, ok, err := c.keystroke(uri, 2, e[0], 5*time.Minute)
		if err != nil {
			return out, err
		}
		if !ok {
			out.Skipped++
		} else {
			out.Samples = append(out.Samples, s)
		}
		return out, nil
	case "sim":
		return c.simProgress(a, out)
	}
	c.didOpen(uri, text)
	if _, _, err := c.firstDiagnostics(5 * time.Minute); err != nil {
		return out, err
	}
	if a.cpuprofile != "" {
		f, err := os.Create(a.cpuprofile)
		if err != nil {
			return out, err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			return out, err
		}
		defer f.Close()
		defer pprof.StopCPUProfile()
	}
	// Per edit kind, two warm-up keystrokes, then n measured ones (n
	// even, so the text is the original again after each kind and for
	// the queries): body edits first, then signature edits.
	n := a.n + a.n%2
	v := 1
	cur := text
	for _, kind := range []string{KindBody, KindSignature} {
		edits := KeystrokesIn(text, kind, a.seed, n+2)
		if edits == nil {
			out.Notes = append(out.Notes, "the target has no "+kind+
				" lines")
		}
		for i, e := range edits {
			v++
			cur = Apply(cur, e)
			s, ok, err := c.keystroke(uri, v, e, a.keystrokeTO)
			s.Kind = kind
			switch {
			case err != nil:
				out.Notes = append(out.Notes, err.Error())
				out.Skipped++
			case i < 2:
			case !ok:
				out.Reused++
			default:
				out.Samples = append(out.Samples, s)
			}
		}
	}
	pos := Names(cur, a.seed^0x9e3779b97f4a7c15, a.n)
	for _, q := range []struct {
		method string
		into   *[]float64
	}{
		{"textDocument/hover", &out.Hover},
		{"textDocument/definition", &out.Definition},
		{"textDocument/completion", &out.Completion},
	} {
		for _, p := range pos {
			t0 := time.Now()
			m, err := c.request(q.method, map[string]any{
				"textDocument": map[string]string{"uri": uri},
				"position": map[string]int{"line": p.Line,
					"character": p.Char}})
			if err != nil {
				return out, err
			}
			*q.into = append(*q.into, ms(m.at.Sub(t0)))
		}
	}
	// Saves: one stray name stays in the file throughout, so every
	// save publishes the target's diagnostics (a document without any,
	// before and after, gets no publish to wait for).
	saves := Keystrokes(text, a.seed+1, a.n+a.n%2+1)
	keep := saves[0]
	saves = saves[1:]
	for i := range saves {
		if saves[i].Line == keep.Line {
			saves[i].Line++ // a pure function still; never typed over
			saves[i].EndLine++
		}
	}
	v++
	cur = Apply(cur, keep)
	if _, _, err := c.keystroke(uri, v, keep, time.Minute); err != nil {
		return out, err
	}
	for _, e := range saves {
		v++
		cur = Apply(cur, e)
		if err := os.WriteFile(path, cur, 0o644); err != nil {
			return out, err
		}
		x := c.snap
		c.didChange(uri, v, e)
		c.notify("textDocument/didSave", map[string]any{
			"textDocument": map[string]string{"uri": uri}})
		t0 := time.Now()
		m, err := c.await("diagnostics after save", time.Minute,
			snapshotOf(uri, x+2))
		if err != nil {
			out.Notes = append(out.Notes, err.Error())
			out.Skipped++
			continue
		}
		out.Save = append(out.Save, ms(m.at.Sub(t0)))
	}
	return out, nil
}

// keystroke sends e as version v of uri and measures the time to the
// diagnostics published for it.
func (c *client) keystroke(uri string, v int, e Edit,
	timeout time.Duration,
) (Sample, bool, error) {
	t0 := time.Now()
	c.didChange(uri, v, e)
	m, err := c.await(fmt.Sprintf("diagnostics of version %d", v),
		timeout, published(uri, v))
	if err != nil {
		return Sample{}, false, err
	}
	s, ok := split(c.rec.events(), t0, m.at)
	return s, ok, nil
}

// snapshotOf matches the $/gohard/snapshot notification of uri at
// snapshot id or later.
func snapshotOf(uri string, id uint64,
) func(map[string]json.RawMessage) bool {
	return func(m map[string]json.RawMessage) bool {
		if method(m) != server.SnapshotMethod {
			return false
		}
		var p struct {
			Snapshot uint64
			URI      string
		}
		_ = json.Unmarshal(m["params"], &p)
		return p.URI == uri && p.Snapshot >= id
	}
}

// simProgress runs the project's testbench from a warm server and
// measures the time to the run's first $/progress begin and report.
func (c *client) simProgress(a childArgs, out childOut) (childOut,
	error,
) {
	path := filepath.Join(a.dir, filepath.FromSlash(a.p.TestbenchFile))
	text, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	c.didOpen(server.PathToURI(path), text)
	if _, _, err := c.firstDiagnostics(5 * time.Minute); err != nil {
		return out, err
	}
	if a.cpuprofile != "" {
		f, err := os.Create(a.cpuprofile)
		if err != nil {
			return out, err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			return out, err
		}
		defer f.Close()
		defer pprof.StopCPUProfile()
	}
	id := c.next + 1
	c.next = id
	t0 := time.Now()
	c.write(map[string]any{"jsonrpc": "2.0", "id": id,
		"method": "workspace/executeCommand", "params": map[string]any{
			"command":       server.CmdRun,
			"workDoneToken": "bench-sim",
			"arguments": []any{map[string]string{
				"testbench": a.p.Testbench}},
		}})
	// The run's $/progress: begin at once, analysis reports
	// ("parsing"), "<tb>: elaborated", then "<tb>: <simulated time>"
	// from the kernel. First progress is the first kernel report, or
	// the response when the run finished before any.
	var begin, elab, kernel float64 = -1, -1, -1
	m, err := c.await("simulation progress", 2*time.Minute,
		func(m map[string]json.RawMessage) bool {
			if method(m) == "" && string(m["id"]) == strconv.Itoa(id) {
				return true
			}
			if method(m) != "$/progress" {
				return false
			}
			var p struct {
				Token string
				Value struct{ Kind, Message string }
			}
			_ = json.Unmarshal(m["params"], &p)
			if p.Token != "bench-sim" {
				return false
			}
			t := ms(time.Since(t0))
			tb := a.p.Testbench + ": "
			switch msg := p.Value.Message; {
			case p.Value.Kind == "begin" && begin < 0:
				begin = t
			case msg == tb+"elaborated" && elab < 0:
				elab = t
			case strings.HasPrefix(msg, tb) && msg != tb+"elaborated":
				return true
			}
			return false
		})
	if err != nil {
		return out, err
	}
	kernel = ms(m.at.Sub(t0))
	if method(m.m) == "" {
		out.Notes = append(out.Notes, "the run finished before any "+
			"kernel progress: its response counts")
		if e := m.m["error"]; e != nil {
			out.Notes = append(out.Notes, "the run failed: "+string(e))
		}
	}
	out.SimBegin = append(out.SimBegin, begin)
	if elab >= 0 {
		out.SimElab = append(out.SimElab, elab)
	}
	out.SimKernel = append(out.SimKernel, kernel)
	return out, nil
}
