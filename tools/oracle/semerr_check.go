package main

import (
	"fmt"
	"path"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// The semerr check (#34 Done when): wherever GHDL and NVC both report
// an error on a line of an analyzed file, GoHard reports an error on
// that line, when GoHard analyzes the same file with the files of its
// run analyzed before it that it may depend on (its dependency
// closure). A file is checked only when every file of its closure is
// clean in both tools: a unit a tool rejected is missing for it, and
// its errors on dependents (a cascade) say nothing about the file. A
// recorded disagreement (semerr_follows) adds the line with the side
// GoHard follows. Known gaps are listed in gaps.txt; the check fails on
// a gap not listed and on a listed gap GoHard has closed, so the list
// only shrinks.

// semGapsName is the known-gap list in semerrDir. It is not a golden
// file: GoHard's results decide it, and
// go test ./tools/oracle -run TestSemerrGaps -update-gaps rewrites it.
const semGapsName = "gaps.txt"

// gapsHint is the hint printed with every gap-list problem.
const gapsHint = "fix sema or rerun go test ./tools/oracle " +
	"-run TestSemerrGaps -update-gaps"

// semStats counts the outcome of one golden file.
type semStats struct {
	Runs, Files int
	// GHDLFiles and NVCFiles count analyses with an error line.
	GHDLFiles, NVCFiles int
	// Agree counts lines both tools report. Of those, Cascade are
	// GHDL cascades, Excluded are in files whose closure is not clean,
	// Opaque in files GoHard reports as opaque, AfterSyntax after
	// GoHard's first syntax error; Gated are the lines checked, and
	// Hits those GoHard reports an error on.
	Agree, Cascade, Excluded, Opaque, AfterSyntax, Gated, Hits int
	// GHDLOnly and NVCOnly count lines only one tool reports (in
	// gated files); Followed the recorded ones.
	GHDLOnly, NVCOnly, Followed int
	// With semCountFalse: CleanFiles counts the checked analyses both
	// tools accept, FalseFiles those GoHard reports an error in.
	CleanFiles, FalseFiles int
}

// semCountFalse makes the check also analyze the files both tools
// accept and count GoHard's errors there (TestSemerrGaps
// -semerr-false); the gate itself never needs it.
var semCountFalse bool

func (s *semStats) add(o semStats) {
	s.Runs += o.Runs
	s.Files += o.Files
	s.GHDLFiles += o.GHDLFiles
	s.NVCFiles += o.NVCFiles
	s.Agree += o.Agree
	s.Cascade += o.Cascade
	s.Excluded += o.Excluded
	s.Opaque += o.Opaque
	s.AfterSyntax += o.AfterSyntax
	s.Gated += o.Gated
	s.Hits += o.Hits
	s.GHDLOnly += o.GHDLOnly
	s.NVCOnly += o.NVCOnly
	s.Followed += o.Followed
	s.CleanFiles += o.CleanFiles
	s.FalseFiles += o.FalseFiles
}

func (s semStats) String() string {
	return fmt.Sprintf("runs=%d files=%d ghdl-error-files=%d "+
		"nvc-error-files=%d agreed=%d cascade=%d excluded=%d opaque=%d "+
		"after-syntax=%d checked=%d gohard=%d gaps=%d ghdl-only=%d "+
		"nvc-only=%d followed=%d", s.Runs, s.Files, s.GHDLFiles,
		s.NVCFiles, s.Agree, s.Cascade, s.Excluded, s.Opaque,
		s.AfterSyntax, s.Gated, s.Hits, s.Gated-s.Hits, s.GHDLOnly,
		s.NVCOnly, s.Followed) + fmt.Sprintf(" clean=%d gohard-errors-in"+
		"-clean=%d", s.CleanFiles, s.FalseFiles)
}

// semResults collects the stats and the gaps of every checked golden
// file of the process (TestSemerrGaps and TestSemerrStats read them).
var semResults = struct {
	sync.Mutex
	stats map[string]semStats
	gaps  map[string][]string
	false []string
}{stats: map[string]semStats{}, gaps: map[string][]string{}}

// readGaps returns the known gaps of the golden file names, by name.
func readGaps(x *ctx) map[string]bool {
	if g, ok := x.plans["semerr/gaps"].(map[string]bool); ok {
		return g
	}
	out := map[string]bool{}
	b, err := readRepo(x.root, path.Join(semerrDir, semGapsName))
	if err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l != "" && !strings.HasPrefix(l, "#") {
				out[l] = true
			}
		}
	}
	x.plans["semerr/gaps"] = out
	return out
}

