package ide

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/resolve"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/span"
)

// allHints turns every inlay hint kind on in gohard.yaml.
const allHints = "\ninlay_hints:\n  parameter_names: true\n" +
	"  types: true\n  values: true\n"

// withHints returns p with every inlay hint kind on.
func withHints(p project) project {
	q := maps.Clone(p)
	q["gohard.yaml"] = append(slices.Clone(p["gohard.yaml"]), allHints...)
	return q
}

// TestInlayHintsCorpus: on every corpus design with every kind on, the
// inlay hints of a snapshot are the same whoever computes them (two
// fresh analyzers, sequential and parallel jobs, the Analyzer task and
// the query layer), each hint sits on a token boundary of its file,
// and the hints of two halves of a file are the hints of the whole
// (#56, P1).
func TestInlayHintsCorpus(t *testing.T) {
	kinds := map[server.InlayHintKind]int{}
	values := 0
	for _, name := range slices.Sorted(maps.Keys(corpus(t))) {
		p := withHints(corpus(t)[name])
		snap := snapshotOf(t, p, nil)
		if k := HintKinds(snap); !k.ParameterNames || !k.Types ||
			!k.Values {
			t.Fatalf("%s: the inlay_hints keys are not on: %+v", name, k)
		}
		seq := analyze(t, snap)
		jobs := simenv.New(simenv.Options{Seed: 3}).Env().Jobs
		par, _ := run(New(Options{Jobs: jobs, Limit: 16}).Analysis(snap))
		for _, d := range Files(snap) {
			whole := server.Span{End: len(d.Text())}
			got := seq.InlayHintsIn(d.URI, whole, HintKinds(snap))
			if again := par.InlayHintsIn(d.URI, whole,
				HintKinds(snap)); !reflect.DeepEqual(got, again) {
				t.Errorf("%s: hints differ between analyses", d.Rel)
			}
			task, _ := run(New(Options{}).InlayHints(snap, d.URI, whole))
			if !reflect.DeepEqual(got, task) {
				t.Errorf("%s: the task gives other hints", d.Rel)
			}
			mid := len(d.Text()) / 2
			half := append(seq.InlayHintsIn(d.URI,
				server.Span{End: mid - 1}, HintKinds(snap)),
				seq.InlayHintsIn(d.URI, server.Span{Start: mid,
					End: whole.End}, HintKinds(snap))...)
			if !reflect.DeepEqual(got, half) {
				t.Errorf("%s: the halves give other hints", d.Rel)
			}
			f, ok := seq.Result.File(d.Rel)
			if !ok {
				t.Fatalf("%s: not analyzed", d.Rel)
			}
			bounds := map[int]bool{}
			for tk := range f.Root().DescendantTokens() {
				if !tk.Missing() {
					bounds[tk.Span().Start] = true
					bounds[tk.Span().End] = true
				}
			}
			for _, h := range got {
				if !bounds[h.Offset] {
					t.Errorf("%s:%d: hint %q is not on a token boundary",
						d.Rel, h.Offset, h.Label)
				}
				kinds[h.Kind]++
				if h.Kind == 0 {
					values++
				}
			}
		}
	}
	t.Logf("hints: %d parameter, %d type, %d value",
		kinds[server.HintParameter], kinds[server.HintType], values)
	if kinds[server.HintParameter] == 0 || kinds[server.HintType] == 0 ||
		values == 0 {
		t.Fatal("the corpus does not exercise every hint kind")
	}
}

// TestInlayHintsOff: with no inlay_hints key the analyzer answers at
// once with no hint, without analyzing.
func TestInlayHintsOff(t *testing.T) {
	p := corpus(t)["overload_ops"]
	snap := snapshotOf(t, p, nil)
	d := Files(snap)[0]
	hs, slices := run(New(Options{}).InlayHints(snap, d.URI,
		server.Span{End: len(d.Text())}))
	if len(hs) != 0 || slices != 1 {
		t.Fatalf("%d hints in %d slices", len(hs), slices)
	}
}

// hintsSession is a language server session over the real analyzer
// whose messages are recorded with the latest snapshot at the time
// each was sent.
type hintsSession struct {
	t    *testing.T
	d    *server.Driver
	sent []sentMsg
	next int
}

type sentMsg struct {
	msg  jsonrpc2.Message
	snap *server.Snapshot
}

