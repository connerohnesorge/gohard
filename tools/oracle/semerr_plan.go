package main

import (
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

// The plan of the semerr section: which files GHDL and NVC analyze, in
// which groups and order, read from the projects' own test drivers. It
// is pure: corpus.yaml, the submodule checkouts and the corpus only.

// semFile is one analysis of one file in a run.
type semFile struct {
	Path string // repository-relative slash path
	Arg  string // the path as given to the tools, relative to the run dir
	Lib  string // work library, lower case
	// Flags are the extra GHDL options of this analysis, in order:
	// -frelaxed (or -frelaxed-rules) also gives NVC --relaxed, -fpsl
	// gives NVC --psl and GoHard psl.comments.
	Flags []string
}

func (f semFile) relaxed() bool {
	return slices.Contains(f.Flags, "-frelaxed") ||
		slices.Contains(f.Flags, "-frelaxed-rules")
}

func (f semFile) psl() bool { return slices.Contains(f.Flags, "-fpsl") }

// semRun is one analysis group: files analyzed in order into the same
// work libraries, under one standard. Each tool analyzes a run in a
// fresh work directory, one command per file.
type semRun struct {
	ID      string // unique within the section
	Golden  string // the golden file the run is recorded in
	Dir     string // repository-relative directory the tools run in
	Std     token.Standard
	GHDLStd string // the GHDL --std value ("93c", "08", ...)
	Defines []config.Define
	Files   []semFile
}

// semPlan is the plan of one target (a regression entry or a corpus
// design): its runs in a fixed order.
type semPlan struct {
	Runs []*semRun
	// Skipped counts analyses no run holds: GHDL --std=87 commands
	// (GoHard has no VHDL-1987 mode) and references to files the
	// scripts generate.
	Skipped int
}

const work = "work"

// ghdlStdOf maps a GHDL --std value to the GoHard standard; ok is false
// for 87 and unknown values.
func ghdlStdOf(v string) (token.Standard, bool) {
	switch v {
	case "93", "93c":
		return token.Std1993, true
	case "00", "02":
		return token.Std2002, true
	case "08":
		return token.Std2008, true
	case "19":
		return token.Std2019, true
	}
	return 0, false
}

// ghdlFlagPrefixes are the script options passed on to `ghdl -a`;
// others (-e, --expect-failure, --out=, ...) belong to elaboration or
// synthesis.
var ghdlFlagPrefixes = []string{"-f", "--ieee=", "-P", "--workdir=",
	"-W", "--warn-", "--force-analysis", "-C", "--mb-comments"}

func ghdlFlag(o string) bool {
	for _, p := range ghdlFlagPrefixes {
		if strings.HasPrefix(o, p) {
			return true
		}
	}
	return false
}

// ghdlAnalysis is one analysis command of a GHDL script.
type ghdlAnalysis struct {
	dir     string // repository-relative
	ghdlStd string
	files   []semFile
}

// ghdlCommands are the testenv.sh helpers whose file operands are
// analyzed (synth analyzes its files in memory; the plan analyzes them
// with -a, which reports the same analysis errors).
var ghdlCommands = map[string]bool{
	"analyze": true, "analyze_failure": true, "elab_simulate": true,
	"elab_simulate_failure": true, "synth": true, "synth_failure": true,
}

// ghdlHelperFiles are the testenv.sh helpers taking a unit name:
// the files they analyze and whether they clean the library after.
var ghdlHelperFiles = map[string]struct {
	files []string
	clean bool
}{
	"synth_only":    {[]string{"%s.vhdl"}, false},
	"synth_analyze": {[]string{"%s.vhdl"}, false},
	"synth_tb":      {[]string{"%s.vhdl", "tb_%s.vhdl"}, true},
	"synth_vlg_tb":  {[]string{"tb_%s.vhdl"}, true},
}

// ghdlScriptRuns interprets one testsuite.sh (slash path relative to
// the testsuite folder root, repoDir is that folder in the repository)
// and returns its runs: analyses in script order, grouped by work
// directory and standard; `clean` (any form) ends the open groups of
// its directory. A top-level exit ends the script.
func ghdlScriptRuns(root, repoDir, script string, src []byte,
	golden string) ([]*semRun, int) {
	dir := path.Dir(script)
	vars := map[string]string{}
	var (
		runs    []*semRun
		open    = map[string]*semRun{} // dir + "\x00" + std
		skipped int
		stopped bool
	)
	closeDir := func(d string) {
		for k := range open {
			if strings.HasPrefix(k, d+"\x00") {
				delete(open, k)
			}
		}
	}
	add := func(a ghdlAnalysis) {
		std, ok := ghdlStdOf(a.ghdlStd)
		for _, f := range a.files {
			full := path.Clean(path.Join(repoDir, a.dir, f.Arg))
			if !ok || !fileExists(root, full) {
				skipped++
				continue
			}
			key := a.dir + "\x00" + std.String()
			r := open[key]
			if r == nil {
				r = &semRun{ID: fmt.Sprintf("%s#%d", path.Dir(script),
					len(runs)+1), Golden: golden,
					Dir: path.Join(repoDir, a.dir), Std: std,
					GHDLStd: a.ghdlStd}
				runs = append(runs, r)
				open[key] = r
			}
			f.Path = full
			r.Files = append(r.Files, f)
		}
	}
	var exec func(cmds []shCmd)
	exec = func(cmds []shCmd) {
		for _, c := range cmds {
			if stopped {
				return
			}
			osDir := filepath.Join(root, filepath.FromSlash(
				path.Join(repoDir, dir)))
			if c.forVar != "" {
				var vals []string
				for _, w := range c.forIn {
					vals = append(vals, w.expand(vars, osDir)...)
				}
				old, had := vars[c.forVar]
				for _, v := range vals {
					vars[c.forVar] = v
					exec(c.body)
				}
				if had {
					vars[c.forVar] = old
				} else {
					delete(vars, c.forVar)
				}
				continue
			}
			switch c.words[0].literal() {
			case "exit":
				if !c.inBlock && !c.cond {
					stopped = true
				}
				continue
			case "cd":
				if len(c.words) == 2 && !c.cond {
					to := c.words[1].literal()
					if !strings.Contains(to, "$") && !path.IsAbs(to) {
						dir = path.Join(dir, to)
					}
				}
				continue
			case "clean":
				closeDir(dir)
				continue
			}
			as, clean := ghdlScriptCommand(c, vars, osDir, dir)
			for _, a := range as {
				add(a)
			}
			if clean {
				closeDir(dir)
			}
		}
	}
	exec(parseScript(src))
	return runs, skipped
}

// ghdlScriptCommand applies one simple command: assignments update
// vars; analysis commands return their analyses, and clean reports a
// helper that removes the work library afterwards.
func ghdlScriptCommand(c shCmd, vars map[string]string, osDir,
	dir string) ([]ghdlAnalysis, bool) {
	words := c.words
	if len(words) > 0 && words[0].literal() == "export" {
		for _, w := range words[1:] {
			if lit := w.literal(); shAssign.MatchString(lit) {
				name, _, _ := strings.Cut(lit, "=")
				vars[name] = assignValue(w, vars, osDir)
			}
		}
		return nil, false
	}
	local := vars
	n := 0
	for n < len(words) && shAssign.MatchString(words[n].literal()) {
		n++
	}
	if n > 0 {
		if n < len(words) {
			local = map[string]string{}
			for k, v := range vars {
				local[k] = v
			}
		}
		for _, w := range words[:n] {
			name, _, _ := strings.Cut(w.literal(), "=")
			local[name] = assignValue(w, vars, osDir)
		}
		words = words[n:]
	}
	if len(words) == 0 {
		return nil, false
	}
	name := words[0].literal()
	helper, isHelper := ghdlHelperFiles[name]
	if !ghdlCommands[name] && !isHelper {
		return nil, false
	}
	var args []string
	for _, w := range words[1:] {
		args = append(args, w.expand(local, osDir)...)
	}
	opts := strings.Fields(local["GHDL_STD_FLAGS"] + " " +
		local["GHDL_FLAGS"])
	if strings.HasPrefix(name, "synth") {
		opts = append(opts, strings.Fields(local["GHDL_SYNTH_FLAGS"])...)
	}
	var files []string
	if isHelper && len(args) > 0 {
		for _, p := range helper.files {
			files = append(files, fmt.Sprintf(p, args[0]))
		}
		args = args[1:]
	}
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-"):
			opts = append(opts, a)
		case isVHDLName(a):
			files = append(files, a)
		}
	}
	a := ghdlAnalysis{dir: dir, ghdlStd: "93c"}
	lib := work
	var flags []string
	for _, o := range opts {
		switch {
		case strings.HasPrefix(o, "--std="):
			a.ghdlStd = strings.TrimPrefix(o, "--std=")
		case strings.HasPrefix(o, "--work="):
			lib = strings.ToLower(strings.TrimPrefix(o, "--work="))
		case ghdlFlag(o):
			flags = append(flags, o)
		}
	}
	for _, f := range files {
		a.files = append(a.files, semFile{Arg: path.Clean(f), Lib: lib,
			Flags: flags})
	}
	return []ghdlAnalysis{a}, isHelper && helper.clean
}

