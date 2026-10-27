package corpus

import (
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/simrun"
	"github.com/connerohnesorge/gohard/internal/token"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// The corpus harness (#8, #24, #35, #36): every design of corpus.yaml,
// its listed variants and its generated #8 variants run the way
// `gohard check --sim --tb <testbench>` runs them, and must give the
// recorded outcome. Pull requests run seeds 0-3; the nightly job runs
// -corpus.seeds=64 (scripts/ci/corpus-seeds.sh).
var (
	seeds = flag.Int("corpus.seeds", 4,
		"seeds per design (0..n-1); nightly: 64")
	requireSim = flag.Bool("corpus.require-sim", false,
		"fail designs that cannot simulate yet (no interpreter); "+
			"set by scripts/ci/corpus-seeds.sh")
	only = flag.String("corpus.design", "",
		"comma-separated designs to run")
)

// repoRoot is the module root, two levels above this package.
func repoRoot(t testing.TB) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func reader(root string) ReadFunc {
	return func(rel string) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	}
}

func loadManifest(t testing.TB) (*Manifest, ReadFunc) {
	t.Helper()
	read := reader(repoRoot(t))
	src, err := read(ManifestPath)
	if err != nil {
		t.Fatal(Missing(ManifestPath, err))
	}
	m, err := ParseManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	return m, read
}

// TestManifest checks the design list: the 14 designs of #8 and the
// #27 gc_heap design, sorted, each with a directory, and every file of
// every run present (a missing file names git submodule update --init,
// never a skip).
func TestManifest(t *testing.T) {
	m, read := loadManifest(t)
	want := []string{"assert_fail", "budget_hang", "force_ext",
		"gc_heap", "hier_generate", "md_resolve", "mode_views",
		"overload_ops", "postponed_attrs", "prot_cond", "psl_simple",
		"standin_model", "textio_env", "vhdl93_only", "wait_forms"}
	var got []string
	for _, d := range m.Designs {
		got = append(got, d.Name)
		for _, r := range d.Runs() {
			if _, err := r.Sources(read); err != nil {
				t.Errorf("%s: %v", r.Name(), err)
			}
		}
		if !d.Has("variant_stable") {
			t.Errorf("%s does not claim variant_stable", d.Name)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("designs %v, want %v", got, want)
	}
	for _, r := range m.Regression {
		if _, err := os.Stat(filepath.Join(repoRoot(t),
			filepath.FromSlash(r.Path))); err != nil {
			t.Errorf("regression %s: %v", r.Name, Missing(r.Path, err))
		}
	}
}

// TestMissingFile: a missing corpus or submodule file is a failure
// whose message names git submodule update --init.
func TestMissingFile(t *testing.T) {
	r := Run{Design: Design{Name: "x"}, Paths: []string{"gone.vhd"}}
	_, err := r.Sources(func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	})
	if err == nil || !strings.Contains(err.Error(),
		"git submodule update --init") {
		t.Fatalf("err = %v", err)
	}
}

func selectedDesign(name string) bool {
	return *only == "" || slices.Contains(strings.Split(*only, ","), name)
}

// TestCorpus runs every design (no design is skipped): the main run
// and each listed variant at every seed, the generated variants at
// seed 0.
func TestCorpus(t *testing.T) {
	m, read := loadManifest(t)
	for _, d := range m.Designs {
		if !selectedDesign(d.Name) {
			continue
		}
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()
			for _, r := range d.Runs() {
				runDesign(t, r, read)
			}
		})
	}
}

// pending reports a run that stops before its simulation for want of
// process code: a failure under -corpus.require-sim or once an
// interpreter is registered, a log line otherwise. The analysis and elaboration parts were checked.
func pending(t *testing.T, r Run, res *Result) {
	t.Helper()
	msg := fmt.Sprintf("%s: simulation pending (processes without "+
		"code: %v)", r.Name(), res.Pending)
	if *requireSim || simrun.DefaultCode != nil {
		// With an interpreter registered, a process without code is
		// a failure in every run.
		t.Error(msg)
		return
	}
	t.Log(msg)
}

func runDesign(t *testing.T, r Run, read ReadFunc) {
	src, err := r.Sources(read)
	if err != nil {
		t.Fatal(err)
	}
	code := simrun.DefaultCode
	res, err := Execute(r, src, Options{Code: code})
	if err != nil {
		t.Fatalf("%s: %v", r.Name(), err)
	}
	needsSim := r.Expected.StopTime != "" && r.Expected.StopTime != "none"
	if res.Pending != nil && needsSim {
		pending(t, r, res)
		checkPreSim(t, r, res)
	} else {
		for _, p := range Check(r.Expected, res, MatchFor(r)) {
			t.Errorf("%s seed 0: %s", r.Name(), p)
		}
	}
	if res.Simulated {
		checkSeeds(t, r, src, res)
		if r.Variant == "" {
			checkGolden(t, r, read, res)
		}
	}
	if r.Variant == "" {
		checkGenerated(t, r, src, res)
	}
}

