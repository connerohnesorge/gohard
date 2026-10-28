package server

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
)

// slow is an analyzer whose tasks need several slices, so tests can
// interleave cancels, edits and slices.
type slow struct {
	Nop
	hover, diags int // slices per task
}

func (a slow) Features() Features {
	return Features{Set: FeatureDiagnostics | FeatureHover}
}

func (a slow) Diagnostics(snap *Snapshot) Task[[]FileDiagnostics] {
	var fds []FileDiagnostics
	for _, d := range snap.Documents() {
		fds = append(fds, FileDiagnostics{URI: d.URI,
			Diagnostics: []diag.Diagnostic{{Line: 1, Col: 1,
				Severity: diag.Hint, Code: "lsp/test",
				Message: "snapshot " +
					strconv.FormatUint(snap.ID, 10)}}})
	}
	return Steps(a.diags, fds)
}

func (a slow) Hover(q Query) Task[*Hover] {
	return Steps(a.hover, &Hover{Markdown: "snapshot " +
		strconv.FormatUint(q.Snap.ID, 10)})
}

// harness drives the pure core directly, one event at a time, and
// performs the effects without any I/O.
type harness struct {
	t    *testing.T
	s    State
	sent []sent
	exit int
	hold bool // drop LoadConfig and LoadWorkspace: loads never finish
}

type sent struct {
	msg  jsonrpc2.Message
	snap uint64 // the latest snapshot when it was sent
	at   time.Duration
}

func newHarness(t *testing.T, an Analyzer) *harness {
	return &harness{t: t, s: New(Options{Analyzer: an, Version: "t"}),
		exit: -1}
}

func (h *harness) client(format string, args ...any) {
	h.s = h.s.Push(Message{Data: []byte(fmt.Sprintf(format, args...))},
		PriorityClient)
}

// step runs one queued event; it reports false when the queue is
// empty.
func (h *harness) step() bool {
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
			if h.hold {
				continue
			}
			l := config.Loaded{Root: e.Folder, Found: config.FoundStart,
				Config: config.Defaults()}
			h.s = h.s.Push(ConfigLoaded{l}, PriorityResult)
		case LoadWorkspace:
			if h.hold {
				continue
			}
			h.s = h.s.Push(WorkspaceLoaded{Root: e.Root},
				PriorityResult)
		case Exit:
			h.exit = e.Code
		}
	}
	return true
}

func (h *harness) drain() {
	for i := 0; h.step(); i++ {
		if i > 1_000_000 {
			h.t.Fatal("the core never became idle")
		}
	}
}