func newHintsSession(t *testing.T, p project) *hintsSession {
	files := map[string][]byte{}
	for k, v := range p {
		files["/work/"+k] = v
	}
	sim := simenv.New(simenv.Options{Files: files, Dir: "/work"})
	s := &hintsSession{t: t}
	s.d = server.NewDriver(server.Options{
		Analyzer: New(Options{ToolVersion: "test"})}, sim.Env(), 1,
		func(m server.Send) error {
			s.sent = append(s.sent, sentMsg{m.Msg, s.d.State.Snapshot()})
			return nil
		})
	s.push(map[string]any{"jsonrpc": "2.0", "id": "init",
		"method": "initialize", "params": map[string]any{
			"processId": nil, "rootUri": "file:///work",
			"capabilities": map[string]any{"general": map[string]any{
				"positionEncodings": []string{"utf-8"}}}}})
	s.push(map[string]any{"jsonrpc": "2.0", "method": "initialized",
		"params": map[string]any{}})
	s.run()
	return s
}

func (s *hintsSession) push(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.t.Fatal(err)
	}
	s.d.Push(b)
}

func (s *hintsSession) run() {
	if err := s.d.Run(); err != nil {
		s.t.Fatal(err)
	}
}

// TestInlayHintsNeverStaleCorpus: in sessions over the real analyzer
// on every corpus design, inlay hint requests interleaved at random
// with edits, slices and runs to idle each get one response, and every
// hint result is the hints of the latest snapshot at the time it is
// sent: no hint is published for a superseded snapshot (#56, P3). The
// runs to idle answer requests partway through a session, so one
// session answers at several snapshots, the case where hints kept from
// an older snapshot would be sent.
func TestInlayHintsNeverStaleCorpus(t *testing.T) {
	answered, modified, multi := 0, 0, 0
	for i, name := range slices.Sorted(maps.Keys(corpus(t))) {
		p := withHints(corpus(t)[name])
		var vhd []string
		for k := range p {
			if strings.HasSuffix(k, ".vhd") {
				vhd = append(vhd, k)
			}
		}
		slices.Sort(vhd)
		if len(vhd) == 0 {
			continue
		}
		for seed := range uint64(2) {
			// Each design and seed has its own operations.
			r := env.SplitMix64{State: uint64(i)<<1 | seed}
			s := newHintsSession(t, p)
			rel := vhd[env.Intn(&r, len(vhd))]
			text := string(p[rel])
			u := uri(rel)
			s.push(map[string]any{"jsonrpc": "2.0",
				"method": "textDocument/didOpen", "params": map[string]any{
					"textDocument": map[string]any{"uri": u,
						"languageId": "vhdl", "version": 1, "text": text}}})
			s.run()
			ids, version := 0, 1
			for range 24 {
				switch env.Intn(&r, 5) {
				case 0:
					ids++
					s.push(map[string]any{"jsonrpc": "2.0", "id": ids,
						"method": "textDocument/inlayHint",
						"params": map[string]any{
							"textDocument": map[string]any{"uri": u},
							"range": map[string]any{
								"start": map[string]int{"line": 0,
									"character": 0},
								"end": map[string]int{"line": 1 << 20,
									"character": 0}}}})
				case 1:
					// A blank line at the start of a random line
					// moves every hint below it.
					lines := strings.SplitAfter(text, "\n")
					i := env.Intn(&r, len(lines))
					lines[i] = "\n" + lines[i]
					text = strings.Join(lines, "")
					version++
					s.push(map[string]any{"jsonrpc": "2.0",
						"method": "textDocument/didChange",
						"params": map[string]any{
							"textDocument": map[string]any{"uri": u,
								"version": version},
							"contentChanges": []any{
								map[string]any{"text": text}}}})
				case 2:
					s.run()
				default:
					for range 1 + env.Intn(&r, 3) {
						if err := s.d.Step(); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			s.run()
			got := map[string]int{}
			snaps := map[uint64]bool{}
			for _, m := range s.sent {
				res, ok := m.msg.(*jsonrpc2.Response)
				if !ok || res.ID.String() == `"init"` {
					continue
				}
				got[res.ID.String()]++
				if res.Error != nil {
					if res.Error.Code != jsonrpc2.CodeContentModified {
						t.Fatalf("%s: %s", name, res.Error.Message)
					}
					modified++
					continue
				}
				answered++
				snaps[m.snap.ID] = true
				checkHints(t, name+" "+rel, m.snap, u, res.Result)
			}
			if len(snaps) > 1 {
				multi++
			}
			for id := 1; id <= ids; id++ {
				if n := got[fmt.Sprint(id)]; n != 1 {
					t.Fatalf("%s: request %d got %d responses", name, id, n)
				}
			}
		}
	}
	t.Logf("%d answered, %d ContentModified, %d sessions answer at "+
		"more than one snapshot", answered, modified, multi)
	if answered == 0 || modified == 0 || multi == 0 {
		t.Fatal("the sessions miss a case")
	}
}

// checkHints compares an inlay hint result with the hints a fresh
// analysis of snap gives for the document u.
func checkHints(t *testing.T, at string, snap *server.Snapshot, u string,
	result json.RawMessage,
) {
	t.Helper()
	d, ok := snap.Document(u)
	if !ok {
		t.Fatalf("%s: no document in snapshot %d", at, snap.ID)
	}
	var got []struct {
		Position struct{ Line, Character int }
		Label    string
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	want := analyze(t, snap).InlayHintsIn(u,
		server.Span{End: len(d.Text())}, HintKinds(snap))
	x := span.NewIndex(d.Text())
	if len(got) != len(want) {
		t.Fatalf("%s: %d hints sent at snapshot %d, want %d", at,
			len(got), snap.ID, len(want))
	}
	for i, w := range want {
		p := x.Position(w.Offset, span.UTF8)
		g := got[i]
		if g.Position.Line != p.Line || g.Position.Character != p.Col ||
			g.Label != w.Label {
			t.Fatalf("%s: hint %d is %d:%d %q at snapshot %d, want "+
				"%d:%d %q", at, i, g.Position.Line, g.Position.Character,
				g.Label, snap.ID, p.Line, p.Col, w.Label)
		}
	}
}

// TestSignatureHelpCorpus: at every byte offset of every corpus file,
// signature help is the same from two fresh analyses (sequential and
// parallel jobs), and every answer is well formed: at least one
// signature, each parameter a non-empty range of its label after the
// previous one, the active parameter one of them or none (#56, P1).
func TestSignatureHelpCorpus(t *testing.T) {
	answered := 0
	for _, name := range slices.Sorted(maps.Keys(corpus(t))) {
		snap := snapshotOf(t, corpus(t)[name], nil)
		seq := analyze(t, snap)
		jobs := simenv.New(simenv.Options{Seed: 5}).Env().Jobs
		par, _ := run(New(Options{Jobs: jobs, Limit: 16}).Analysis(snap))
		for _, d := range Files(snap) {
			for off := range len(d.Text()) + 1 {
				q := server.Query{Snap: snap, URI: d.URI, Offset: off}
				h := seq.SignatureHelpAt(q)
				if again := par.SignatureHelpAt(q); !reflect.DeepEqual(h,
					again) {
					t.Fatalf("%s:%d: signature help differs between "+
						"analyses", d.Rel, off)
				}
				if h == nil {
					continue
				}
				answered++
				if len(h.Signatures) == 0 {
					t.Fatalf("%s:%d: no signature", d.Rel, off)
				}
				for _, s := range h.Signatures {
					end := 0
					for _, p := range s.Params {
						if p.Start < end || p.End <= p.Start ||
							p.End > len(s.Label) {
							t.Fatalf("%s:%d: parameter %v of %q", d.Rel,
								off, p, s.Label)
						}
						end = p.End
					}
					if s.Active < -1 || s.Active >= len(s.Params) {
						t.Fatalf("%s:%d: active parameter %d of %q",
							d.Rel, off, s.Active, s.Label)
					}
				}
			}
		}
	}
	t.Logf("%d offsets with signature help", answered)
	if answered == 0 {
		t.Fatal("no signature help on the corpus")
	}
}

// TestSignatureHelpTask: the analyzer's signature help is resolve's,
// with the label offsets of each parameter, on a call of the
// overload_ops design resolved by its result type.
func TestSignatureHelpTask(t *testing.T) {
	p := corpus(t)["overload_ops"]
	snap := snapshotOf(t, p, nil)
	a := New(Options{})
	if !a.Features().Has(server.FeatureSignatureHelp |
		server.FeatureInlayHints) {
		t.Fatal("no signature help or inlay hints feature")
	}
	if !slices.Equal(a.Features().SignatureTriggers, []string{"(", ","}) {
		t.Fatalf("triggers %q", a.Features().SignatureTriggers)
	}
	text := string(p["overload_ops.vhd"])
	off := strings.Index(text, "neg_i <= -a;") + len("neg_i <= -")
	q := server.Query{Snap: snap, URI: uri("overload_ops.vhd"),
		Offset: off}
	h, _ := run(a.SignatureHelp(q))
	if h == nil || len(h.Signatures) == 0 {
		t.Fatal("no signature help")
	}
	an := analyze(t, snap)
	sigs, _ := resolve.SignatureHelp(an.Result, "overload_ops.vhd", off)
	if len(sigs) != len(h.Signatures) {
		t.Fatalf("%d signatures, resolve has %d", len(h.Signatures),
			len(sigs))
	}
	first := h.Signatures[0]
	if first.Label != `function "-"(a : word_t) return INTEGER` ||
		first.Active != 0 || len(first.Params) != 1 ||
		first.Label[first.Params[0].Start:first.Params[0].End] !=
			"a : word_t" {
		t.Fatalf("first signature %+v", first)
	}
}
