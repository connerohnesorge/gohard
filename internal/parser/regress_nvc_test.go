package parser

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/condanal"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// This file reads NVC's own expectations (corpus.yaml, "Expected-
// failure extraction", NVC): test/regress/testlist.txt names the
// regression tests, every one of which must analyse; the C unit tests
// test/test_*.c name each input file and the diagnostics NVC expects
// on it.

// nvcRef is one file NVC names with an expectation. file is relative
// to the test folder.
type nvcRef struct {
	file    string
	fail    bool // NVC expects diagnostics: may fail
	std     token.Standard
	psl     bool
	relaxed bool
	defines []condanal.Define
	why     string
}

// nvcStd maps run_regr.c's standard options and the STD_xx constants.
var nvcStd = map[string]token.Standard{
	"2000":   token.Std2002,
	"2002":   token.Std2002,
	"2008":   token.Std2008,
	"2019":   token.Std2019,
	"STD_87": token.Std1993,
	"STD_93": token.Std1993,
	"STD_00": token.Std2002,
	"STD_02": token.Std2002,
	"STD_08": token.Std2008,
	"STD_19": token.Std2019,
}

var nvcStdOrder = []string{"STD_87", "STD_93", "STD_00", "STD_02",
	"STD_08", "STD_19"}

// readTestlist reads test/regress/testlist.txt: `<name> <opt>,...`.
// No entry is an analysis failure (the fail option is about the run):
// every listed <name>.vhd must analyse under the standard of its
// options (default 1993, see push_std in run_regr.c). verilog tests
// have no .vhd, and shell tests run their own script instead.
func readTestlist(root string) ([]nvcRef, error) {
	const list = "regress/testlist.txt"
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(list)))
	if err != nil {
		return nil, err
	}
	var refs []nvcRef
	for i, line := range strings.Split(string(src), "\n") {
		w := strings.Fields(line)
		if len(w) == 0 || strings.HasPrefix(w[0], "#") {
			continue
		}
		r := nvcRef{file: path.Join("regress", w[0]+".vhd"),
			std: token.Std1993,
			why: fmt.Sprintf("%s:%d", list, i+1)}
		skip := false
		if len(w) > 1 {
			for o := range strings.SplitSeq(w[1], ",") {
				switch {
				case o == "verilog", o == "shell":
					// verilog: no .vhd. shell: regress/<name>.sh
					// analyses with its own options, not run_regr.c.
					skip = true
				case o == "psl":
					r.psl = true
				case o == "relaxed":
					r.relaxed = true
				case strings.HasPrefix(o, "define="):
					k, v, _ := strings.Cut(o[len("define="):], "=")
					r.defines = append(r.defines,
						condanal.Define{Name: k, Value: v})
				default:
					if s, ok := nvcStd[o]; ok {
						r.std = s
					}
				}
			}
		}
		if !skip {
			refs = append(refs, r)
		}
	}
	return refs, nil
}

var (
	nvcStart  = regexp.MustCompile(`^\s*START_TEST\((\w+)\)`)
	nvcEnd    = regexp.MustCompile(`^\s*END_TEST\b`)
	nvcSetStd = regexp.MustCompile(`set_standard\((\w+)\)`)
	nvcRelax  = regexp.MustCompile(`opt_set_int\(OPT_RELAXED,\s*1\)`)
	nvcInput  = regexp.MustCompile(`input_from_file\(TESTDIR\s*"/([^"]+)"`)
	nvcExpect = regexp.MustCompile(`error_t\s+expect\[\]\s*=\s*\{`)
	nvcEntry  = regexp.MustCompile(`\{\s*(-?\d+)\s*,`)
	nvcLoop   = regexp.MustCompile(`tcase_add_loop_test\(\s*\w+\s*,\s*` +
		`(\w+)\s*,\s*(STD_\w+)\s*,\s*(STD_\w+)\s*\+\s*1\s*\)`)
)

// readUnitTests reads one test/test_*.c. Within each START_TEST ...
// END_TEST block, input_from_file names an input; the expect[] array,
// set_standard and opt_set_int(OPT_RELAXED, 1) in force when the input
// is parsed (the last ones before the next input_from_file, or the end
// of the block) apply to it. An expect[] with any entry other than
// { -1, NULL } means NVC reports diagnostics on the file: it may fail.
// Without one the file is clean and must parse. Standards default to
// 1993; set_standard(_i) takes every standard of the test's
// tcase_add_loop_test range. Verilog inputs are skipped.
func readUnitTests(root, name string) ([]nvcRef, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(src), "\n")
	loops := map[string][]token.Standard{}
	for _, l := range lines {
		if m := nvcLoop.FindStringSubmatch(l); m != nil {
			from, to := indexOf(nvcStdOrder, m[2]),
				indexOf(nvcStdOrder, m[3])
			for i := from; i >= 0 && i <= to; i++ {
				loops[m[1]] = append(loops[m[1]], nvcStd[nvcStdOrder[i]])
			}
		}
	}
	type input struct {
		file string
		line int
	}
	var refs []nvcRef
	var (
		test    string
		inBlock bool
		inputs  []input
		// state in force at each input, by input index
		stds    [][]token.Standard
		fails   []bool
		relaxed []bool
		curStd  []token.Standard
		curFail bool
		curRel  bool
	)
	// settle records the state in force for the last input.
	settle := func() {
		if n := len(inputs); n > 0 {
			stds[n-1], fails[n-1], relaxed[n-1] = curStd, curFail, curRel
		}
	}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if m := nvcStart.FindStringSubmatch(l); m != nil {
			test, inBlock = m[1], true
			inputs, stds, fails, relaxed = nil, nil, nil, nil
			curStd = []token.Standard{token.Std1993}
			curFail, curRel = false, false
			continue
		}
		if !inBlock {
			continue
		}
		if nvcEnd.MatchString(l) {
			settle()
			for k, in := range inputs {
				if !isVHDLName(in.file) {
					continue
				}
				for _, s := range stds[k] {
					refs = append(refs, nvcRef{file: in.file,
						fail: fails[k], std: s, relaxed: relaxed[k],
						why: fmt.Sprintf("%s:%d %s", name, in.line,
							test)})
				}
			}
			inBlock = false
			continue
		}
		if m := nvcInput.FindStringSubmatch(l); m != nil {
			settle()
			inputs = append(inputs, input{file: m[1], line: i + 1})
			stds = append(stds, nil)
			fails = append(fails, false)
			relaxed = append(relaxed, false)
			// The expect[], standard and relaxed mode in force carry
			// over until the block replaces them.
		}
		if m := nvcSetStd.FindStringSubmatch(l); m != nil {
			if m[1] == "_i" {
				curStd = loops[test]
			} else if s, ok := nvcStd[m[1]]; ok {
				curStd = []token.Standard{s}
			}
		}
		if nvcRelax.MatchString(l) {
			curRel = true
		}
		if nvcExpect.MatchString(l) {
			var arr strings.Builder
			for ; i < len(lines); i++ {
				arr.WriteString(lines[i])
				arr.WriteByte('\n')
				if strings.Contains(lines[i], "};") {
					break
				}
			}
			curFail = false
			for _, e := range nvcEntry.FindAllStringSubmatch(
				arr.String(), -1) {
				if e[1] != "-1" {
					curFail = true
				}
			}
		}
	}
	return refs, nil
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}