func (h *harness) start(caps string) {
	h.client(`{"jsonrpc":"2.0","id":"init","method":"initialize",`+
		`"params":{"rootUri":"file:///work","capabilities":%s}}`, caps)
	h.client(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	h.drain()
}

func (h *harness) open(uri, text string) {
	h.client(`{"jsonrpc":"2.0","method":"textDocument/didOpen",`+
		`"params":{"textDocument":{"uri":%q,"languageId":"vhdl",`+
		`"version":1,"text":%q}}}`, uri, text)
}

func (h *harness) change(uri string, version int, text string) {
	h.client(`{"jsonrpc":"2.0","method":"textDocument/didChange",`+
		`"params":{"textDocument":{"uri":%q,"version":%d},`+
		`"contentChanges":[{"text":%q}]}}`, uri, version, text)
}

func (h *harness) hover(id int, uri string) {
	h.client(`{"jsonrpc":"2.0","id":%d,"method":"textDocument/hover",`+
		`"params":{"textDocument":{"uri":%q},"position":`+
		`{"line":0,"character":0}}}`, id, uri)
}

func (h *harness) cancel(id int) {
	h.client(`{"jsonrpc":"2.0","method":"$/cancelRequest",`+
		`"params":{"id":%d}}`, id)
}

// responses counts the responses per request ID.
func (h *harness) responses() map[string][]*jsonrpc2.Response {
	out := map[string][]*jsonrpc2.Response{}
	for _, s := range h.sent {
		if r, ok := s.msg.(*jsonrpc2.Response); ok {
			out[r.ID.String()] = append(out[r.ID.String()], r)
		}
	}
	return out
}

func notes(h *harness, method string) []sent {
	var out []sent
	for _, s := range h.sent {
		if n, ok := s.msg.(*jsonrpc2.Notification); ok &&
			n.Method == method {
			out = append(out, s)
		}
	}
	return out
}

// rng is a tiny deterministic generator for the property tests.
type rng struct{ env.SplitMix64 }

func (r *rng) n(k int) int { return env.Intn(&r.SplitMix64, k) }

const uri = "file:///work/a.vhd"

// TestExactlyOneResponse: under any interleaving of requests, cancels,
// edits and slices, every request gets exactly one final response, a
// cancel that arrives before the result gives RequestCancelled, and a
// result is never sent for an older snapshot (#25 invariants).
func TestExactlyOneResponse(t *testing.T) {
	for seed := range uint64(300) {
		r := &rng{env.SplitMix64{State: seed}}
		h := newHarness(t, slow{hover: 1 + r.n(5), diags: 1 + r.n(4)})
		h.start(`{}`)
		h.open(uri, "x")
		h.drain()
		next, version := 1, 1
		cancelled := map[string]bool{}
		for range 60 {
			switch r.n(5) {
			case 0:
				h.hover(next, uri)
				next++
			case 1:
				if next > 1 {
					id := 1 + r.n(next-1)
					h.cancel(id)
				}
			case 2:
				version++
				h.change(uri, version, "v"+strconv.Itoa(version))
			default:
				h.step()
			}
			for _, s := range h.sent {
				if r, ok := s.msg.(*jsonrpc2.Response); ok &&
					r.Error != nil &&
					r.Error.Code == jsonrpc2.CodeRequestCancelled {
					cancelled[r.ID.String()] = true
				}
			}
		}
		h.drain()
		got := h.responses()
		for id := 1; id < next; id++ {
			rs := got[strconv.Itoa(id)]
			if len(rs) != 1 {
				t.Fatalf("seed %d: request %d got %d responses", seed,
					id, len(rs))
			}
		}
		// A successful hover names the snapshot it was computed for;
		// it must be the latest one at the time it was sent.
		for _, s := range h.sent {
			r, ok := s.msg.(*jsonrpc2.Response)
			if !ok || r.Error != nil || string(r.Result) == "null" {
				continue
			}
			var v struct {
				Contents struct{ Value string }
			}
			if json.Unmarshal(r.Result, &v) != nil ||
				v.Contents.Value == "" {
				continue
			}
			want := "snapshot " + strconv.FormatUint(s.snap, 10)
			if v.Contents.Value != want {
				t.Fatalf("seed %d: response %s computed for %q "+
					"sent at %s", seed, r.ID, v.Contents.Value, want)
			}
		}
	}
}

// TestStaleDiagnosticsNeverPublished: with slow diagnostics and edits
// arriving between slices, every publish comes from the snapshot that
// is the latest when it is sent, and once idle the last publish is for
// the final snapshot.
func TestStaleDiagnosticsNeverPublished(t *testing.T) {
	for seed := range uint64(300) {
		r := &rng{env.SplitMix64{State: seed}}
		h := newHarness(t, slow{diags: 1 + r.n(6)})
		h.start(`{}`)
		h.open(uri, "x")
		version := 1
		for range 40 {
			if r.n(3) == 0 {
				version++
				h.change(uri, version, "v")
			} else {
				h.step()
			}
		}
		h.drain()
		pubs := notes(h, "textDocument/publishDiagnostics")
		if len(pubs) == 0 {
			t.Fatalf("seed %d: nothing published", seed)
		}
		for _, p := range pubs {
			var v struct {
				Diagnostics []struct{ Message string }
			}
			n := p.msg.(*jsonrpc2.Notification)
			_ = json.Unmarshal(n.Params, &v)
			want := "snapshot " + strconv.FormatUint(p.snap, 10)
			if len(v.Diagnostics) != 1 || v.Diagnostics[0].Message != want {
				t.Fatalf("seed %d: published %s at %s", seed, n.Params,
					want)
			}
		}
		last := pubs[len(pubs)-1]
		if last.snap != h.s.Snapshot().ID {
			t.Fatalf("seed %d: idle with snapshot %d published, "+
				"latest %d", seed, last.snap, h.s.Snapshot().ID)
		}
	}
}

// TestStateIsAValue: a kept State is not changed by later steps, and
// stepping a copy twice gives identical effects (fork and replay).
func TestStateIsAValue(t *testing.T) {
	h := newHarness(t, slow{hover: 3, diags: 2})
	h.start(`{}`)
	h.open(uri, "first")
	h.drain()
	kept := h.s
	keptSnap := kept.Snapshot()
	keptText := string(mustDoc(t, keptSnap).Text())
	h.change(uri, 2, "second")
	h.hover(1, uri)
	h.drain()
	if string(mustDoc(t, keptSnap).Text()) != keptText ||
		kept.Snapshot() != keptSnap {
		t.Fatal("a later step changed an older snapshot")
	}
	run := func(s State) string {
		s = s.Push(Message{Data: []byte(`{"jsonrpc":"2.0","id":9,` +
			`"method":"textDocument/hover","params":{"textDocument":` +
			`{"uri":"` + uri + `"},"position":{"line":0,` +
			`"character":0}}}`)}, PriorityClient)
		var b []byte
		for {
			var ev Event
			var ok bool
			s, ev, ok = s.Next()
			if !ok {
				return string(b)
			}
			var fx []Effect
			s, fx = Step(s, ev)
			for _, e := range fx {
				if snd, ok := e.(Send); ok {
					b = append(append(b, snd.Body()...), '\n')
				}
			}
		}
	}
	a, b := run(kept), run(kept)
	if a != b || a == "" {
		t.Fatalf("forked runs differ:\n%s\n%s", a, b)
	}
}

func mustDoc(t *testing.T, s *Snapshot) *Document {
	d, ok := s.Document(uri)
	if !ok {
		t.Fatal("no document")
	}
	return d
}

// TestNoProgressRule: work that neither finishes nor reports for
// NoProgress of virtual time (a step bound) ends with RequestFailed.
func TestNoProgressRule(t *testing.T) {
	limit := int(NoProgress / Tick)
	h := newHarness(t, slow{hover: limit + 10})
	h.start(`{}`)
	h.open(uri, "x")
	h.hover(1, uri)
	h.drain()
	rs := h.responses()["1"]
	if len(rs) != 1 || rs[0].Error == nil ||
		rs[0].Error.Code != jsonrpc2.CodeRequestFailed {
		t.Fatalf("got %+v", rs)
	}
	// Just under the bound the request completes.
	h = newHarness(t, slow{hover: limit - 1})
	h.start(`{}`)
	h.open(uri, "x")
	h.hover(1, uri)
	h.drain()
	if rs := h.responses()["1"]; len(rs) != 1 || rs[0].Error != nil {
		t.Fatalf("got %+v", rs)
	}
}

// TestLiveness is the liveness workload of #25: a session that edits
// and asks for hovers for 60 s of virtual time. While work is pending
// the server never goes NoProgress without sending something, and
// every request is answered.
func TestLiveness(t *testing.T) {
	r := &rng{env.SplitMix64{State: 42}}
	h := newHarness(t, slow{hover: 40, diags: 25})
	h.start(`{}`)
	h.open(uri, "x")
	h.drain()
	start := h.s.Now()
	end := start + 60*time.Second
	id, version := 0, 1
	for at := start; at < end; at += time.Duration(5+r.n(60)) *
		time.Millisecond {
		var msg string
		if r.n(2) == 0 {
			version++
			msg = fmt.Sprintf(`{"jsonrpc":"2.0","method":`+
				`"textDocument/didChange","params":{"textDocument":`+
				`{"uri":%q,"version":%d},"contentChanges":`+
				`[{"text":"v%d"}]}}`, uri, version, version)
		} else {
			id++
			msg = fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":`+
				`"textDocument/hover","params":{"textDocument":`+
				`{"uri":%q},"position":{"line":0,"character":0}}}`,
				id, uri)
		}
		h.s = h.s.PushAt(at, Message{Data: []byte(msg)}, PriorityClient)
	}
	id++
	h.s = h.s.PushAt(end, Message{Data: []byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"textDocument/hover",`+
			`"params":{"textDocument":{"uri":%q},"position":`+
			`{"line":0,"character":0}}}`, id, uri))}, PriorityClient)
	last := h.s.Now()
	for h.step() {
		if !h.s.Busy() {
			last = h.s.Now()
			continue
		}
		if n := len(h.sent); n > 0 && h.sent[n-1].at > last {
			last = h.sent[n-1].at
		}
		if h.s.Now()-last > NoProgress {
			t.Fatalf("no progress from %s to %s", last, h.s.Now())
		}
	}
	if h.s.Now() < end {
		t.Fatalf("the workload ended at %s, before %s", h.s.Now(), end)
	}
	got := h.responses()
	for i := 1; i <= id; i++ {
		if len(got[strconv.Itoa(i)]) != 1 {
			t.Fatalf("request %d: %d responses", i,
				len(got[strconv.Itoa(i)]))
		}
	}
}

// TestProgress: a request with a workDoneToken reports begin and end;
// diagnostics use a token the server creates once the client accepts.
func TestProgress(t *testing.T) {
	h := newHarness(t, slow{hover: 3, diags: 3})
	h.start(`{"window":{"workDoneProgress":true}}`)
	h.open(uri, "x")
	h.client(`{"jsonrpc":"2.0","id":1,"method":"textDocument/hover",`+
		`"params":{"textDocument":{"uri":%q},"position":`+
		`{"line":0,"character":0},"workDoneToken":"tok"}}`, uri)
	h.drain()
	var kinds []string
	var creates []string
	for _, s := range h.sent {
		switch m := s.msg.(type) {
		case *jsonrpc2.Notification:
			if m.Method == "$/progress" {
				kinds = append(kinds, string(m.Params))
			}
		case *jsonrpc2.Request:
			creates = append(creates, m.ID.String())
		}
	}
	want := []string{
		`{"token":"tok","value":{"kind":"begin",` +
			`"title":"textDocument/hover"}}`,
		`{"token":"tok","value":{"kind":"end"}}`,
	}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("progress %v, want %v", kinds, want)
	}
	if len(creates) == 0 {
		t.Fatal("no window/workDoneProgress/create for diagnostics")
	}
	// Accepting a token for running work begins its progress.
	h.version(2)
	h.step() // the change starts diagnostics; their first slice runs:
	h.step()
	n := len(h.sent)
	var create *jsonrpc2.Request
	for _, s := range h.sent {
		if r, ok := s.msg.(*jsonrpc2.Request); ok {
			create = r
		}
	}
	id, _ := create.ID.Int()
	h.client(`{"jsonrpc":"2.0","id":%d,"result":null}`, id)
	h.drain()
	found := false
	for _, s := range h.sent[n:] {
		if m, ok := s.msg.(*jsonrpc2.Notification); ok &&
			m.Method == "$/progress" {
			found = true
		}
	}
	if !found {
		t.Fatal("no progress after the client accepted the token")
	}
}

func (h *harness) version(v int) { h.change(uri, v, "v") }