func fileExists(root, rel string) bool {
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && fi.Mode().IsRegular()
}

// vhdlFilesUnder lists the regular .vhd/.vhdl files below the
// repository-relative dir, sorted.
func vhdlFilesUnder(root, dir string) ([]string, error) {
	var out []string
	base := filepath.Join(root, filepath.FromSlash(dir))
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry,
		err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && isVHDLName(p) {
			r, _ := filepath.Rel(base, p)
			out = append(out, path.Join(dir, filepath.ToSlash(r)))
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

// planGHDL is the plan of the GHDL testsuite (regression entry dir,
// the testsuite folder): every per-test testsuite.sh in path order,
// then the VESTS lists, then every file no run analyzes, alone.
func planGHDL(root, dir string) (*semPlan, error) {
	osDir := filepath.Join(root, filepath.FromSlash(dir))
	var scripts []string
	err := filepath.WalkDir(osDir, func(p string, d fs.DirEntry,
		err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "testsuite.sh" {
			return nil
		}
		r, _ := filepath.Rel(osDir, p)
		r = filepath.ToSlash(r)
		// The drivers (testsuite.sh, gna/testsuite.sh, ...) run the
		// per-test scripts; vests has its own driver.
		if strings.Count(r, "/") >= 2 && !strings.HasPrefix(r, "vests/") {
			scripts = append(scripts, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(scripts)
	p := &semPlan{}
	for _, s := range scripts {
		src, err := os.ReadFile(filepath.Join(osDir, filepath.FromSlash(s)))
		if err != nil {
			return nil, err
		}
		top, _, _ := strings.Cut(s, "/")
		runs, skipped := ghdlScriptRuns(root, dir, s, src,
			"ghdl-"+top+".txt")
		p.Runs = append(p.Runs, runs...)
		p.Skipped += skipped
	}
	vests, err := planVESTS(root, dir)
	if err != nil {
		return nil, err
	}
	p.Runs = append(p.Runs, vests...)
	if err := addAlone(root, dir, p, "ghdl-alone.txt"); err != nil {
		return nil, err
	}
	return p, nil
}

// planVESTS reads GHDL's VESTS driver (vests/testsuite.sh): each .exp
// list it sources is one run, its tests analyzed in list order into
// the work library (LIBRARY=name selects another), under the driver's
// common_args standard.
func planVESTS(root, dir string) ([]*semRun, error) {
	const driver = "vests/testsuite.sh"
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(
		path.Join(dir, driver))))
	if err != nil {
		return nil, err
	}
	modeOf := map[string]string{}
	defRe := regexp.MustCompile(`^\s*([a-z_]+)\s*\(\)\s*\{\s*` +
		`handle_test\s+([a-z_]+)\b`)
	argsRe := regexp.MustCompile(`^\s*common_args="([^"]*)"`)
	dirRe := regexp.MustCompile(`^\s*dir=(\S+)\s*$`)
	srcRe := regexp.MustCompile(`^\s*\.\s+\$dir/(\S+\.exp)\s*$`)
	std, sub := "93c", ""
	var flags []string
	var runs []*semRun
	for _, s := range strings.Split(string(src), "\n") {
		if m := defRe.FindStringSubmatch(s); m != nil {
			modeOf[m[1]] = m[2]
		} else if m := argsRe.FindStringSubmatch(s); m != nil {
			flags = nil
			for _, o := range strings.Fields(m[1]) {
				if v, ok := strings.CutPrefix(o, "--std="); ok {
					std = v
				} else if ghdlFlag(o) {
					flags = append(flags, o)
				}
			}
		} else if m := dirRe.FindStringSubmatch(s); m != nil {
			sub = path.Join("vests", m[1])
		} else if m := srcRe.FindStringSubmatch(s); m != nil {
			exp := path.Join(sub, m[1])
			b, err := os.ReadFile(filepath.Join(root,
				filepath.FromSlash(path.Join(dir, exp))))
			if err != nil {
				return nil, err
			}
			gs, ok := ghdlStdOf(std)
			if !ok {
				continue
			}
			r := &semRun{ID: "vests:" + exp, Golden: "ghdl-vests.txt",
				Dir: path.Join(dir, path.Dir(exp)), Std: gs, GHDLStd: std}
			for _, line := range strings.Split(string(b), "\n") {
				w := strings.Fields(line)
				if len(w) < 2 || strings.HasPrefix(w[0], "#") ||
					modeOf[w[0]] == "" || !isVHDLName(w[1]) {
					continue
				}
				f := semFile{Arg: path.Clean(w[1]), Lib: work,
					Flags: flags}
				for _, o := range w[2:] {
					if v, ok := strings.CutPrefix(o, "LIBRARY="); ok {
						f.Lib = strings.ToLower(v)
					}
				}
				f.Path = path.Join(r.Dir, f.Arg)
				if fileExists(root, f.Path) {
					r.Files = append(r.Files, f)
				}
			}
			if len(r.Files) > 0 {
				runs = append(runs, r)
			}
		}
	}
	return runs, nil
}

// addAlone appends a run of its own, under VHDL-2008, for every file
// below dir that no run of p analyzes and that no skipped command
// names (files without an upstream expectation; GHDL's
// vests/vhdl-ams and billowitch/disputed are among them).
func addAlone(root, dir string, p *semPlan, golden string) error {
	files, err := vhdlFilesUnder(root, dir)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range p.Runs {
		for _, f := range r.Files {
			seen[f.Path] = true
		}
	}
	for _, f := range files {
		if seen[f] {
			continue
		}
		p.Runs = append(p.Runs, &semRun{ID: "alone:" + f[len(dir)+1:],
			Golden: golden, Dir: path.Dir(f), Std: token.Std2008,
			GHDLStd: "08", Files: []semFile{{Path: f,
				Arg: path.Base(f), Lib: work}}})
	}
	return nil
}

// nvcFlags are the GHDL options of NVC's relaxed and PSL modes.
func nvcFlags(relaxed, psl bool) []string {
	var out []string
	if relaxed {
		out = append(out, "-frelaxed")
	}
	if psl {
		out = append(out, "-fpsl")
	}
	return out
}

// toGHDLStd is the GHDL --std value of a GoHard standard.
func toGHDLStd(s token.Standard) string {
	return fmt.Sprintf("%02d", int(s)%100)
}

// planNVC is the plan of NVC's test folder: one run per
// regress/testlist.txt test (run_regr.c analyzes the test's .vhd
// alone), one run per START_TEST block of the C unit tests and per
// standard the block runs under (its input_from_file files in order),
// identical runs once; then every other file alone.
func planNVC(root, dir string) (*semPlan, error) {
	p := &semPlan{}
	reg, err := nvcRegressRuns(root, dir)
	if err != nil {
		return nil, err
	}
	p.Runs = append(p.Runs, reg...)
	cs, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir),
		"test_*.c"))
	if err != nil {
		return nil, err
	}
	slices.Sort(cs)
	seen := map[string]bool{}
	for _, c := range cs {
		runs, err := nvcUnitRuns(root, dir, filepath.Base(c))
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			k := runKey(r)
			if seen[k] {
				continue
			}
			seen[k] = true
			p.Runs = append(p.Runs, r)
		}
	}
	if err := addAlone(root, dir, p, "nvc-alone.txt"); err != nil {
		return nil, err
	}
	return p, nil
}

