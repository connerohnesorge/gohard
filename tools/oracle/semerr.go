package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

// The semerr section (#34, R12): GHDL and NVC analyze every PR-set
// file (the corpus designs, GHDL's testsuite and NVC's test folder) in
// the analysis groups and order of the projects' own drivers, and the
// golden files record, per analysis, each tool's exit code and the
// lines of the file where it reports an error. The check requires
// GoHard to report an error on every line where both tools do (see
// semerr_check.go).

// fieldSemerr is the corpus.yaml hash field of the semerr section, in
// each corpus design and in the ghdl and nvc regression entries.
const fieldSemerr = "semerr_sha256"

// semerrDir holds every semerr golden file.
const semerrDir = "testdata/corpus/golden/semerr"

var semerrSection = &section{
	name:    "semerr",
	targets: semerrTargets,
	record:  recordSemerr,
	check:   checkSemerr,
}

// semManifest is the part of corpus.yaml the semerr section reads
// beyond manifest.
type semManifest struct {
	Designs []struct {
		Name    string   `yaml:"name"`
		Follows []follow `yaml:"semerr_follows"`
	} `yaml:"designs"`
	Regression []struct {
		Name      string   `yaml:"name"`
		Path      string   `yaml:"path"`
		Tag       string   `yaml:"tag"`
		Subfolder string   `yaml:"subfolder"`
		Follows   []follow `yaml:"semerr_follows"`
	} `yaml:"regression"`
}

// follow is one recorded disagreement (`semerr_follows` in a design or
// regression entry): on line Line of file Path (relative to the design
// directory or the entry's subfolder) GHDL and NVC disagree, and GoHard
// follows the named side for the reason given by the LRM section. The
// check then requires GoHard to report an error there exactly when
// that side does.
type follow struct {
	Path    string `yaml:"path"`
	Line    int    `yaml:"line"`
	Follows string `yaml:"follows"`
	LRM     string `yaml:"lrm"`
	Notes   string `yaml:"notes"`
}

// semTarget is the pure plan of one semerr target.
type semTarget struct {
	ref     entryRef
	plan    *semPlan
	follows map[string]follow // repo path + ":" + line
}

// semTargets returns the semerr targets: every corpus design, then the
// ghdl and nvc regression entries (the PR set's submodules).
func semTargetsOf(x *ctx) ([]*semTarget, error) {
	if ts, ok := x.plans["semerr"].([]*semTarget); ok {
		return ts, nil
	}
	var sm semManifest
	if err := yaml.Unmarshal(x.src, &sm); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	var out []*semTarget
	for _, d := range x.man.Designs {
		p, err := planDesign(x, d)
		if err != nil {
			return nil, err
		}
		t := &semTarget{ref: entryRef{List: "designs", Name: d.Name,
			Field: fieldSemerr, After: fieldFormat}, plan: p,
			follows: map[string]follow{}}
		for _, e := range sm.Designs {
			if e.Name == d.Name {
				addFollows(t, d.dir(), e.Follows)
			}
		}
		out = append(out, t)
	}
	for _, e := range sm.Regression {
		if e.Tag != "pr" {
			continue
		}
		dir := path.Join(e.Path, path.Clean(e.Subfolder))
		var (
			p   *semPlan
			err error
		)
		switch e.Name {
		case "ghdl":
			p, err = planGHDL(x.root, dir)
		case "nvc":
			p, err = planNVC(x.root, dir)
		default:
			err = fmt.Errorf("no semerr plan for the PR entry %s", e.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("regression %s: %w (a missing "+
				"checkout needs git submodule update --init %s)", e.Name,
				err, e.Path)
		}
		t := &semTarget{ref: entryRef{List: "regression", Name: e.Name,
			Field: fieldSemerr, After: "subfolder"}, plan: p,
			follows: map[string]follow{}}
		addFollows(t, dir, e.Follows)
		out = append(out, t)
	}
	for _, t := range out {
		sortRunsStable(t.plan.Runs)
	}
	x.plans["semerr"] = out
	return out, nil
}

func addFollows(t *semTarget, dir string, fs []follow) {
	for _, f := range fs {
		t.follows[path.Join(dir, f.Path)+":"+strconv.Itoa(f.Line)] = f
	}
}

func semerrTargets(x *ctx) ([]target, error) {
	ts, err := semTargetsOf(x)
	if err != nil {
		return nil, err
	}
	var out []target
	for _, t := range ts {
		out = append(out, target{Ref: t.ref, Dir: semerrDir,
			Files: t.plan.goldens()})
	}
	return out, nil
}

// goldens are the golden file names of the plan's runs, sorted.
func (p *semPlan) goldens() []string {
	var out []string
	for _, r := range p.Runs {
		if !slices.Contains(out, r.Golden) {
			out = append(out, r.Golden)
		}
	}
	slices.Sort(out)
	return out
}

func semTargetFor(x *ctx, ref entryRef) (*semTarget, error) {
	ts, err := semTargetsOf(x)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.ref == ref {
			return t, nil
		}
	}
	return nil, fmt.Errorf("no semerr target %s", ref)
}

