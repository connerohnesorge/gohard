package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/token"
)

var semFalse = flag.Bool("semerr-false", false,
	"TestSemerrGaps also counts GoHard errors in files both tools accept")

var debugPath = flag.String("semerr-debug", "",
	"TestSemerrGaps logs the analysis of this repository path")

var oneSidedFlag = flag.Bool("semerr-onesided", false,
	"TestSemerrGaps also lists GoHard errors on lines only one tool "+
		"reports that no semerr_follows entry records")

var updateGaps = flag.Bool("update-gaps", false,
	"rewrite "+semerrDir+"/"+semGapsName+" from the current GoHard "+
		"results (TestSemerrGaps)")

// TestSemerrGaps checks the semerr section alone and logs the
// agreement numbers per golden file. With -update-gaps it rewrites the
// known-gap list from the current GoHard results; without it, it only
// runs when -run names it (TestRecorded does the same check).
func TestSemerrGaps(t *testing.T) {
	if !*updateGaps && !strings.Contains(flag.Lookup("test.run").Value.
		String(), "TestSemerrGaps") {
		t.Skip("TestRecorded runs the check; -run TestSemerrGaps for " +
			"the numbers")
	}
	root := repoRoot(t)
	semCountFalse = *semFalse
	semDebugPath = *debugPath
	semOneSided = *oneSidedFlag
	defer func() {
		semCountFalse, semDebugPath, semOneSided = false, "", false
	}()
	probs, err := doCheck(root, []*section{semerrSection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	semResults.Lock()
	defer semResults.Unlock()
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range semResults.stats {
			if !yield(k) {
				return
			}
		}
	})
	var total semStats
	for _, n := range names {
		t.Logf("%-26s %s", n, semResults.stats[n])
		total.add(semResults.stats[n])
	}
	t.Logf("%-26s %s", "total", total)
	for _, l := range semResults.debug {
		t.Log(l)
	}
	if *semFalse {
		slices.Sort(semResults.false)
		for _, f := range semResults.false {
			t.Logf("gohard error in a clean file: %s", f)
		}
	}
	if *oneSidedFlag {
		slices.Sort(semResults.oneSided)
		for _, f := range slices.Compact(semResults.oneSided) {
			t.Logf("unrecorded one-sided GoHard error: %s", f)
		}
	}
	if *updateGaps {
		var b strings.Builder
		b.WriteString(gapsHeader)
		for _, n := range names {
			for _, g := range semResults.gaps[n] {
				b.WriteString(g + "\n")
			}
		}
		if err := writeFile(root, semerrDir+"/"+semGapsName,
			[]byte(b.String())); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, p := range probs {
		t.Error(p)
	}
}

const gapsHeader = `# Known gaps of the semantic-error oracle (#34, tools/oracle semerr):
# lines where GHDL and NVC both report an error and GoHard does not,
# as "<golden file> <run> <path>:<line>". GoHard writes this list, not
# the tools: go test ./tools/oracle -run TestSemerrGaps -update-gaps.
# The check fails on a gap missing here and on a listed gap that is
# closed, so the list only shrinks as sema improves.
`

// semFake stands in for GHDL and NVC in the semerr tests: GHDL reports
// an error on every line holding "--E" or "--G", NVC on every line
// holding "--E" or "--N". On a line holding "--C" GHDL reports an
// error and NVC a cascade (a unit it depends on has errors).
func semFake() *toolset {
	fake := func(nvc bool) func(string, []string) (string, int) {
		return func(dir string, args []string) (string, int) {
			file := args[len(args)-1]
			b, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				return err.Error(), 2
			}
			mine := "--G"
			if nvc {
				mine = "--N"
			}
			var out strings.Builder
			for i, l := range strings.Split(string(b), "\n") {
				if !strings.Contains(l, "--E") &&
					!strings.Contains(l, mine) &&
					!strings.Contains(l, "--C") {
					continue
				}
				switch {
				case nvc && strings.Contains(l, "--C"):
					fmt.Fprintf(&out, "** Error: design unit depends on "+
						"WORK.P which was analysed with errors\n"+
						"    > %s:%d\n", file, i+1)
				case nvc:
					fmt.Fprintf(&out, "** Error: marked\n    > %s:%d\n",
						file, i+1)
				default:
					fmt.Fprintf(&out, "%s:%d:1:error: marked\n", file, i+1)
				}
			}
			if out.Len() > 0 {
				return out.String(), 1
			}
			return "", 0
		}
	}
	return &toolset{found: map[string]*tool{
		"ghdl": {name: "ghdl", version: "6.0.0", fake: fake(false)},
		"nvc":  {name: "nvc", version: "1.23.0", fake: fake(true)},
	}}
}

