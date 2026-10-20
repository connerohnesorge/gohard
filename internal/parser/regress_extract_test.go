package parser

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/condanal"
	"github.com/connerohnesorge/gohard/internal/token"
)

// refSummary renders refs as "file fail|must std [psl]" lines.
func refSummary(refs []ghdlRef) []string {
	var out []string
	for _, r := range refs {
		k := "must"
		if r.fail {
			k = "fail"
		}
		s := r.file + " " + k + " " + r.run.std.String()
		if r.run.psl {
			s += " psl"
		}
		if r.run.v87 {
			s += " v87"
		}
		out = append(out, s)
	}
	return out
}

// TestShellModel checks the testsuite.sh model on synthetic scripts:
// quoting, variables, for loops over lists, variables and globs,
// redirections, here-documents, conditions, cd and exit.
func TestShellModel(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gna", "t1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"g1.vhdl", "g2.vhdl", "x.vhd"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil,
			0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, src string
		want      []string
	}{
		{"plain", "analyze a.vhdl\nanalyze_failure b.vhdl 2> b.err\n",
			[]string{"gna/t1/a.vhdl must 1993",
				"gna/t1/b.vhdl fail 1993"}},
		{"std", "export GHDL_STD_FLAGS=--std=08\nanalyze a.vhdl\n" +
			"GHDL_STD_FLAGS=\"-fpsl --std=02\"\nanalyze b.vhdl\n" +
			"analyze --std=19 c.vhdl\nGHDL_STD_FLAGS=--std=93 clean\n" +
			"analyze d.vhdl\nexport GHDL_STD_FLAGS=-fsynopsys\n" +
			"analyze e.vhdl --std=87\nGHDL_STD_FLAGS=\nGHDL_FLAGS=--std=08\n" +
			"analyze f.vhdl\n",
			[]string{"gna/t1/a.vhdl must 2008",
				"gna/t1/b.vhdl must 2002 psl",
				"gna/t1/c.vhdl must 2019 psl",
				"gna/t1/d.vhdl must 2002 psl",
				"gna/t1/e.vhdl must 1993 v87",
				"gna/t1/f.vhdl must 2008"}},
		{"loops", "files=\"\n p.vhdl\n q.vhdl\n\"\nfor f in $files; do\n" +
			"  analyze_failure $f\ndone\nfor i in *.vhdl; do " +
			"analyze_failure $i; done\nfor u in r s; do\n" +
			"analyze ${u}.vhd\ndone\n",
			[]string{"gna/t1/p.vhdl fail 1993", "gna/t1/q.vhdl fail 1993",
				"gna/t1/g1.vhdl fail 1993", "gna/t1/g2.vhdl fail 1993",
				"gna/t1/r.vhd must 1993", "gna/t1/s.vhd must 1993"}},
		{"conditions", "if analyze_failure a.vhdl; then\n  exit 1\nfi\n" +
			"if analyze -P b.vhdl; then echo 'error expected'; exit 1; " +
			"fi\n! analyze c.vhdl\n# analyze d.vhdl\n" +
			"while read l; do\n  analyze w.vhdl\ndone < list\n" +
			"if true; then\n  analyze e.vhdl\nfi\n",
			[]string{"gna/t1/w.vhdl must 1993", "gna/t1/e.vhdl must 1993"}},
		{"heredoc", "cat > x.txt <<'EOF'\nanalyze no.vhdl \"\nEOF\n" +
			"analyze yes.vhdl\n",
			[]string{"gna/t1/yes.vhdl must 1993"}},
		{"cd", "mkdir d1\ncd d1\nanalyze ../a.vhdl\ncd ..\n" +
			"analyze sub/b.vhdl\n",
			[]string{"gna/t1/a.vhdl must 1993",
				"gna/t1/sub/b.vhdl must 1993"}},
		{"exit", "exit 0\nanalyze a.vhdl\n", nil},
		{"helpers", "synth_tb top -fpsl\nsynth_analyze m\n" +
			"synth x.vhdl -e > syn_x.vhdl\nsynth_failure bad.vhdl\n" +
			"elab_simulate tb --std=08 t.vhdl\n",
			[]string{"gna/t1/top.vhdl must 1993 psl",
				"gna/t1/tb_top.vhdl must 1993 psl",
				"gna/t1/m.vhdl must 1993", "gna/t1/x.vhdl must 1993",
				"gna/t1/t.vhdl must 2008"}},
		{"continuation", "analyze a.vhdl \\\n  b.vhdl\n",
			[]string{"gna/t1/a.vhdl must 1993", "gna/t1/b.vhdl must 1993"}},
	}
	for _, c := range cases {
		got := refSummary(scriptRefs(root, "gna/t1/testsuite.sh",
			[]byte(c.src)))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func ghdlRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(repoRoot, "testdata", "ghdl", "testsuite")
	if n, err := countVHDL(root); n == 0 {
		t.Fatal(missingSubmodule("testdata/ghdl/testsuite",
			"testdata/ghdl", err))
	}
	return root
}

