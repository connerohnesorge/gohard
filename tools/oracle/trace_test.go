package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/simrun"
)

const traceManifest = `schema: 1
designs:
  - name: demo
    standard: "2008"
    files:
      - path: a.vhd
        standard: "2008"
    testbench: tb
    oracle: both
    disagreements: []
    trace_sha256: pending
    oracle_sha256: pending
regression: []
`

const demoVCD = `$timescale 1 fs $end
$scope module tb $end
$var reg 1 ! clk $end
$scope module dut $end
$var reg 2 " q[1:0] $end
$upscope $end
$upscope $end
$enddefinitions $end
#0
0!
b00 "
#5
1!
b01 "
`

// traceTools fake GHDL and NVC runs: the run step writes dump.vcd
// (NVC's with lower-case values), the analysis step does nothing.
func traceTools(vcd map[string]string) *toolset {
	fake := func(name string) func(string, []string) (string, int) {
		return func(dir string, args []string) (string, int) {
			if slices.Contains(args, "--elab-run") ||
				slices.Contains(args, "-r") {
				os.WriteFile(filepath.Join(dir, "dump.vcd"),
					[]byte(vcd[name]), 0o644)
			}
			return "", 0
		}
	}
	return &toolset{found: map[string]*tool{
		"ghdl": {name: "ghdl", version: "6.0.0", fake: fake("ghdl")},
		"nvc":  {name: "nvc", version: "1.23.0", fake: fake("nvc")},
	}}
}

func traceRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "testdata", "corpus", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, filepath.FromSlash(manifestPath)),
		traceManifest)
	write(t, filepath.Join(dir, "a.vhd"), "entity tb is end;\n")
	return root
}

// TestTraceSection: record, check, hash, a GHDL/NVC disagreement, a
// recorded disagreement that resolves it, and staleness.
func TestTraceSection(t *testing.T) {
	saved := simrun.DefaultCode
	simrun.DefaultCode = nil // compare the recordings only
	t.Cleanup(func() { simrun.DefaultCode = saved })
	root := traceRepo(t)
	sel := []*section{traceSection}
	upd := func(vcd map[string]string) {
		t.Helper()
		var out bytes.Buffer
		if err := doUpdate(root, sel, nil, traceTools(vcd),
			&out); err != nil {
			t.Fatal(err)
		}
	}
	chk := func() []string {
		t.Helper()
		p, err := doCheck(root, sel, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	upd(map[string]string{"ghdl": demoVCD,
		"nvc": strings.ReplaceAll(demoVCD, "b00", "b0")})
	if p := chk(); len(p) > 0 {
		t.Fatalf("after update: %q", p)
	}
	g := readFile(t, root, "testdata/corpus/demo/golden/oracle.nvc.txt")
	want := "gohard-oracle 1\ntool nvc 1.23.0\n"
	if !strings.HasPrefix(g, want) || !strings.Contains(g,
		"signal tb.dut.q logic 2\n") || !strings.Contains(g,
		"5 tb.dut.q 01\n") {
		t.Fatalf("golden:\n%s", g)
	}
	man := readFile(t, root, manifestPath)
	if strings.Contains(man, "oracle_sha256: pending") {
		t.Fatalf("hash not written:\n%s", man)
	}

	// NVC differs on tb.dut.q: a problem until a disagreement that
	// follows GHDL names the signal.
	upd(map[string]string{"ghdl": demoVCD,
		"nvc": strings.Replace(demoVCD, "b01", "bu1", 1)})
	if p := chk(); !hasProblem(p, "GHDL and NVC disagree on tb.dut.q") {
		t.Fatalf("disagreement: %q", p)
	}
	man = readFile(t, root, manifestPath)
	write(t, filepath.Join(root, filepath.FromSlash(manifestPath)),
		strings.Replace(man, "    disagreements: []\n",
			"    disagreements:\n      - topic: q\n        follows: ghdl\n"+
				"        signals: [tb.dut.q]\n", 1))
	if p := chk(); len(p) > 0 {
		t.Fatalf("recorded disagreement: %q", p)
	}

	// A changed source makes the recordings stale.
	write(t, filepath.Join(root, "testdata", "corpus", "demo", "a.vhd"),
		"entity tb is end entity;\n")
	if p := chk(); !hasProblem(p, "the sources changed") {
		t.Fatalf("stale: %q", p)
	}
}

const ghdlTree = `hier_generate_tb [entity]
` + "`" + `-sim [arch]
  +-din [signal]
  +-dut [instance]
  | ` + "`" + `-hier_generate [entity]
  |   +-din [port in]
  |   ` + "`" + `-rtl [arch]
  |     +-lanes(0) [for-generate]
  |     | ` + "`" + `-u [instance]
  |     |   ` + "`" + `-hier_generate_lane [entity]
  |     |     ` + "`" + `-rtl [arch]
  |     |       ` + "`" + `-polarity [if-generate true]
  |     |         ` + "`" + `-P0 [process]
  |     ` + "`" + `-tap [if-generate true]
  |       ` + "`" + `-P0 [process]
  ` + "`" + `-check [process]
std.standard [package]
`

func TestParseGHDLTree(t *testing.T) {
	got, err := parseGHDLTree(ghdlTree, "hier_generate_tb")
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, n := range got {
		s = append(s, n.path+" "+n.kind)
	}
	want := []string{
		"hier_generate_tb instance",
		"hier_generate_tb.dut instance",
		"hier_generate_tb.dut.lanes(0) for-generate",
		"hier_generate_tb.dut.lanes(0).u instance",
		"hier_generate_tb.dut.lanes(0).u.polarity if-generate",
		"hier_generate_tb.dut.tap if-generate",
	}
	if !slices.Equal(s, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(s, "\n"),
			strings.Join(want, "\n"))
	}
	if _, err := parseGHDLTree(ghdlTree, "other_tb"); err == nil {
		t.Fatal("wrong top accepted")
	}
}