const semManifestText = `schema: 1
designs:
  - name: demo
    standard: "2008"
    files:
      - path: p.vhd
        standard: "2008"
      - path: a.vhd
        standard: "2008"
      - path: b.vhd
        standard: "2008"
    trace_sha256: pending
    oracle_sha256: pending
regression: []
`

// semRepo is a repository with one design: p.vhd declares a package,
// a.vhd uses it, b.vhd is independent.
func semRepo(t *testing.T, p, a, b string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "testdata", "corpus", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, filepath.FromSlash(manifestPath)),
		semManifestText)
	write(t, filepath.Join(dir, "p.vhd"), p)
	write(t, filepath.Join(dir, "a.vhd"), a)
	write(t, filepath.Join(dir, "b.vhd"), b)
	return root
}

func semUpdate(t *testing.T, root string) {
	t.Helper()
	var out bytes.Buffer
	if err := doUpdate(root, sections, nil, semFake(), &out); err != nil {
		t.Fatal(err)
	}
}

func semCheck(t *testing.T, root string) []string {
	t.Helper()
	probs, err := doCheck(root, []*section{semerrSection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return probs
}

const (
	pkgClean = "package p is\n  constant c : integer := 1;\nend;\n"
	pkgBad   = "package p is\n  constant c : integer := 1; --E\nend;\n"
	useP     = "use work.p.all;\nentity a is end;\n" +
		"architecture rtl of a is\n" +
		"  signal s : integer := c;\n" +
		"  signal t : integer := nope; --E\n" +
		"begin\nend;\n"
	bClean = "entity b is end;\narchitecture rtl of b is\n" +
		"  signal s : integer := 0;\nbegin\nend;\n"
)

// TestSemerrFlow records with fake tools and checks: an agreed error
// GoHard reports passes; an agreed error on a valid line is a gap that
// must be listed; a listed gap that is not one fails; a source change
// makes the recording stale; the hash goes after format_oracle_sha256.
func TestSemerrFlow(t *testing.T) {
	root := semRepo(t, pkgClean, useP, bClean)
	if probs := semCheck(t, root); len(probs) != 1 ||
		!strings.Contains(probs[0], "is not recorded") {
		t.Fatalf("before update: %q", probs)
	}
	semUpdate(t, root)
	if probs := semCheck(t, root); len(probs) > 0 {
		t.Fatalf("after update: %q", probs)
	}
	man := readFile(t, root, manifestPath)
	if !strings.Contains(man, "    format_oracle_sha256: ") ||
		!strings.Contains(man, "\n    "+fieldSemerr+": ") ||
		strings.Index(man, fieldSemerr) < strings.Index(man,
			"format_oracle_sha256") {
		t.Fatalf("corpus.yaml:\n%s", man)
	}
	g := readFile(t, root, semerrDir+"/corpus-demo.txt")
	if !strings.Contains(g, "\n a.vhd ghdl=1@5 nvc=1@5\n") ||
		!strings.Contains(g, "\n p.vhd ghdl=0 nvc=0\n") {
		t.Fatalf("golden:\n%s", g)
	}

	// A valid line both tools reject: a gap.
	write(t, filepath.Join(root, "testdata/corpus/demo/b.vhd"),
		strings.Replace(bClean, "0;", "0; --E", 1))
	if probs := semCheck(t, root); len(probs) != 1 ||
		!strings.Contains(probs[0], "plan or sources changed") {
		t.Fatalf("stale: %q", probs)
	}
	semUpdate(t, root)
	probs := semCheck(t, root)
	gap := "corpus-demo.txt corpus:demo:main testdata/corpus/demo/b.vhd:3"
	if len(probs) != 1 || !strings.Contains(probs[0], gap+", GoHard "+
		"does not") {
		t.Fatalf("gap: %q", probs)
	}
	write(t, filepath.Join(root, semerrDir, semGapsName),
		gapsHeader+gap+"\n")
	if probs := semCheck(t, root); len(probs) > 0 {
		t.Fatalf("listed gap: %q", probs)
	}
	// The gap closes (the tools no longer agree): the entry must go.
	write(t, filepath.Join(root, "testdata/corpus/demo/b.vhd"), bClean)
	semUpdate(t, root)
	if probs := semCheck(t, root); len(probs) != 1 ||
		!strings.Contains(probs[0], "the known gap "+gap+" is closed") {
		t.Fatalf("closed gap: %q", probs)
	}
}

// TestSemerrCascade: a file whose dependency a tool rejected is not
// checked (its agreed errors may be the cascade), while an independent
// file of the same run still is.
func TestSemerrCascade(t *testing.T) {
	bGap := strings.Replace(bClean, "0;", "0; --E", 1)
	root := semRepo(t, pkgBad, strings.Replace(useP, "nope", "0", 1),
		bGap)
	semUpdate(t, root)
	probs := semCheck(t, root)
	// p.vhd:2 is a gap (valid line), a.vhd:5 is not checked (p
	// failed), b.vhd:3 is a gap.
	if len(probs) != 2 || !strings.Contains(probs[0], "p.vhd:2") &&
		!strings.Contains(probs[1], "p.vhd:2") ||
		strings.Contains(strings.Join(probs, "\n"), "a.vhd") {
		t.Fatalf("cascade: %q", probs)
	}
}

// TestSemerrNVCCascade: a line where NVC reports only a cascade (a
// unit of the same file failed) is not checked, as GHDL's cascades.
func TestSemerrNVCCascade(t *testing.T) {
	b := strings.Replace(bClean, "0;", "0; --C", 1)
	root := semRepo(t, pkgClean, useP, b)
	semUpdate(t, root)
	if probs := semCheck(t, root); len(probs) > 0 {
		t.Fatalf("an NVC cascade is checked: %q", probs)
	}
	g := readFile(t, root, semerrDir+"/corpus-demo.txt")
	if !strings.Contains(g, "~") {
		t.Fatalf("no cascade mark recorded:\n%s", g)
	}
	for _, m := range []string{
		"design unit depends on WORK.P which was analysed with errors",
		"design unit WORK.LOG was analysed with errors",
		"entity \"e\" was not analysed",
	} {
		if !cascadeMsg.MatchString(m) {
			t.Errorf("%q is not a cascade", m)
		}
	}
	if cascadeMsg.MatchString(`unit "p" not found in library "work"`) {
		t.Error("a missing unit is a cascade")
	}
}

// TestSemerrFollows: a recorded disagreement makes GoHard follow the
// named side on that line.
func TestSemerrFollows(t *testing.T) {
	// GHDL alone rejects line 4 of b.vhd, which GoHard accepts.
	b := strings.Replace(bClean, "0;", "0; --G", 1)
	root := semRepo(t, pkgClean, useP, b)
	semUpdate(t, root)
	if probs := semCheck(t, root); len(probs) > 0 {
		t.Fatalf("unrecorded disagreement: %q", probs)
	}
	for side, ok := range map[string]bool{"ghdl": false, "nvc": true} {
		man := strings.Replace(readFile(t, root, manifestPath),
			"    trace_sha256:", "    semerr_follows:\n"+
				"      - path: b.vhd\n        line: 3\n"+
				"        follows: "+side+"\n        lrm: \"6.4.2.4\"\n"+
				"    trace_sha256:", 1)
		write(t, filepath.Join(root, filepath.FromSlash(manifestPath)),
			man)
		probs := semCheck(t, root)
		if ok != (len(probs) == 0) || !ok && !strings.Contains(probs[0],
			"GoHard follows ghdl here (LRM 6.4.2.4), which reports an "+
				"error, but GoHard reports no error") {
			t.Fatalf("follows %s: %q", side, probs)
		}
		man = strings.Replace(man, "    semerr_follows:\n"+
			"      - path: b.vhd\n        line: 3\n"+
			"        follows: "+side+"\n        lrm: \"6.4.2.4\"\n", "", 1)
		write(t, filepath.Join(root, filepath.FromSlash(manifestPath)),
			man)
	}
}

// TestGHDLScriptRuns: analyses group by directory and standard in
// script order; clean and the synth_tb helper end the groups; --work
// selects the library; generated files are skipped.
func TestGHDLScriptRuns(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ts", "gna", "t1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.vhdl", "b.vhdl", "c.vhdl", "d.vhdl",
		"e.vhdl", "tb_e.vhdl"} {
		write(t, filepath.Join(dir, f), "")
	}
	script := `#! /bin/sh
. ../../testenv.sh
analyze a.vhdl b.vhdl
analyze_failure --work=Foo -frelaxed c.vhdl
export GHDL_STD_FLAGS=--std=08
analyze d.vhdl
synth_tb e
analyze a.vhdl syn_x.vhdl
clean
export GHDL_STD_FLAGS=--std=93c
analyze b.vhdl
analyze --std=87 c.vhdl
exit 0
analyze d.vhdl
`
	runs, skipped := ghdlScriptRuns(root, "ts", "gna/t1/testsuite.sh",
		[]byte(script), "g.txt")
	var got []string
	for _, r := range runs {
		s := r.ID + " " + r.GHDLStd + ":"
		for _, f := range r.Files {
			s += " " + fileSpec(f)
		}
		got = append(got, s)
	}
	want := []string{
		"gna/t1#1 93c: a.vhdl b.vhdl c.vhdl lib=foo -frelaxed",
		"gna/t1#2 08: d.vhdl e.vhdl tb_e.vhdl",
		"gna/t1#3 08: a.vhdl",
		"gna/t1#4 93c: b.vhdl",
	}
	if !slices.Equal(got, want) || skipped != 2 {
		t.Fatalf("runs (skipped %d):\n%s", skipped,
			strings.Join(got, "\n"))
	}
	if runs[0].Std != token.Std1993 || runs[0].Dir != "ts/gna/t1" ||
		runs[0].Files[2].Path != "ts/gna/t1/c.vhdl" {
		t.Fatalf("run 1: %+v", runs[0])
	}
}

// TestClosure: the dependency closure follows unit names
// transitively, keeps re-analysis order and ignores later files.
func TestClosure(t *testing.T) {
	texts := [][]byte{
		[]byte("package p1 is end;"),                                 // 0
		[]byte("use work.p1.all; package p2 is end;"),                // 1
		[]byte("package body p2 is end; -- p9"),                      // 2
		[]byte("entity e is end; architecture a of e is begin end;"), // 3
		[]byte("package P1 is end; -- replaces 0"),                   // 4
		[]byte("use work.p2.all; entity top is end;\n" +
			"architecture x of top is begin u: entity work.e; end;"), // 5
		[]byte("package p9 is end;"),                // 6
		[]byte("architecture b of e is begin end;"), // 7
	}
	x := newUnitIndex(texts, nil)
	cases := map[int][]int{
		0: {},
		1: {0},
		2: {0, 1},
		5: {1, 2, 3, 4},
		7: {3},
	}
	for i, want := range cases {
		if got := x.closure(i); !slices.Equal(got, want) {
			t.Errorf("closure(%d) = %v, want %v", i, got, want)
		}
	}
}

// TestClosureLibraries: re-analysis replaces a unit only within its
// library, and a file's own architecture does not hide another
// entity's architecture of the same name.
func TestClosureLibraries(t *testing.T) {
	texts := [][]byte{
		[]byte(
			"package g is end;",
		), // 0, library stim
		[]byte(
			"package g is end;",
		), // 1, work: does not replace 0
		[]byte("library stim; use stim.g.all; entity t is end;"), // 2
		[]byte("entity f is end;"),                               // 3
		[]byte("architecture rtl of f is begin end;"),            // 4
		[]byte("entity top is end; architecture rtl of top is\n" +
			"begin u: entity work.f(rtl); end;"), // 5
		[]byte("package g is end;"),                 // 6, stim: replaces 0
		[]byte("use stim.g.all; entity t2 is end;"), // 7
	}
	libs := []string{"stim", work, work, work, work, work, "stim", work}
	x := newUnitIndex(texts, libs)
	cases := map[int][]int{2: {0, 1}, 5: {3, 4}, 7: {1, 6}}
	for i, want := range cases {
		if got := x.closure(i); !slices.Equal(got, want) {
			t.Errorf("closure(%d) = %v, want %v", i, got, want)
		}
	}
}

// TestClosureConfiguration: a configuration is a primary unit its
// users depend on, though its name is followed by "of entity".
func TestClosureConfiguration(t *testing.T) {
	texts := [][]byte{
		[]byte("entity e is end; architecture a of e is begin end;"), // 0
		[]byte("configuration Conf_E of e is for a end for; end;"),   // 1
		[]byte("configuration top of t is for x\n" +
			"for u : c use configuration work.conf_e; end for;\n" +
			"end for; end;"), // 2
	}
	x := newUnitIndex(texts, nil)
	if got := x.closure(2); !slices.Equal(got, []int{0, 1}) {
		t.Errorf("closure(2) = %v, want [0 1]", got)
	}
}

// TestToolResult: the compact result form round-trips.
func TestToolResult(t *testing.T) {
	for _, s := range []string{"0", "1", "-1", "1@3", "1@3,10,200"} {
		r, err := parseToolResult(s)
		if err != nil || r.String() != s {
			t.Errorf("%q: %v %q", s, err, r.String())
		}
	}
	if _, err := parseToolResult("1@x"); err == nil {
		t.Error("1@x parsed")
	}
}

// TestSemPlanDeterministic: the plan of the real repository is the
// same on every call and every PR-set file is analyzed by some run.
func TestSemPlanDeterministic(t *testing.T) {
	root := repoRoot(t)
	plan := func() []string {
		x, err := loadCtx(root)
		if err != nil {
			t.Fatal(err)
		}
		ts, err := semTargetsOf(x)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, st := range ts {
			for _, r := range st.plan.Runs {
				out = append(out, runKey(r)+r.ID+r.Golden)
			}
		}
		return out
	}
	a, b := plan(), plan()
	if !slices.Equal(a, b) {
		t.Fatal("plan differs between calls")
	}
	x, _ := loadCtx(root)
	ts, _ := semTargetsOf(x)
	for _, st := range ts {
		if st.ref.List != "regression" {
			continue
		}
		seen := map[string]bool{}
		dir := ""
		for _, r := range st.plan.Runs {
			for _, f := range r.Files {
				seen[f.Path] = true
			}
			if strings.HasPrefix(r.ID, "alone:") {
				dir = strings.TrimSuffix(r.Files[0].Path,
					strings.TrimPrefix(r.ID, "alone:"))
			}
		}
		if dir == "" {
			continue
		}
		files, err := vhdlFilesUnder(root, strings.TrimSuffix(dir, "/"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if !seen[f] {
				t.Errorf("%s: %s is in no run", st.ref.Name, f)
			}
		}
	}
}

// TestSemerrDecisions: a GoHard error in a file both tools accept
// fails the check unless a semerr_decisions entry records it (gohard:
// error, by line or, with line 0, for the whole file); an agreed line
// GoHard does not report is no gap when an entry records it (gohard:
// none); an entry GoHard no longer matches fails.
func TestSemerrDecisions(t *testing.T) {
	setMan := func(root, entries string) {
		man := readFile(t, root, manifestPath)
		if i := strings.Index(man, "    semerr_decisions:\n"); i >= 0 {
			j := strings.Index(man, "    trace_sha256:")
			man = man[:i] + man[j:]
		}
		if entries != "" {
			man = strings.Replace(man, "    trace_sha256:",
				"    semerr_decisions:\n"+entries+"    trace_sha256:", 1)
		}
		write(t, filepath.Join(root, filepath.FromSlash(manifestPath)),
			man)
	}
	entry := func(line int, gohard string) string {
		return fmt.Sprintf("      - path: b.vhd\n        line: %d\n"+
			"        gohard: %s\n        lrm: \"14.4.1\"\n", line, gohard)
	}
	// GoHard alone rejects line 3 of b.vhd.
	b := strings.Replace(bClean, ":= 0;", ":= nope;", 1)
	root := semRepo(t, pkgClean, useP, b)
	semUpdate(t, root)
	probs := semCheck(t, root)
	if len(probs) != 1 || !strings.Contains(probs[0],
		"b.vhd:3: GoHard reports sema/undefined") {
		t.Fatalf("unrecorded clean-file error: %q", probs)
	}
	for _, e := range []string{entry(3, "error"), entry(0, "error")} {
		setMan(root, e)
		if probs := semCheck(t, root); len(probs) > 0 {
			t.Fatalf("recorded clean-file error %q: %q", e, probs)
		}
	}
	setMan(root, entry(3, "error")+entry(4, "error"))
	if probs := semCheck(t, root); len(probs) != 1 ||
		!strings.Contains(probs[0], "b.vhd:4: the recorded decision") {
		t.Fatalf("stale clean-file decision: %q", probs)
	}
	// Both tools reject line 3 of a clean b.vhd, which GoHard accepts.
	root = semRepo(t, pkgClean, useP,
		strings.Replace(bClean, "0;", "0; --E", 1))
	semUpdate(t, root)
	probs = semCheck(t, root)
	if len(probs) != 1 || !strings.Contains(probs[0], "b.vhd:3, GoHard "+
		"does not") {
		t.Fatalf("unrecorded gap: %q", probs)
	}
	setMan(root, entry(3, "none"))
	if probs := semCheck(t, root); len(probs) > 0 {
		t.Fatalf("recorded gap: %q", probs)
	}
	setMan(root, entry(3, "error"))
	if probs := semCheck(t, root); len(probs) != 1 ||
		!strings.Contains(probs[0], "says GoHard reports \"error\"") {
		t.Fatalf("decision that does not match: %q", probs)
	}
}