// TestGHDLExtraction checks the extraction on the scripts corpus.yaml
// names as special cases: the seven loop scripts, a cd script, a
// disabled script and the VESTS lists.
func TestGHDLExtraction(t *testing.T) {
	root := ghdlRoot(t)
	read := func(s string) []ghdlRef {
		refs, err := readGHDLScript(root, s)
		if err != nil {
			t.Fatal(err)
		}
		return refs
	}
	failing := func(refs []ghdlRef) []string {
		var out []string
		for _, r := range refs {
			if r.fail {
				out = append(out, r.file)
			}
		}
		return out
	}
	vhdl, _ := filepath.Glob(filepath.Join(root, "gna", "bug090", "*.vhdl"))
	if got := failing(read("gna/bug090/testsuite.sh")); len(got) !=
		len(vhdl) || len(vhdl) == 0 {
		t.Errorf("bug090: %d failures, %d .vhdl files", len(got),
			len(vhdl))
	}
	for _, c := range []struct {
		script string
		want   []string
	}{
		{"gna/bug0144/testsuite.sh", []string{"badloc", "underscore1",
			"quote1"}},
		{"gna/issue2686/testsuite.sh", []string{"tst_rec", "tst_arr"}},
		{"gna/issue2110/testsuite.sh", []string{"conf1", "psl1", "psl2",
			"retid"}},
	} {
		var want []string
		dir := filepath.ToSlash(filepath.Dir(c.script))
		for _, w := range c.want {
			want = append(want, dir+"/"+w+".vhdl")
		}
		if got := failing(read(c.script)); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", c.script, got, want)
		}
	}
	for _, s := range []string{"gna/issue2070/testsuite.sh",
		"gna/issue2076/testsuite.sh", "gna/issue2116/testsuite.sh"} {
		refs := read(s)
		if len(failing(refs)) < 2 {
			t.Errorf("%s: files= list not expanded: %v", s,
				refSummary(refs))
		}
	}
	for _, r := range read("gna/issue2070/testsuite.sh") {
		if r.run.std != token.Std2008 {
			t.Errorf("issue2070 %s under %v, want 2008", r.file, r.run.std)
		}
	}
	if got := refSummary(read("gna/ticket87/testsuite.sh")); !slices.Equal(
		got, []string{"gna/ticket87/hello.vhdl must 1993"}) {
		t.Errorf("ticket87 (cd dir1; analyze ../hello.vhdl): %v", got)
	}
	if got := read("gna/issue1623/testsuite.sh"); len(got) != 0 {
		t.Errorf("issue1623 starts with exit 0: %v", refSummary(got))
	}

	// VESTS: the two listed non_compliant.exp give exactly their
	// run_non_compliant_test lines as failures; the driver agrees and
	// requires the simulator_failure files to analyse.
	countLines := func(exp, fn string) int {
		src, err := os.ReadFile(filepath.Join(root,
			filepath.FromSlash(exp)))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for l := range strings.SplitSeq(string(src), "\n") {
			if w := strings.Fields(l); len(w) > 1 && w[0] == fn {
				n++
			}
		}
		return n
	}
	const (
		ash = "vests/vhdl-93/ashenden/non_compliant/non_compliant.exp"
		bil = "vests/vhdl-93/billowitch/non_compliant/analyzer_failure/" +
			"non_compliant.exp"
		sim = "vests/vhdl-93/billowitch/non_compliant/simulator_failure/" +
			"non_compliant.exp"
	)
	for _, exp := range []string{ash, bil} {
		refs, err := readExp(root, exp, token.Std1993, exactExpMode)
		if err != nil {
			t.Fatal(err)
		}
		if n := countLines(exp, "run_non_compliant_test"); len(refs) != n ||
			len(failing(refs)) != n || n == 0 {
			t.Errorf("%s: %d refs, %d lines", exp, len(refs), n)
		}
	}
	driver, err := readVESTS(root)
	if err != nil {
		t.Fatal(err)
	}
	bySrc := map[string][2]int{}
	for _, r := range driver {
		exp, _, _ := strings.Cut(r.why, ":")
		c := bySrc[exp]
		if r.fail {
			c[0]++
		} else {
			c[1]++
		}
		bySrc[exp] = c
	}
	nSim := countLines(sim, "run_non_compliant_test")
	if c := bySrc[sim]; c[0] != 0 || c[1] != nSim || nSim == 0 {
		t.Errorf("simulator_failure: %v, want 0 failures, %d must", c,
			nSim)
	}
	if c := bySrc[bil]; c[0] != countLines(bil,
		"run_non_compliant_test") || c[1] != 0 {
		t.Errorf("analyzer_failure via the driver: %v", c)
	}
	if c := bySrc["vests/vhdl-93/billowitch/compliant/compliant.exp"]; c[1] !=
		countLines("vests/vhdl-93/billowitch/compliant/compliant.exp",
			"run_compliant_test") || c[1] == 0 {
		t.Errorf("billowitch compliant: %v", c)
	}
	for _, r := range driver {
		if strings.Contains(r.file, "vhdl-ams") ||
			strings.Contains(r.file, "disputed") {
			t.Errorf("%s: not run by vests/testsuite.sh", r.file)
		}
	}
}

