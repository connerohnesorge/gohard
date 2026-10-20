package simrun

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
)

const raceTB = `entity tb is
end entity;

architecture sim of tb is
  signal s : integer := 0;
begin
  quiet : process begin wait; end process;
  w : process begin wait; end process;
  r : process begin s <= 1; wait; end process;
end architecture;
`

type fn struct{ f func(*sim.Ctx) error }

func (f fn) Clone() sim.Code { return f }
func (f fn) Resume(c *sim.Ctx) (sim.Wait, error) {
	return sim.Wait{}, f.f(c)
}

// code stands in for the interpreter: w sets a shared Go variable that
// r reads in the same delta, a race on in-delta order. racy false
// makes r ignore it.
func code(racy bool) CodeFunc {
	return func(d *elab.Design, p *elab.Proc) (sim.Code, error) {
		id, _ := d.Lookup("tb.s")
		t := d.Model.Signals[id].Type
		switch {
		case strings.HasSuffix(p.Path, ".w"):
			return fn{func(*sim.Ctx) error { sharedVar = 1; return nil }},
				nil
		case strings.HasSuffix(p.Path, ".r"):
			return fn{func(c *sim.Ctx) error {
				v := int64(5)
				if racy {
					v += sharedVar
				}
				sharedVar = 0
				return c.Assign(sim.Whole(id), []sim.WaveElem{{
					Value: eval.Int(t, v)}}, sim.Mechanism{})
			}}, nil
		}
		return fn{func(*sim.Ctx) error { return nil }}, nil
	}
}

var sharedVar int64

func raceOpts(t *testing.T, racy bool) Options {
	t.Helper()
	r := sema.Analyze(sema.Input{Files: []sema.Source{{Path: "tb.vhd",
		Text: []byte(raceTB)}}, Config: config.Defaults()})
	return Options{Result: r, Sim: config.Defaults().Simulation,
		Testbench: "tb", Code: code(racy)}
}

// TestRaces checks a seed-dependent design gives one sim/race warning
// naming w and r, and a race-free one gives none.
func TestRaces(t *testing.T) {
	seeds := []uint64{0, 1, 2, 3, 4, 5, 6, 7}
	ds := Races(raceOpts(t, true), seeds)
	if len(ds) != 1 || ds[0].Code != "sim/race" ||
		!strings.Contains(ds[0].Message, "tb.w and tb.r") {
		t.Fatalf("racy: %v", ds)
	}
	if ds := Races(raceOpts(t, false), seeds); len(ds) != 0 {
		t.Fatalf("race-free: %v", ds)
	}
}
