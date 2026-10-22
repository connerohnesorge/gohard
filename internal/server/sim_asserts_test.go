package server

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
)

// assertSim is fakeSim whose runs count the asserts a.
type assertSim struct {
	fakeSim
	a SimAsserts
}

func (s assertSim) Simulate(snap *Snapshot, tb string,
	seed uint64,
) Task[SimResult] {
	return mapTask(s.fakeSim.Simulate(snap, tb, seed),
		func(r SimResult) SimResult {
			a := s.a
			r.Report.Asserts = &a
			return r
		})
}

// TestSimLensAsserts: after a run that counted asserts, the re-run
// lens names the counts, and the run response carries them under
// "asserts" before "diagnostics" (#18).
func TestSimLensAsserts(t *testing.T) {
	h := &simHarness{harness: &harness{t: t, exit: -1},
		cfg: config.Defaults()}
	h.s = New(Options{Analyzer: logged{slow{hover: 1, diags: 1}, &h.log},
		Simulator: assertSim{fakeSim{2, &h.log},
			SimAsserts{Warning: 2, Error: 1}}, Version: "t"})
	h.start(`{"workspace":{"codeLens":{"refreshSupport":true}}}`)
	h.open(uri, tbText)
	h.drain()
	h.command(2, CmdRun, `{"testbench":"tb","seed":7}`)
	h.drain()
	want := []string{"▶ Re-run · seed 7 · 1 error, 2 warnings",
		"Copy repro"}
	if got := h.lenses(3); !slices.Equal(got, want) {
		t.Fatalf("lenses %q, want %q", got, want)
	}
	var keys map[string]json.RawMessage
	res := h.response(2).Result
	if err := json.Unmarshal(res, &keys); err != nil {
		t.Fatal(err)
	}
	if string(keys["asserts"]) !=
		`{"note":0,"warning":2,"error":1,"failure":0}` {
		t.Errorf("asserts %s", keys["asserts"])
	}
}

func TestAssertsSummary(t *testing.T) {
	for _, c := range []struct {
		a    SimAsserts
		want string
	}{
		{SimAsserts{}, ""},
		{SimAsserts{Note: 1}, "1 note"},
		{SimAsserts{Note: 2, Failure: 1, Warning: 3},
			"1 failure, 3 warnings, 2 notes"},
	} {
		if got := c.a.Summary(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a, got, c.want)
		}
	}
	if got := RerunAssertsTitle(3, nil); got != RerunTitle(3) {
		t.Errorf("no counts: %q", got)
	}
	if got := RerunAssertsTitle(3, &SimAsserts{}); got != RerunTitle(3) {
		t.Errorf("zero counts: %q", got)
	}
}

// TestSimReportKeyOrder: asserts sits between repro and diagnostics,
// null when nothing ran.
func TestSimReportKeyOrder(t *testing.T) {
	b, err := json.Marshal(NewSimReport("", nil, "", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"testbench":"","seed":null,"status":"","end":"",` +
		`"trace":"","repro":"","asserts":null,"diagnostics":[]}`
	if string(b) != want {
		t.Errorf("got %s\nwant %s", b, want)
	}
}