// fileSpec is the plan part of a file line: the argument, the library
// when not work, and the GHDL options.
func fileSpec(f semFile) string {
	s := f.Arg
	if f.Lib != work {
		s += " lib=" + f.Lib
	}
	for _, o := range f.Flags {
		s += " " + o
	}
	return s
}

// runLine is the plan line of a run; sum is its source hash.
func runLine(r *semRun, sum string) string {
	s := fmt.Sprintf("run %s dir=%s std=%s", r.ID, r.Dir, r.GHDLStd)
	for _, d := range r.Defines {
		s += " define=" + d.Name + "=" + d.Value
	}
	return s + " sha=" + sum
}

// runSum hashes the paths and bytes of a run's files (16 hex digits).
func runSum(root string, r *semRun) (string, error) {
	h := sha256.New()
	for _, f := range r.Files {
		b, err := readRepo(root, f.Path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\n%d\n", f.Path, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// toolResult is one tool's analysis of one file: the exit code, the
// sorted lines of the file it reports errors on, and the subset of
// those lines whose only errors are cascades (GHDL's "<unit> was not
// analysed": the unit failed because of an error elsewhere).
type toolResult struct {
	Exit    int
	Lines   []int
	Cascade []int
}

// cascadeMsg matches GHDL's cascade errors.
var cascadeMsg = regexp.MustCompile(`was not analysed$`)

// String is the compact form: exit code, then "@" and the lines, a
// cascade line marked with "~".
func (t toolResult) String() string {
	s := strconv.Itoa(t.Exit)
	if len(t.Lines) == 0 {
		return s
	}
	ls := make([]string, len(t.Lines))
	for i, l := range t.Lines {
		ls[i] = strconv.Itoa(l)
		if slices.Contains(t.Cascade, l) {
			ls[i] += "~"
		}
	}
	return s + "@" + strings.Join(ls, ",")
}

func parseToolResult(s string) (toolResult, error) {
	e, ls, _ := strings.Cut(s, "@")
	var t toolResult
	var err error
	if t.Exit, err = strconv.Atoi(e); err != nil {
		return t, err
	}
	if ls == "" {
		return t, nil
	}
	for l := range strings.SplitSeq(ls, ",") {
		c, cascade := strings.CutSuffix(l, "~")
		n, err := strconv.Atoi(c)
		if err != nil {
			return t, err
		}
		t.Lines = append(t.Lines, n)
		if cascade {
			t.Cascade = append(t.Cascade, n)
		}
	}
	return t, nil
}

const semerrHeader = `# GoHard semantic-error oracle (#34, R12): %s.
# Written by go run ./tools/oracle --update semerr; do not edit.
# Each "run" is one analysis group of the project's own driver (a
# testsuite.sh between cleans, a VESTS list, an NVC regression test or
# unit-test block, a corpus design run, or a file no driver names,
# alone under 2008): its files are analyzed in order, one command per
# file, in a fresh work directory per tool ("dir" is the working
# directory; the run's GHDL --std and the sha256 prefix of its sources
# follow). Each file line is the argument, the library when not work,
# the GHDL options, then per tool the exit code and the lines of that
# file it reports errors on; "~" marks a line whose only errors are
# cascades ("<unit> was not analysed"). tools/oracle checks that GoHard
# reports an error on every line both tools report one on
# (semerr_check.go).
`

// semRecorded is a golden file read back: the tool lines and every
// run with its file results.
type semRecorded struct {
	tools []string
	runs  []recRun
}

type recRun struct {
	line  string
	files []recFile
}

type recFile struct {
	spec      string
	ghdl, nvc toolResult
}

func parseSemGolden(b []byte) (*semRecorded, error) {
	g := &semRecorded{}
	for i, l := range strings.Split(string(b), "\n") {
		switch {
		case l == "" || strings.HasPrefix(l, "#"):
		case strings.HasPrefix(l, "tool "):
			g.tools = append(g.tools, l)
		case strings.HasPrefix(l, "run "):
			g.runs = append(g.runs, recRun{line: l})
		case strings.HasPrefix(l, " ") && len(g.runs) > 0:
			spec, rest, ok := strings.Cut(l[1:], " ghdl=")
			gs, ns, ok2 := strings.Cut(rest, " nvc=")
			if !ok || !ok2 {
				return nil, fmt.Errorf("line %d: bad file line", i+1)
			}
			gr, err := parseToolResult(gs)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			nr, err := parseToolResult(ns)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			r := &g.runs[len(g.runs)-1]
			r.files = append(r.files, recFile{spec: spec, ghdl: gr,
				nvc: nr})
		default:
			return nil, fmt.Errorf("line %d: unexpected %q", i+1, l)
		}
	}
	return g, nil
}

// semJobs is the number of runs recorded or checked at once.
func semJobs() int { return max(1, runtime.NumCPU()) }

// parallel runs job(i) for i in [0, n) on up to k goroutines; results
// are stored by index, so the outcome never depends on scheduling.
func parallel(n, k int, job func(i int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(n, k) {
		wg.Go(func() {
			for i := range next {
				job(i)
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}

func recordSemerr(x *ctx, tl *toolset, t target) ([]goldenFile, error) {
	st, err := semTargetFor(x, t.Ref)
	if err != nil {
		return nil, err
	}
	g, err := tl.get("ghdl")
	if err != nil {
		return nil, err
	}
	n, err := tl.get("nvc")
	if err != nil {
		return nil, err
	}
	runs := st.plan.Runs
	type result struct {
		line       string
		ghdl, nvcs []toolResult
		err        error
	}
	res := make([]result, len(runs))
	parallel(len(runs), semJobs(), func(i int) {
		r := runs[i]
		sum, err := runSum(x.root, r)
		if err != nil {
			res[i].err = err
			return
		}
		res[i].line = runLine(r, sum)
		res[i].ghdl, err = analyzeSemRun(x.root, g, r)
		if err != nil {
			res[i].err = err
			return
		}
		res[i].nvcs, res[i].err = analyzeSemRun(x.root, n, r)
	})
	var out []goldenFile
	for _, name := range t.Files {
		var b strings.Builder
		fmt.Fprintf(&b, semerrHeader, name)
		fmt.Fprintf(&b, "tool ghdl %s\ntool nvc %s\n", g.version,
			n.version)
		for i, r := range runs {
			if r.Golden != name {
				continue
			}
			if res[i].err != nil {
				return nil, fmt.Errorf("run %s: %w", r.ID, res[i].err)
			}
			b.WriteString(res[i].line + "\n")
			for j, f := range r.Files {
				fmt.Fprintf(&b, " %s ghdl=%s nvc=%s\n", fileSpec(f),
					res[i].ghdl[j], res[i].nvcs[j])
			}
		}
		out = append(out, goldenFile{Name: name, Data: []byte(b.String())})
	}
	return out, nil
}

// semTimeout bounds one tool command; a command that runs longer is
// recorded with exit code -1.
const semTimeout = 2 * time.Minute

// analyzeSemRun analyzes the files of r in order with one tool in a
// fresh work directory holding copies of the run's files at their
// repository paths.
func analyzeSemRun(root string, tt *tool, r *semRun) ([]toolResult,
	error) {
	files := map[string][]byte{}
	var order []string
	for _, f := range r.Files {
		if _, ok := files[f.Path]; ok {
			continue
		}
		b, err := readRepo(root, f.Path)
		if err != nil {
			return nil, err
		}
		files[f.Path] = b
		order = append(order, f.Path)
	}
	w, err := newWorkDir(files, order)
	if err != nil {
		return nil, err
	}
	defer w.close()
	cwd := filepath.Join(w.dir, filepath.FromSlash(r.Dir))
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return nil, err
	}
	var out []toolResult
	for _, f := range r.Files {
		args := semArgs(tt.name, r, f)
		for _, o := range f.Flags {
			if d, ok := strings.CutPrefix(o, "--workdir="); ok {
				if err := os.MkdirAll(filepath.Join(cwd,
					filepath.FromSlash(d)), 0o755); err != nil {
					return nil, err
				}
			}
		}
		text, code, err := runTool(tt, cwd, args)
		if err != nil {
			return nil, err
		}
		var ds []toolDiag
		if tt.name == "ghdl" {
			ds = parseGHDL(text, f.Arg)
		} else {
			ds = parseNVC(text, f.Arg)
		}
		res := toolResult{Exit: code}
		primary := map[int]bool{}
		for _, d := range ds {
			if d.Line <= 0 || path.Clean(path.Join(r.Dir, d.File)) !=
				f.Path {
				continue
			}
			if !slices.Contains(res.Lines, d.Line) {
				res.Lines = append(res.Lines, d.Line)
			}
			if !cascadeMsg.MatchString(d.Msg) {
				primary[d.Line] = true
			}
		}
		slices.Sort(res.Lines)
		for _, l := range res.Lines {
			if !primary[l] {
				res.Cascade = append(res.Cascade, l)
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// semArgs is the analysis command of f for one tool.
func semArgs(name string, r *semRun, f semFile) []string {
	if name == "ghdl" {
		a := []string{"-a", "--std=" + r.GHDLStd}
		if f.Lib != work {
			a = append(a, "--work="+f.Lib)
		}
		a = append(a, f.Flags...)
		return append(a, f.Arg)
	}
	a := []string{"--std=" + r.Std.String()}
	if f.Lib != work {
		a = append(a, "--work="+f.Lib)
	}
	a = append(a, "-L", ".", "-a", "--error-limit=0")
	if f.relaxed() {
		a = append(a, "--relaxed")
	}
	if f.psl() {
		a = append(a, "--psl")
	}
	for _, d := range r.Defines {
		a = append(a, "-D", d.Name+"="+d.Value)
	}
	return append(a, f.Arg)
}

// runTool runs tt like tool.run, with the semTimeout bound.
func runTool(tt *tool, dir string, args []string) (string, int, error) {
	if tt.fake != nil {
		out, code := tt.fake(dir, args)
		return out, code, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), semTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tt.path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NVC_COLORS=never", "TERM=dumb")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), -1, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode(), nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("%s: %w", tt.name, err)
	}
	return string(out), 0, nil
}