// checkPreSim checks what a run that could not simulate did produce:
// no error the expected outcome does not list.
func checkPreSim(t *testing.T, r Run, res *Result) {
	t.Helper()
	exp := r.Expected
	exp.StopTime = "none"
	for _, p := range Check(exp, res, Match{Subsequence: true,
		IgnoreLines: true}) {
		if strings.HasPrefix(p, "unexpected") {
			t.Errorf("%s: %s", r.Name(), p)
		}
	}
	if res.Exit == 2 && r.Expected.Exit != 2 {
		t.Errorf("%s: exit 2 before simulation: %v", r.Name(),
			res.Messages)
	}
}

// checkSeeds: a design claiming seed_independent gives byte-identical
// traces at every seed; on a difference the first differing line is
// shown.
func checkSeeds(t *testing.T, r Run, src map[string][]byte, base *Result) {
	t.Helper()
	if !r.Design.Has("seed_independent") {
		return
	}
	for s := 1; s < *seeds; s++ {
		res, err := Execute(r, src, Options{Seed: uint64(s),
			Code: simrun.DefaultCode})
		if err != nil {
			t.Fatalf("%s seed %d: %v", r.Name(), s, err)
		}
		for _, p := range Check(r.Expected, res, MatchFor(r)) {
			t.Errorf("%s seed %d: %s", r.Name(), s, p)
		}
		if n, a, b, ok := trace.FirstDiff(base.Text, res.Text); ok {
			t.Errorf("%s: seed %d trace differs from seed 0 at line %d:"+
				"\n  seed 0: %s\n  seed %d: %s", r.Name(), s, n, a, s, b)
			for _, d := range Races(r, src, simrun.DefaultCode,
				[]uint64{0, uint64(s)}) {
				t.Errorf("%s: %s: %s", r.Name(), d.Code, d.Message)
			}
		}
	}
}

// checkGolden compares the seed-0 trace with golden/trace.txt and its
// hash with trace_sha256, printing the first differing line.
func checkGolden(t *testing.T, r Run, read ReadFunc, res *Result) {
	t.Helper()
	d := r.Design
	rel := filepath.ToSlash(filepath.Join(d.GoldenDir(), "trace.txt"))
	if d.TraceSHA256 == "pending" || d.TraceSHA256 == "" {
		t.Errorf("%s: trace_sha256 is %q: run go run ./tools/oracle "+
			"--update golden", d.Name, d.TraceSHA256)
		return
	}
	want, err := read(rel)
	if err != nil {
		t.Errorf("%s: %v", d.Name, Missing(rel, err))
		return
	}
	if err := AnswerKey(want); err != nil {
		t.Errorf("%s: %s: %v", d.Name, rel, err)
	}
	if h := FieldHash(map[string][]byte{"trace.txt": want}); h !=
		d.TraceSHA256 {
		t.Errorf("%s: %s hashes to %s, corpus.yaml has %s", d.Name, rel,
			h, d.TraceSHA256)
	}
	if n, a, b, ok := trace.FirstDiff(want, res.Text); ok {
		t.Errorf("%s: trace differs from %s at line %d:\n  golden: %s\n"+
			"  gohard: %s", d.Name, rel, n, a, b)
	}
}

// checkGenerated runs the #8 variants: each must give the expected
// outcome (lines ignored, since layout and reordering move them), and
// the same pre-simulation diagnostics as the design itself.
func checkGenerated(t *testing.T, r Run, src map[string][]byte,
	base *Result) {
	t.Helper()
	cfg, err := r.Config(src)
	if err != nil {
		t.Fatal(err)
	}
	po := parser.FromConfig(cfg, toolVersion)
	for _, tr := range Transforms {
		vs := tr.Apply(src, po)
		res, err := Execute(r, vs, Options{Code: simrun.DefaultCode})
		if err != nil {
			t.Errorf("%s/%s: %v", r.Name(), tr.Name, err)
			continue
		}
		name := r.Name() + "/" + tr.Name
		if base.Pending == nil {
			for _, p := range Check(r.Expected, res, Match{
				IgnoreLines: true}) {
				t.Errorf("%s: %s", name, p)
			}
		}
		if a, b := outline(base), outline(res); a != b {
			t.Errorf("%s: outcome changed:\n  design:  %s\n  variant: %s",
				name, a, b)
		}
	}
}

// toolVersion is TOOL_VERSION for the generated variants' parse (the
// value does not matter; it only has to be the same everywhere).
const toolVersion = "0.0.0-dev"

// outline is the location-free outcome of a run.
func outline(r *Result) string {
	var ms []string
	for _, m := range r.Messages {
		ms = append(ms, fmt.Sprintf("%s %s %q", m.Severity, m.Code,
			m.Message))
	}
	slices.Sort(ms)
	return fmt.Sprintf("exit=%d status=%s simulated=%v stop=%d "+
		"pending=%d %v", r.Exit, r.Status, r.Simulated, r.StopTime,
		len(r.Pending), ms)
}