// maxListed caps the problems of one kind listed per golden file.
const maxListed = 20

func checkSemerr(x *ctx, t target, golden []goldenFile) []string {
	st, err := semTargetFor(x, t.Ref)
	if err != nil {
		return []string{err.Error()}
	}
	known := readGaps(x)
	var probs []string
	for _, g := range golden {
		rec, err := parseSemGolden(g.Data)
		if err != nil {
			probs = append(probs, fmt.Sprintf("%s/%s: %v: %s", t.Dir,
				g.Name, err, rerun))
			continue
		}
		var runs []*semRun
		for _, r := range st.plan.Runs {
			if r.Golden == g.Name {
				runs = append(runs, r)
			}
		}
		if p := stalePlan(x.root, runs, rec); p != "" {
			probs = append(probs, fmt.Sprintf("%s/%s: %s: %s", t.Dir,
				g.Name, p, rerun))
			continue
		}
		outs := make([]runOutcome, len(runs))
		parallel(len(runs), semJobs(), func(i int) {
			outs[i] = evalRun(x.root, runs[i], rec.runs[i], st.follows)
		})
		var (
			stats  semStats
			gaps   []string
			others []string
		)
		for _, o := range outs {
			stats.add(o.stats)
			for _, gp := range o.gaps {
				gaps = append(gaps, g.Name+" "+gp)
			}
			others = append(others, o.probs...)
		}
		semResults.Lock()
		semResults.stats[g.Name] = stats
		semResults.gaps[g.Name] = gaps
		for _, o := range outs {
			semResults.false = append(semResults.false, o.false...)
		}
		semResults.Unlock()
		var unlisted, closed []string
		for _, gp := range gaps {
			if !known[gp] {
				unlisted = append(unlisted, gp)
			}
		}
		for k := range known {
			if strings.HasPrefix(k, g.Name+" ") && !slices.Contains(gaps,
				k) {
				closed = append(closed, k)
			}
		}
		slices.Sort(closed)
		probs = append(probs, listed(others,
			func(s string) string { return s })...)
		probs = append(probs, listed(unlisted, func(s string) string {
			return "GHDL and NVC report an error on " + s +
				", GoHard does not: " + gapsHint
		})...)
		probs = append(probs, listed(closed, func(s string) string {
			return "the known gap " + s + " is closed (or gone): " +
				gapsHint
		})...)
	}
	return probs
}

// listed formats up to maxListed items and a count of the rest.
func listed(items []string, f func(string) string) []string {
	var out []string
	for i, s := range items {
		if i == maxListed {
			out = append(out, fmt.Sprintf("... and %d more",
				len(items)-maxListed))
			break
		}
		out = append(out, f(s))
	}
	return out
}

// stalePlan compares the recorded runs and file specs with the plan
// and returns the first difference ("" when they match).
func stalePlan(root string, runs []*semRun, rec *semRecorded) string {
	for i, r := range runs {
		sum, err := runSum(root, r)
		if err != nil {
			return err.Error()
		}
		want := runLine(r, sum)
		if i >= len(rec.runs) {
			return fmt.Sprintf("run %s is not recorded", r.ID)
		}
		if rec.runs[i].line != want {
			return fmt.Sprintf("plan or sources changed (want %q, "+
				"recorded %q)", want, rec.runs[i].line)
		}
		fs := rec.runs[i].files
		if len(fs) != len(r.Files) {
			return fmt.Sprintf("run %s: %d files recorded, %d planned",
				r.ID, len(fs), len(r.Files))
		}
		for j, f := range r.Files {
			if fs[j].spec != fileSpec(f) {
				return fmt.Sprintf("run %s: file %q recorded as %q", r.ID,
					fileSpec(f), fs[j].spec)
			}
		}
	}
	if len(rec.runs) > len(runs) {
		return fmt.Sprintf("%q is no longer planned",
			rec.runs[len(runs)].line)
	}
	return ""
}