// TestNVCExtraction checks the NVC readers on known entries.
func TestNVCExtraction(t *testing.T) {
	root := filepath.Join(repoRoot, "testdata", "nvc", "test")
	if n, err := countVHDL(root); n == 0 {
		t.Fatal(missingSubmodule("testdata/nvc/test", "testdata/nvc", err))
	}
	list, err := readTestlist(root)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]nvcRef{}
	for _, r := range list {
		if r.fail {
			t.Errorf("%s: testlist entries never expect an analysis "+
				"failure", r.file)
		}
		byName[r.file] = r
	}
	if r := byName["regress/cond4.vhd"]; r.std != token.Std2019 ||
		!slices.Equal(r.defines, []condanal.Define{{
			Name: "USER_DIRECTIVE", Value: "VALID_VALUE"}}) {
		t.Errorf("cond4: %+v", r)
	}
	if r := byName["regress/wait1.vhd"]; r.std != token.Std1993 {
		t.Errorf("wait1 default standard: %+v", r)
	}
	if r := byName["regress/psl1.vhd"]; !r.psl {
		t.Errorf("psl1 --psl: %+v", r)
	}
	if _, ok := byName["regress/vlog2.vhd"]; ok {
		t.Error("verilog test listed")
	}
	parse, err := readUnitTests(root, "test_parse.c")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range parse {
		if r.file == "parse/entity.vhd" {
			found = r.fail
		}
	}
	if !found {
		t.Error("parse/entity.vhd has an expect[]: may fail")
	}
	elab, err := readUnitTests(root, "test_elab.c")
	if err != nil {
		t.Fatal(err)
	}
	var stds []token.Standard
	for _, r := range elab {
		if r.file == "elab/block2.vhd" {
			stds = append(stds, r.std)
		}
	}
	// tcase_add_loop_test(tc, test_block2, STD_02, STD_19 + 1):
	// STD_02, STD_08, STD_19.
	if !slices.Equal(stds, []token.Standard{token.Std2002,
		token.Std2008, token.Std2019}) {
		t.Errorf("block2 standards %v", stds)
	}
	for _, r := range elab {
		if strings.HasSuffix(r.file, ".v") {
			t.Errorf("verilog input %s", r.file)
		}
	}
}