// TestTransformsChangeText: each generated variant really changes
// every design (else it checks nothing), and parses without new
// syntax errors.
func TestTransformsChangeText(t *testing.T) {
	m, read := loadManifest(t)
	for _, tr := range Transforms {
		changed := 0
		for _, d := range m.Designs {
			r := d.Runs()[0]
			src, err := r.Sources(read)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := r.Config(src)
			if err != nil {
				t.Fatal(err)
			}
			po := parser.FromConfig(cfg, toolVersion)
			out := tr.Apply(src, po)
			if !slices.Equal(slices.Sorted(maps.Keys(out)),
				slices.Sorted(maps.Keys(src))) {
				t.Fatalf("%s/%s: files changed", d.Name, tr.Name)
			}
			for k := range src {
				if !isVHDL(k) {
					continue
				}
				if string(out[k]) != string(src[k]) {
					changed++
				}
				before := len(parser.Parse(src[k], po).Problems)
				after := len(parser.Parse(out[k], po).Problems)
				if after != before {
					t.Errorf("%s/%s %s: %d syntax problems, was %d",
						d.Name, tr.Name, k, after, before)
				}
			}
		}
		if changed == 0 {
			t.Errorf("%s changes no file", tr.Name)
		}
	}
}

// TestTransformsKeepTokens: layout and case keep every token
// (case-folded), rename renames declarations and uses alike, reorder
// swaps independent statements, and no variant adds a parse problem.
func TestTransformsKeepTokens(t *testing.T) {
	src := []byte("entity e is end;\narchitecture a of e is\n" +
		"  signal s, t : bit; -- c\nbegin\n  s <= '1';\n" +
		"  t <= s after 1 ns;\nend;\n")
	po := parser.Options{}
	in := map[string][]byte{"a.vhd": src}
	words := func(b []byte) string {
		return strings.ToLower(strings.Join(strings.Fields(string(b)),
			" "))
	}
	for _, tr := range Transforms {
		out := tr.Apply(in, po)["a.vhd"]
		switch tr.Name {
		case "layout", "case":
			if words(out) != words(src) {
				t.Errorf("%s: %q", tr.Name, out)
			}
		case "rename":
			if !strings.Contains(string(out), "s_rn <= '1'") ||
				!strings.Contains(string(out), "t_rn <= s_rn") {
				t.Errorf("rename: %q", out)
			}
		case "reorder":
			i := strings.Index(string(out), "t <= s")
			j := strings.Index(string(out), "s <= '1'")
			if i < 0 || j < 0 || i > j {
				t.Errorf("reorder: %q", out)
			}
		}
		if p := parser.Parse(out, po).Problems; len(p) > 0 {
			t.Errorf("%s: %v", tr.Name, p)
		}
	}
}

// TestTransformsPSLComments (#109): under psl.comments, rename renames
// the objects a `-- psl` line names, and reorder moves each `-- psl`
// statement with its marker.
func TestTransformsPSLComments(t *testing.T) {
	src := []byte("entity e is end;\narchitecture a of e is\n" +
		"  signal s, t : bit;\nbegin\n  s <= '1';\n" +
		"  -- psl default clock is rising_edge(s);\n" +
		"  -- psl x : assert always t;\nend;\n")
	po := parser.Options{Standard: token.Std2008, PSLComments: true}
	in := map[string][]byte{"a.vhd": src}
	want := map[string]string{
		"rename": "  s_rn <= '1';\n" +
			"  -- psl default clock is rising_edge(s_rn);\n" +
			"  -- psl x : assert always t_rn;\n",
		"reorder": "  -- psl x : assert always t;\n" +
			"  -- psl default clock is rising_edge(s);\n" +
			"  s <= '1';\n",
	}
	for _, tr := range Transforms {
		out := tr.Apply(in, po)["a.vhd"]
		if w, ok := want[tr.Name]; ok &&
			!strings.Contains(string(out), "begin\n"+w+"end;") {
			t.Errorf("%s: %q", tr.Name, out)
		}
		if p := parser.Parse(out, po).Problems; len(p) > 0 {
			t.Errorf("%s: %v", tr.Name, p)
		}
	}
}

// TestOutlineDetects: the generated-variant check sees a transform
// that changes the meaning (a declaration renamed without its uses).
func TestOutlineDetects(t *testing.T) {
	m, read := loadManifest(t)
	d, _ := m.Design("md_resolve")
	r := d.Runs()[0]
	src, err := r.Sources(read)
	if err != nil {
		t.Fatal(err)
	}
	base, err := Execute(r, src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	bad := maps.Clone(src)
	bad["md_resolve_tb.vhd"] = []byte(strings.Replace(
		string(src["md_resolve_tb.vhd"]), "signal a_en", "signal a_xx", 1))
	res, err := Execute(r, bad, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outline(base) == outline(res) {
		t.Fatalf("broken variant not detected: %s", outline(res))
	}
}