// runKey identifies what a run analyzes, ignoring its ID.
func runKey(r *semRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %v", r.Dir, r.Std, r.Defines)
	for _, f := range r.Files {
		b.WriteString("\x00" + fileSpec(f))
	}
	return b.String()
}

// nvcStdOf maps run_regr.c options and STD_xx constants.
var nvcStdOf = map[string]token.Standard{
	"2000": token.Std2002, "2002": token.Std2002, "2008": token.Std2008,
	"2019": token.Std2019, "STD_93": token.Std1993,
	"STD_00": token.Std2002, "STD_02": token.Std2002,
	"STD_08": token.Std2008, "STD_19": token.Std2019,
}

func nvcRegressRuns(root, dir string) ([]*semRun, error) {
	const list = "regress/testlist.txt"
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(
		path.Join(dir, list))))
	if err != nil {
		return nil, err
	}
	var runs []*semRun
	for _, line := range strings.Split(string(src), "\n") {
		w := strings.Fields(line)
		if len(w) == 0 || strings.HasPrefix(w[0], "#") {
			continue
		}
		r := &semRun{ID: "regress:" + w[0], Golden: "nvc-regress.txt",
			Dir: path.Join(dir, "regress"), Std: token.Std1993}
		f := semFile{Arg: w[0] + ".vhd", Lib: work}
		skip, relaxed, psl := false, false, false
		if len(w) > 1 {
			for o := range strings.SplitSeq(w[1], ",") {
				switch {
				case o == "verilog", o == "shell":
					skip = true
				case o == "psl":
					psl = true
				case o == "relaxed":
					relaxed = true
				case strings.HasPrefix(o, "work="):
					f.Lib = strings.ToLower(o[len("work="):])
				case strings.HasPrefix(o, "define="):
					k, v, _ := strings.Cut(o[len("define="):], "=")
					r.Defines = append(r.Defines,
						config.Define{Name: k, Value: v})
				default:
					if s, ok := nvcStdOf[o]; ok {
						r.Std = s
					}
				}
			}
		}
		f.Path = path.Join(r.Dir, f.Arg)
		if skip || !fileExists(root, f.Path) {
			continue
		}
		f.Flags = nvcFlags(relaxed, psl)
		r.GHDLStd = toGHDLStd(r.Std)
		r.Files = []semFile{f}
		runs = append(runs, r)
	}
	return runs, nil
}

