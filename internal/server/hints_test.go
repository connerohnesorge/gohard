package server

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
)

// hinter is an analyzer with signature help and inlay hints: one
// signature whose label holds a non-ASCII parameter name, and one
// hint naming the snapshot, after slices slices.
type hinter struct {
	Nop
	slices int
}

func (hinter) Features() Features {
	return Features{Set: FeatureSignatureHelp | FeatureInlayHints,
		SignatureTriggers: []string{"(", ","}}
}

func (hinter) SignatureHelp(Query) Task[*SignatureHelp] {
	// "f(é : in t; 𝔸 : out t)": é is 2 bytes and 1 UTF-16 unit, 𝔸
	// 4 bytes and 2 units.
	l := "f(é : in t; 𝔸 : out t)"
	return Done(&SignatureHelp{Signatures: []Signature{{Label: l,
		Params: []Span{{2, 11}, {13, 25}}, Active: 1}}})
}

func (h hinter) InlayHints(snap *Snapshot, _ string,
	r Span,
) Task[[]InlayHint] {
	return Steps(h.slices, []InlayHint{{Offset: r.Start,
		Label: "snapshot " + strconv.FormatUint(snap.ID, 10),
		Kind:  HintType}})
}

func TestSignatureHelpEncodings(t *testing.T) {
	for _, c := range []struct {
		caps, want string
	}{
		{`{"general":{"positionEncodings":["utf-8"]}}`,
			`[{"label":[2,11]},{"label":[13,25]}]`},
		{`{}`, `[{"label":[2,10]},{"label":[12,22]}]`},
	} {
		h := newHarness(t, hinter{slices: 1})
		h.start(c.caps)
		h.open(uri, "x")
		h.drain()
		h.client(`{"jsonrpc":"2.0","id":1,"method":`+
			`"textDocument/signatureHelp","params":{"textDocument":`+
			`{"uri":%q},"position":{"line":0,"character":0}}}`, uri)
		h.drain()
		r := h.responses()["1"]
		if len(r) != 1 || r[0].Error != nil {
			t.Fatalf("%s: %v", c.caps, r)
		}
		var got struct {
			Signatures []struct {
				Parameters      json.RawMessage
				ActiveParameter int
			}
			ActiveParameter int
		}
		if err := json.Unmarshal(r[0].Result, &got); err != nil {
			t.Fatal(err)
		}
		if string(got.Signatures[0].Parameters) != c.want ||
			got.Signatures[0].ActiveParameter != 1 ||
			got.ActiveParameter != 1 {
			t.Errorf("%s: %s", c.caps, r[0].Result)
		}
	}
}

// TestInlayHintRefresh: a change of the inlay_hints settings asks a
// client that supports it to request its hints again; other settings,
// clients without the capability and servers without inlay hints get
// no refresh.
func TestInlayHintRefresh(t *testing.T) {
	refresh := func(h *harness) int {
		n := 0
		for _, s := range h.sent {
			if r, ok := s.msg.(*jsonrpc2.Request); ok &&
				r.Method == "workspace/inlayHint/refresh" {
				n++
			}
		}
		return n
	}
	set := func(h *harness, settings string) {
		h.client(`{"jsonrpc":"2.0","method":`+
			`"workspace/didChangeConfiguration","params":`+
			`{"settings":{"gohard":%s}}}`, settings)
		h.drain()
	}
	h := newHarness(t, hinter{slices: 1})
	h.start(`{"workspace":{"inlayHint":{"refreshSupport":true}}}`)
	h.open(uri, "x")
	h.drain()
	for i, c := range []struct {
		settings string
		want     int
	}{
		{`{"inlay_hints":{"types":true}}`, 1},
		{`{"inlay_hints":{"types":true}}`, 1},
		{`{"inlay_hints":{"types":true},"format":{"indent":4}}`, 1},
		{`{"inlay_hints":{"values":true}}`, 2},
		{`{}`, 3},
	} {
		set(h, c.settings)
		if got := refresh(h); got != c.want {
			t.Errorf("step %d (%s): %d refreshes, want %d", i,
				c.settings, got, c.want)
		}
	}
	h = newHarness(t, hinter{slices: 1})
	h.start(`{}`)
	h.drain()
	set(h, `{"inlay_hints":{"types":true}}`)
	if n := refresh(h); n != 0 {
		t.Errorf("without refreshSupport: %d refreshes", n)
	}
	h = newHarness(t, Nop{})
	h.start(`{"workspace":{"inlayHint":{"refreshSupport":true}}}`)
	h.drain()
	set(h, `{"inlay_hints":{"types":true}}`)
	if n := refresh(h); n != 0 {
		t.Errorf("without the inlay hints feature: %d refreshes", n)
	}
}

// TestInlayHintsNeverStale: under any interleaving of inlay hint
// requests, edits and slices, every request gets one response, and
// hints are only ever sent for the latest snapshot (P3).
func TestInlayHintsNeverStale(t *testing.T) {
	answered, modified := 0, 0
	for seed := range uint64(200) {
		r := &rng{env.SplitMix64{State: seed}}
		h := newHarness(t, hinter{slices: 1 + r.n(4)})
		h.start(`{}`)
		h.open(uri, "x")
		h.drain()
		next, version := 1, 1
		for range 50 {
			switch r.n(4) {
			case 0:
				h.client(`{"jsonrpc":"2.0","id":%d,"method":`+
					`"textDocument/inlayHint","params":{"textDocument":`+
					`{"uri":%q},"range":{"start":{"line":0,`+
					`"character":0},"end":{"line":9,"character":0}}}}`,
					next, uri)
				next++
			case 1:
				version++
				h.change(uri, version, "v"+strconv.Itoa(version))
			default:
				h.step()
			}
		}
		h.drain()
		got := h.responses()
		for id := 1; id < next; id++ {
			if n := len(got[strconv.Itoa(id)]); n != 1 {
				t.Fatalf("seed %d: request %d got %d responses", seed,
					id, n)
			}
		}
		for _, s := range h.sent {
			res, ok := s.msg.(*jsonrpc2.Response)
			if ok && res.Error != nil &&
				res.Error.Code == jsonrpc2.CodeContentModified {
				modified++
			}
			if !ok || res.Error != nil || res.ID.String() == `"init"` {
				continue
			}
			answered++
			var hs []struct{ Label string }
			if err := json.Unmarshal(res.Result, &hs); err != nil {
				t.Fatal(err)
			}
			want := "snapshot " + strconv.FormatUint(s.snap, 10)
			if len(hs) != 1 || hs[0].Label != want {
				t.Fatalf("seed %d: %s sent at snapshot %d", seed,
					res.Result, s.snap)
			}
		}
	}
	if answered == 0 || modified == 0 {
		t.Fatalf("%d answered, %d ContentModified: the interleavings "+
			"miss a case", answered, modified)
	}
}