// runOutcome is the check of one run.
type runOutcome struct {
	stats semStats
	false []string // with semCountFalse: first GoHard error per file
	gaps  []string // "<run id> <path>:<line>", sorted
	probs []string
}

// evalRun checks one recorded run against GoHard.
func evalRun(root string, r *semRun, rec recRun,
	follows map[string]follow) runOutcome {
	o := runOutcome{stats: semStats{Runs: 1, Files: len(r.Files)}}
	texts := make([][]byte, len(r.Files))
	for i, f := range r.Files {
		b, err := readRepo(root, f.Path)
		if err != nil {
			o.probs = append(o.probs, err.Error())
			return o
		}
		texts[i] = b
	}
	idx := newUnitIndex(texts)
	for i, f := range r.Files {
		g, n := rec.files[i].ghdl, rec.files[i].nvc
		if len(g.Lines) > 0 {
			o.stats.GHDLFiles++
		}
		if len(n.Lines) > 0 {
			o.stats.NVCFiles++
		}
		all := intersect(g.Lines, n.Lines)
		o.stats.Agree += len(all)
		agreed := minusInts(all, g.Cascade)
		o.stats.Cascade += len(all) - len(agreed)
		closure := idx.closure(i)
		clean := true
		for _, j := range closure {
			if rec.files[j].ghdl.Exit != 0 || rec.files[j].nvc.Exit != 0 {
				clean = false
			}
		}
		if !clean {
			o.stats.Excluded += len(agreed)
			continue
		}
		ghdlOnly := minusInts(g.Lines, n.Lines)
		nvcOnly := minusInts(n.Lines, g.Lines)
		o.stats.GHDLOnly += len(ghdlOnly)
		o.stats.NVCOnly += len(nvcOnly)
		// The recorded disagreements of this file.
		var fl []follow
		for _, l := range append(slices.Clone(ghdlOnly), nvcOnly...) {
			if e, ok := follows[f.Path+":"+strconv.Itoa(l)]; ok {
				fl = append(fl, e)
			}
		}
		bothClean := g.Exit == 0 && n.Exit == 0
		if len(agreed) == 0 && len(fl) == 0 &&
			!(semCountFalse && bothClean) {
			continue
		}
		gh, err := gohardErrorLines(r, texts, closure, i)
		if err != "" {
			o.probs = append(o.probs, fmt.Sprintf("run %s: %s: GoHard "+
				"panics: %s", r.ID, f.Path, err))
			continue
		}
		if gh.opaque {
			o.stats.Opaque += len(agreed)
			continue
		}
		if semCountFalse && bothClean {
			o.stats.CleanFiles++
			if len(gh.errs) > 0 {
				o.stats.FalseFiles++
				o.false = append(o.false, gh.first)
			}
		}
		for _, l := range agreed {
			switch {
			case gh.syntax > 0 && l > gh.syntax:
				o.stats.AfterSyntax++
			case gh.errs[l]:
				o.stats.Gated++
				o.stats.Hits++
			default:
				o.stats.Gated++
				o.gaps = append(o.gaps, fmt.Sprintf("%s %s:%d", r.ID,
					f.Path, l))
			}
		}
		for _, e := range fl {
			o.stats.Followed++
			var side toolResult
			switch e.Follows {
			case "ghdl":
				side = g
			case "nvc":
				side = n
			default:
				o.probs = append(o.probs, fmt.Sprintf("%s:%d: "+
					"semerr_follows names %q, want ghdl or nvc", f.Path,
					e.Line, e.Follows))
				continue
			}
			want := slices.Contains(side.Lines, e.Line)
			if gh.errs[e.Line] != want {
				o.probs = append(o.probs, fmt.Sprintf("run %s: %s:%d: "+
					"GoHard follows %s here (LRM %s), which reports %s "+
					"error, but GoHard reports %s error", r.ID, f.Path,
					e.Line, e.Follows, e.LRM, an(want),
					an(gh.errs[e.Line])))
			}
		}
	}
	slices.Sort(o.gaps)
	o.gaps = slices.Compact(o.gaps)
	return o
}