var (
	nvcStart  = regexp.MustCompile(`^\s*START_TEST\((\w+)\)`)
	nvcEnd    = regexp.MustCompile(`^\s*END_TEST\b`)
	nvcSetStd = regexp.MustCompile(`set_standard\((\w+)\)`)
	nvcRelax  = regexp.MustCompile(`opt_set_int\(OPT_RELAXED,\s*1\)`)
	nvcPSL    = regexp.MustCompile(`opt_set_int\(OPT_PSL_COMMENTS,\s*1\)`)
	nvcInput  = regexp.MustCompile(`input_from_file\(TESTDIR\s*"/([^"]+)"`)
	nvcLoop   = regexp.MustCompile(`tcase_add_loop_test\(\s*\w+\s*,\s*` +
		`(\w+)\s*,\s*(STD_\w+)\s*,\s*(STD_\w+)\s*\+\s*1\s*\)`)
)

var nvcStdOrder = []string{"STD_87", "STD_93", "STD_00", "STD_02",
	"STD_08", "STD_19"}

// nvcUnitRuns reads one test/test_*.c: each START_TEST block analyzes
// its input_from_file files in order; the standard and relaxed mode in
// force at each input apply to it (set_standard(_i): every standard of
// the block's tcase_add_loop_test range). A block yields one run per
// standard, holding the inputs analyzed under it.
func nvcUnitRuns(root, dir, name string) ([]*semRun, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(
		path.Join(dir, name))))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(src), "\n")
	loops := map[string][]token.Standard{}
	for _, l := range lines {
		m := nvcLoop.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		from := slices.Index(nvcStdOrder, m[2])
		to := slices.Index(nvcStdOrder, m[3])
		for i := max(from, 1); from >= 0 && i <= to; i++ {
			loops[m[1]] = append(loops[m[1]], nvcStdOf[nvcStdOrder[i]])
		}
	}
	type input struct {
		file         string
		stds         []token.Standard
		relaxed, psl bool
	}
	var (
		runs    []*semRun
		test    string
		in      bool
		inputs  []input
		curStd  []token.Standard
		relaxed bool
		psl     bool
	)
	flush := func() {
		var stds []token.Standard
		for _, i := range inputs {
			for _, s := range i.stds {
				if !slices.Contains(stds, s) {
					stds = append(stds, s)
				}
			}
		}
		slices.Sort(stds)
		for _, s := range stds {
			r := &semRun{ID: fmt.Sprintf("unit:%s:%s:%s",
				strings.TrimSuffix(name, ".c"), test, s),
				Golden: "nvc-unit.txt", Dir: dir, Std: s,
				GHDLStd: toGHDLStd(s)}
			for _, i := range inputs {
				if !slices.Contains(i.stds, s) || !isVHDLName(i.file) {
					continue
				}
				f := semFile{Arg: path.Clean(i.file), Lib: work,
					Flags: nvcFlags(i.relaxed, i.psl)}
				f.Path = path.Join(dir, f.Arg)
				if fileExists(root, f.Path) {
					r.Files = append(r.Files, f)
				}
			}
			if len(r.Files) > 0 {
				runs = append(runs, r)
			}
		}
	}
	for _, l := range lines {
		if m := nvcStart.FindStringSubmatch(l); m != nil {
			test, in, inputs = m[1], true, nil
			curStd = []token.Standard{token.Std1993}
			relaxed, psl = false, false
			continue
		}
		if !in {
			continue
		}
		if nvcEnd.MatchString(l) {
			flush()
			in = false
			continue
		}
		// The standard and options set before an input apply to it;
		// set_standard after the last input still applies (the block
		// parses after setting up).
		if m := nvcSetStd.FindStringSubmatch(l); m != nil {
			if m[1] == "_i" {
				curStd = loops[test]
			} else if s, ok := nvcStdOf[m[1]]; ok {
				curStd = []token.Standard{s}
			}
			if n := len(inputs); n > 0 {
				inputs[n-1].stds = curStd
			}
		}
		if nvcRelax.MatchString(l) {
			relaxed = true
			if n := len(inputs); n > 0 {
				inputs[n-1].relaxed = true
			}
		}
		if nvcPSL.MatchString(l) {
			psl = true
			if n := len(inputs); n > 0 {
				inputs[n-1].psl = true
			}
		}
		if m := nvcInput.FindStringSubmatch(l); m != nil {
			inputs = append(inputs, input{file: m[1], stds: curStd,
				relaxed: relaxed, psl: psl})
		}
	}
	return runs, nil
}