// TestMissingSubmodule: an absent checkout fails with the git submodule
// update --init hint instead of skipping (testdata/corpus/README.md).
func TestMissingSubmodule(t *testing.T) {
	for _, e := range []regEntry{
		{Name: "gone", Path: "testdata/no-such-submodule", Tag: "pr",
			Subfolder: "."},
		{Name: "gone", Path: "testdata/no-such-submodule", Tag: "pr",
			Subfolder: "test", ExpectedFailures: []string{"x.txt"}},
	} {
		_, err := entrySource(e)
		if err == nil || !strings.Contains(err.Error(),
			"git submodule update --init testdata/no-such-submodule") {
			t.Errorf("%+v: %v", e, err)
		}
	}
	// A stale expected_failures pattern fails too.
	_, err := entrySource(regEntry{Name: "nvc", Path: "testdata/nvc",
		Tag: "pr", Subfolder: "test",
		ExpectedFailures: []string{"test/no-such-list.txt"}})
	if err == nil || !strings.Contains(err.Error(), "matches no file") {
		t.Errorf("stale pattern: %v", err)
	}
}

// TestGHDLRelaxedDefault: a run with GHDL's default standard (93c) or
// an explicit --std=93c is relaxed (syntax/standard does not fail it);
// any other --std is strict.
func TestGHDLRelaxedDefault(t *testing.T) {
	src := "analyze a.vhdl\nanalyze --std=93c b.vhdl\n" +
		"analyze --std=93 c.vhdl\nanalyze --std=08 d.vhdl\n"
	want := map[string]bool{"gna/t1/a.vhdl": true, "gna/t1/b.vhdl": true,
		"gna/t1/c.vhdl": false, "gna/t1/d.vhdl": false}
	refs := scriptRefs(t.TempDir(), "gna/t1/testsuite.sh", []byte(src))
	if len(refs) != len(want) {
		t.Fatalf("got %d refs, want %d", len(refs), len(want))
	}
	for _, r := range refs {
		if w, ok := want[r.file]; !ok || r.run.relaxed != w {
			t.Errorf("%s: relaxed=%v, want %v", r.file, r.run.relaxed, w)
		}
	}
}

// TestDirectExpectedFailure: a .vhd/.vhdl path in expected_failures
// (a preprocessor template, a documentation snippet) marks that file
// "may fail", and every other file of a source with no upstream list
// must still parse.
func TestDirectExpectedFailure(t *testing.T) {
	s, err := entrySource(regEntry{Name: "nvc", Path: "testdata/nvc",
		Tag: "pr", Subfolder: "test",
		ExpectedFailures: []string{"test/regress/access1.vhd"}})
	if err != nil {
		t.Fatal(err)
	}
	if e := s.exp["testdata/nvc/test/regress/access1.vhd"]; e == nil ||
		!e.mayFail {
		t.Errorf("listed file: %+v", e)
	}
	if s.unlisted == nil || !s.unlisted.anyStd {
		t.Errorf("unlisted: %+v", s.unlisted)
	}
}

// TestMissingSubmoduleCheckout: on a checkout where the submodules
// were never initialised (git leaves each one an empty directory, or
// no directory at all), every pr and nightly regression entry of the
// real corpus.yaml fails with the git submodule update --init hint
// naming that submodule; none is skipped (#30, #8).
func TestMissingSubmoduleCheckout(t *testing.T) {
	m := readManifest(t)
	for i, e := range m.Regression {
		root := t.TempDir()
		if i%2 == 0 {
			// An uninitialised submodule: an empty directory.
			dir := filepath.Join(root, filepath.FromSlash(e.Path))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		_, err := entrySourceAt(root, e)
		want := "git submodule update --init " + e.Path
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s (%s): err = %v, want %q", e.Name, e.Path,
				err, want)
		}
	}
}

// TestInvalidUpstream: every file on invalidUpstream exists and still
// reports its listed syntax error, so the may-fail exemption is exact.
func TestInvalidUpstream(t *testing.T) {
	for _, d := range invalidUpstream {
		src, err := os.ReadFile(filepath.Join(repoRoot,
			filepath.FromSlash(d.file)))
		if err != nil {
			t.Errorf("%s: %v (run git submodule update --init)",
				d.file, err)
			continue
		}
		r := Parse(src, Options{Standard: d.std})
		found := false
		for _, p := range r.Problems {
			for _, a := range p.Args {
				found = found || strings.Contains(a, d.want)
			}
		}
		if !found {
			t.Errorf("%s: no longer reports %q (%s); remove it from "+
				"invalidUpstream", d.file, d.want, d.why)
		}
	}
}