func an(b bool) string {
	if b {
		return "an"
	}
	return "no"
}

func intersect(a, b []int) []int {
	var out []int
	for _, x := range a {
		if slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func minusInts(a, b []int) []int {
	var out []int
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// virtualPath is the path GoHard sees for f: work files keep their
// repository path; a file of library L is placed under lib.L/, a
// folder the configuration maps to L.
func virtualPath(f semFile) string {
	if f.Lib == work {
		return f.Path
	}
	return "lib." + f.Lib + "/" + f.Path
}

// gohardFile is GoHard's analysis of one file of a run.
type gohardFile struct {
	// errs are the lines an error diagnostic covers (from its first to
	// its last line).
	errs map[int]bool
	// syntax is the first line of a syntax error, 0 for none: error
	// recovery after a syntax error is implementation-defined, so the
	// lines after it are not checked.
	syntax int
	// first is the first error as "path:line: code: message".
	first string
	// opaque: GoHard reports a tool problem in the file (an
	// unsupported construct or encrypted IP, exit 2), which makes its
	// units opaque instead of checked (#20, R3).
	opaque bool
}

// maxSpan is the longest error range, in lines, that counts for every
// line it covers; a longer one counts for its first line only.
const maxSpan = 3

// gohardErrorLines analyzes file i of r with its closure. A panic is
// returned as text.
func gohardErrorLines(r *semRun, texts [][]byte, closure []int,
	i int) (out gohardFile, panicked string) {
	defer func() {
		if p := recover(); p != nil {
			out, panicked = gohardFile{}, fmt.Sprintf("%v\n%s", p,
				debug.Stack())
		}
	}()
	cfg := config.Defaults()
	cfg.Standard = r.Std.String()
	cfg.PSL.Comments = r.Files[i].psl()
	cfg.Defines = r.Defines
	own := virtualPath(r.Files[i])
	at := map[string]int{} // virtual path -> latest index
	var libs []string
	for _, j := range append(slices.Clone(closure), i) {
		f := r.Files[j]
		at[virtualPath(f)] = j
		if f.Lib != work && !slices.Contains(libs, f.Lib) {
			libs = append(libs, f.Lib)
		}
	}
	slices.Sort(libs)
	for _, l := range libs {
		cfg.Libraries = append(cfg.Libraries, config.Library{Name: l,
			Folders: []config.Folder{{Path: "lib." + l}}})
	}
	var srcs []sema.Source
	for _, j := range append(slices.Clone(closure), i) {
		vp := virtualPath(r.Files[j])
		if at[vp] != j {
			continue // analyzed again later: the later one counts
		}
		srcs = append(srcs, sema.Source{Path: vp, Text: texts[j]})
	}
	res := sema.Analyze(sema.Input{Files: srcs, Config: cfg,
		ToolVersion: toolVersion})
	out.errs = map[int]bool{}
	for _, d := range res.Diagnostics {
		if d.Severity != diag.Error || d.Path != own {
			continue
		}
		if d.Class() == diag.Tool {
			out.opaque = true
		}
		if out.first == "" {
			out.first = fmt.Sprintf("%s:%d: %s: %s", d.Path, d.Line,
				d.Code, d.Message)
		}
		if strings.HasPrefix(string(d.Code), "syntax/") &&
			(out.syntax == 0 || d.Line < out.syntax) {
			out.syntax = d.Line
		}
		end := d.EndLine
		if end < d.Line || end-d.Line >= maxSpan {
			end = d.Line
		}
		for l := d.Line; l <= end; l++ {
			out.errs[l] = true
		}
	}
	return out, ""
}

// unitIndex finds, for each file of a run, the earlier files it may
// depend on: those declaring a design unit whose name the file (or a
// dependency, transitively) mentions. The scan is lexical and
// over-approximates (any identifier counts as a mention), which only
// adds clean files to an analysis.
type unitIndex struct {
	words [][]string
	defs  [][]unitDef
	// by maps a unit name to its definers in file order.
	by map[string][]definer
}

type unitDef struct {
	name    string
	primary bool // entity, package, configuration, context
	// of is the entity of an architecture: the architecture is a
	// dependency only of a file naming both.
	of string
}

type definer struct {
	file    int
	primary bool
	of      string
}

var (
	// semNoise are comments and string literals: their words name no
	// unit.
	semNoise   = regexp.MustCompile(`--[^\n]*|"[^"\n]*"`)
	semPrimary = regexp.MustCompile(`\b(?:entity|package|configuration|` +
		`context)\s+([a-z][a-z0-9_]*)\s+is\b`)
	semBody = regexp.MustCompile(`\bpackage\s+body\s+([a-z][a-z0-9_]*)` +
		`\s+is\b`)
	semArch = regexp.MustCompile(`\barchitecture\s+([a-z][a-z0-9_]*)` +
		`\s+of\s+([a-z][a-z0-9_]*)\s+is\b`)
	semWord = regexp.MustCompile(`[a-z][a-z0-9_]*`)
)

func newUnitIndex(texts [][]byte) *unitIndex {
	x := &unitIndex{words: make([][]string, len(texts)),
		defs: make([][]unitDef, len(texts)), by: map[string][]definer{}}
	for i, t := range texts {
		s := semNoise.ReplaceAllString(strings.ToLower(string(t)), " ")
		ws := semWord.FindAllString(s, -1)
		slices.Sort(ws)
		x.words[i] = slices.Compact(ws)
		for _, m := range semPrimary.FindAllStringSubmatch(s, -1) {
			x.defs[i] = append(x.defs[i], unitDef{name: m[1],
				primary: true})
		}
		for _, m := range semBody.FindAllStringSubmatch(s, -1) {
			x.defs[i] = append(x.defs[i], unitDef{name: m[1]})
		}
		for _, m := range semArch.FindAllStringSubmatch(s, -1) {
			x.defs[i] = append(x.defs[i], unitDef{name: m[1], of: m[2]})
		}
		for _, d := range x.defs[i] {
			x.by[d.name] = append(x.by[d.name], definer{i, d.primary,
				d.of})
		}
	}
	return x
}

// mentions reports whether file f has the word w.
func (x *unitIndex) mentions(f int, w string) bool {
	_, ok := slices.BinarySearch(x.words[f], w)
	return ok
}

// closure returns the sorted indexes of the earlier files file i may
// depend on. For a name, the definers before i from its last primary
// unit on count (a later analysis replaces an earlier one); a name file
// i declares itself excludes the earlier declarations of the same kind
// (all of them, for a primary unit).
func (x *unitIndex) closure(i int) []int {
	own := map[string]int{} // 1 secondary, 2 primary
	for _, d := range x.defs[i] {
		k := 1
		if d.primary {
			k = 2
		}
		own[d.name] = max(own[d.name], k)
	}
	seen := map[int]bool{}
	queue := []int{i}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		for _, w := range x.words[f] {
			ds := x.by[w]
			if len(ds) == 0 || own[w] == 2 {
				continue
			}
			from := 0
			for k, d := range ds {
				if d.file < i && d.primary {
					from = k
				}
			}
			for _, d := range ds[from:] {
				if d.file >= i || seen[d.file] ||
					(own[w] == 1 && !d.primary) ||
					d.of != "" && !x.mentions(f, d.of) {
					continue
				}
				seen[d.file] = true
				queue = append(queue, d.file)
			}
		}
	}
	out := make([]int, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}