// planDesign is the plan of one corpus design: the main run (the
// stand-in models, then files) and one run per variant, as the format
// section analyzes them.
func planDesign(x *ctx, d design) (*semPlan, error) {
	cfg := config.Defaults()
	if d.Config != "" {
		src, err := readRepo(x.root, path.Join(d.dir(), d.Config))
		if err != nil {
			return nil, err
		}
		c, ds := config.Parse(config.FileName, src)
		for _, dg := range ds {
			if dg.Severity == diag.Error {
				return nil, fmt.Errorf("%s: %s:%d:%d: %s", d.Name,
					d.Config, dg.Line, dg.Col, dg.Message)
			}
		}
		cfg = c
	}
	p := &semPlan{}
	add := func(name, std string, over map[string]string,
		files []fileEntry) error {
		s, ok := token.ParseStandard(std)
		if !ok {
			return fmt.Errorf("%s: run %s: standard %q", d.Name, name, std)
		}
		defs, err := runDefines(cfg, over)
		if err != nil {
			return fmt.Errorf("%s: run %s: %w", d.Name, name, err)
		}
		r := &semRun{ID: "corpus:" + d.Name + ":" + name,
			Golden: semGoldenOf(d.Name), Dir: d.dir(), Std: s,
			GHDLStd: toGHDLStd(s), Defines: defs}
		for _, e := range files {
			r.Files = append(r.Files, semFile{Arg: e.Path,
				Path: path.Join(d.dir(), e.Path), Lib: work,
				Flags: nvcFlags(false, cfg.PSL.Comments)})
		}
		p.Runs = append(p.Runs, r)
		return nil
	}
	var main []fileEntry
	for _, m := range d.modelPaths() {
		main = append(main, fileEntry{Path: m, Standard: d.Standard})
	}
	if err := add("main", d.Standard, nil,
		append(main, d.Files...)); err != nil {
		return nil, err
	}
	for _, v := range d.Variants {
		if err := add(v.Name, v.Standard, v.Defines, v.Files); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func semGoldenOf(design string) string { return "corpus-" + design + ".txt" }

// sortRunsStable keeps the plan order but makes it independent of map
// iteration: runs are sorted by golden file, keeping their relative
// order otherwise.
func sortRunsStable(rs []*semRun) {
	slices.SortStableFunc(rs, func(a, b *semRun) int {
		return cmp.Compare(a.Golden, b.Golden)
	})
}
