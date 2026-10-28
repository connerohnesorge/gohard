package corpus

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/simrun"
)

// racyTB: w and r share a protected counter in the same delta, so the
// value r drives depends on in-delta process order.
const racyTB = `entity tb is end entity;
architecture sim of tb is
  type counter is protected
    procedure set(x : integer);
    impure function get return integer;
  end protected;
  type counter is protected body
    variable v : integer := 0;
    procedure set(x : integer) is begin v := x; end procedure;
    impure function get return integer is begin return v; end function;
  end protected body;
  shared variable c : counter;
  signal s : integer := 0;
begin
  w : process begin c.set(1); wait; end process;
  r : process begin s <= 5 + c.get; wait; end process;
end architecture;
`

// TestRacesHarness checks the harness's seed comparison reports a
// seed-dependent design as sim/race naming both processes, through the
// real interpreter.
func TestRacesHarness(t *testing.T) {
	r := Run{Design: Design{Name: "racy", Testbench: "tb"},
		Standard: "2008", Paths: []string{"tb.vhd"}}
	src := map[string][]byte{"tb.vhd": []byte(racyTB)}
	seeds := []uint64{0, 1, 2, 3, 4, 5, 6, 7}
	ds := Races(r, src, simrun.DefaultCode, seeds)
	if len(ds) != 1 || ds[0].Code != "sim/race" ||
		!strings.Contains(ds[0].Message, "tb.w") ||
		!strings.Contains(ds[0].Message, "tb.r") {
		t.Fatalf("racy: %v", ds)
	}
	fixed := strings.Replace(racyTB, "5 + c.get", "5", 1)
	src["tb.vhd"] = []byte(fixed)
	if ds := Races(r, src, simrun.DefaultCode, seeds); len(ds) != 0 {
		t.Fatalf("race-free: %v", ds)
	}
}
