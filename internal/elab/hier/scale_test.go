package hier

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// TestTopGenericLocated: a bad default of a top's generic is located
// at the default in the entity's file, so it can be filtered by file
// and published (no instantiation to anchor it at).
func TestTopGenericLocated(t *testing.T) {
	src := "entity e is generic (g : positive := 0); end;\n" +
		"architecture a of e is begin end;\n"
	r := analyze(t, map[string]string{"a.vhd": src})
	h := Build(r, Options{})
	if len(h.Diagnostics) != 1 {
		t.Fatalf("diagnostics %v", h.Diagnostics)
	}
	d := h.Diagnostics[0]
	col := strings.Index(src, ":= 0") + 4
	if d.Path != "a.vhd" || d.Line != 1 || d.Col != col ||
		d.Code != "sema/static-error" {
		t.Fatalf("%s:%d:%d %s %s, want a.vhd:1:%d", d.Path, d.Line,
			d.Col, d.Code, d.Message, col)
	}
	// Every diagnostic of a build carries a file.
	for _, d := range h.Diagnostics {
		if d.Path == "" || d.Line == 0 {
			t.Errorf("unlocated %v", d)
		}
	}
}

// TestSelfRecursiveTop: an entity instantiated only by itself is the
// top a user elaborates, so its per-instance errors are found.
func TestSelfRecursiveTop(t *testing.T) {
	r := analyze(t, map[string]string{"a.vhd": `
entity leaf is generic (W : positive := 1); end;
architecture a of leaf is begin end;
entity r is generic (n : natural := 3); end;
architecture a of r is
begin
  g: if n > 0 generate
    u: entity work.r generic map (n => n - 1);
  end generate;
  l: entity work.leaf generic map (W => n);
end;
entity p is end;
entity q is end;
architecture a of p is begin u: entity work.q; end;
architecture a of q is begin u: entity work.p; end;
`})
	h := Build(r, Options{MaxDepth: 8})
	var tops []string
	for _, in := range h.Tops {
		tops = append(tops, in.Path)
	}
	// leaf is used by r; of the cycle p <-> q the first is the top.
	if !slices.Equal(tops, []string{"r", "p"}) {
		t.Fatalf("tops %v", tops)
	}
	found := false
	for _, d := range h.Diagnostics {
		if d.Code == "sema/static-error" &&
			strings.Contains(d.Message, "r.g.u.g.u.g.u.l") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no out-of-range W at the innermost leaf: %v",
			h.Diagnostics)
	}
}

// heavy has a constant that exceeds the step budget in every leaf;
// dep says whether it depends on the leaf's generic.
func heavy(n int, dep bool) map[string]string {
	arg := "200000"
	if dep {
		arg = "200000 + k"
	}
	return map[string]string{"a.vhd": `
entity leaf is generic (k : natural := 0); end;
architecture a of leaf is
  function heavy(n : natural) return natural is
    variable s : natural := 0;
  begin
    for i in 1 to n loop s := (s + i) mod 7; end loop;
    return s;
  end function;
  constant c : natural := heavy(` + arg + `);
begin end;
entity top is end;
architecture a of top is
begin
  g: for i in 0 to ` + itoa(int64(n-1)) + ` generate
    u: entity work.leaf generic map (k => i);
  end generate;
end;
`}
}

// TestInstanceCostBounded: the per-instance work for a constant does
// not grow with the instance count when the constant does not depend
// on the instance (evaluated once), and a dependent one that exceeds
// the limit gives one warning, every instance being incomplete.
func TestInstanceCostBounded(t *testing.T) {
	const steps = 20000
	for _, dep := range []bool{false, true} {
		var spent [2]int64
		for i, n := range []int{1, 200} {
			r := analyzeAny(heavy(n, dep))
			h := Build(r, Options{Steps: steps})
			spent[i] = h.Steps
			warn := 0
			for _, d := range h.Diagnostics {
				if d.Code == "sema/eval-limit" {
					warn++
				}
			}
			if dep && (warn != 1 || len(h.Incomplete) != n) {
				t.Errorf("dep, %d leaves: %d warnings, incomplete %d",
					n, warn, len(h.Incomplete))
			}
			if !dep && warn != 0 {
				t.Errorf("independent, %d leaves: %v", n, h.Diagnostics)
			}
		}
		if spent[1] > spent[0]+2*steps {
			t.Errorf("dep=%v: 1 leaf %d steps, 200 leaves %d steps",
				dep, spent[0], spent[1])
		}
	}
}

// TestTotalSteps: the total budget stops per-instance evaluation and
// warns once, located, listing the remaining instances incomplete.
func TestTotalSteps(t *testing.T) {
	src := heavy(50, true)
	// Each leaf's constant fits in the per-evaluation budget.
	src["a.vhd"] = strings.Replace(src["a.vhd"], "200000 + k",
		"500 + k", 1)
	r := analyze(t, src)
	h := Build(r, Options{TotalSteps: 20000})
	if len(h.Diagnostics) != 1 || h.Diagnostics[0].Path != "a.vhd" ||
		!strings.Contains(h.Diagnostics[0].Message,
			"the hierarchy used its total of 20000 steps") {
		t.Fatalf("diagnostics %v", h.Diagnostics)
	}
	if len(h.Incomplete) == 0 || len(h.Incomplete) >= 50 {
		t.Fatalf("incomplete %v", h.Incomplete)
	}
	if h.Steps > 20000+eval.EditorSteps {
		t.Fatalf("spent %d", h.Steps)
	}
}

// analyzeAny analyzes files that may have unit-level diagnostics.
func analyzeAny(files map[string]string) *sema.Result {
	var srcs []sema.Source
	for _, p := range slices.Sorted(maps.Keys(files)) {
		srcs = append(srcs, sema.Source{Path: p, Text: []byte(files[p])})
	}
	return sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
}
