package cli_test

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/simrun"
)

// readFiles reads the files below dir (skipping golden/ and
// .gohard/) as /work/<path>, leaving out those drop rejects.
func readFiles(t *testing.T, dir string, drop func(string) bool,
) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "golden" || d.Name() == ".gohard" {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if drop != nil && drop(rel) {
			return nil
		}
		b, err := os.ReadFile(p)
		files["/work/"+rel] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestCodeActionResults: the project every applied code action
// fixture leaves (testdata/cli/lsp-codeaction-*/want) passes `gohard
// check` and `gohard fmt --check` with no change (#57).
func TestCodeActionResults(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join(fixtureRoot,
		"lsp-codeaction-*"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, dir := range dirs {
		exit, _ := os.ReadFile(filepath.Join(dir, "want.exit"))
		if strings.TrimSpace(string(exit)) != "0" {
			continue
		}
		n++
		t.Run(filepath.Base(dir), func(t *testing.T) {
			sim := simenv.New(simenv.Options{Dir: "/work",
				Files: readFiles(t, filepath.Join(dir, "want"), nil)})
			for _, args := range [][]string{{"check"}, {"fmt", "--check"}} {
				code, out, errb := runIn(sim, nil, args...)
				if code != 0 || !strings.Contains(string(out),
					"summary: 0 errors, 0 warnings, 0 hints") {
					t.Fatalf("gohard %s: exit %d\n%s%s",
						strings.Join(args, " "), code, out, errb)
				}
			}
		})
	}
	if n < 6 {
		t.Fatalf("%d applied code action fixtures", n)
	}
}

var entityLine = regexp.MustCompile(`(?m)^entity (\w+) is`)

// TestGeneratedTestbenchCorpus: for every entity of the corpus designs
// that analyze cleanly under the default standard, Create testbench
// applied with `gohard lsp codeaction` gives a testbench that `gohard
// check --sim` runs without sim/budget-exceeded: a testbench with a
// clock to std.env.finish (stopped), unless an assertion of the design
// on the inputs it leaves undriven halts it first (an assertion error
// of the design that does not halt the run may make the exit 1).
// budget_hang, whose own testbench runs into the time budget, must be
// one that stops, with exit 0 (#57).
func TestGeneratedTestbenchCorpus(t *testing.T) {
	if simrun.DefaultCode == nil {
		t.Skip("needs the sequential interpreter")
	}
	const root = "../../testdata/corpus"
	designs, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var stopped []string
	for _, d := range designs {
		if !d.IsDir() {
			continue
		}
		files := readFiles(t, filepath.Join(root, d.Name()),
			func(rel string) bool {
				return strings.HasSuffix(rel, "_tb.vhd")
			})
		if code, _, _ := runIn(simenv.New(simenv.Options{Dir: "/work",
			Files: files}), nil, "check"); code != 0 {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(files)) {
			for _, m := range entityLine.FindAllSubmatchIndex(files[name],
				-1) {
				ent := string(files[name][m[2]:m[3]])
				line := 1 + strings.Count(string(files[name][:m[0]]), "\n")
				at := strings.TrimPrefix(name, "/work/") + ":" +
					strconv.Itoa(line) + ":8"
				if testbenchRun(t, files, at, ent) {
					stopped = append(stopped, d.Name()+"/"+ent)
				}
			}
		}
	}
	t.Logf("stopped: %v", stopped)
	if !slices.Contains(stopped, "budget_hang/budget_hang") {
		t.Fatalf("budget_hang's testbench did not stop: %v", stopped)
	}
}

// testbenchRun applies Create testbench to the entity ent at at and,
// when the testbench has a clock, runs it; it reports whether the run
// stopped at std.env.finish.
func testbenchRun(t *testing.T, files map[string][]byte, at, ent string,
) bool {
	t.Helper()
	sim := simenv.New(simenv.Options{Dir: "/work", Files: files})
	title := "Create testbench " + ent + "_tb.vhd"
	_, out, _ := runIn(sim, nil, "lsp", "codeaction", "--at", at)
	var as []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &as); err != nil {
		t.Fatalf("%s: %v\n%s", at, err, out)
	}
	offered := false
	for _, a := range as {
		offered = offered || a.Title == title
	}
	if !offered {
		return false
	}
	code, out, errb := runIn(sim, nil, "lsp", "codeaction", "--at", at,
		title)
	if code != 0 {
		t.Fatalf("%s: apply: exit %d\n%s%s", at, code, out, errb)
	}
	file, _, _ := strings.Cut(at, ":")
	tb, _ := sim.Env().FS.ReadFile(path.Join("/work", path.Dir(file),
		ent+"_tb.vhd"))
	if !strings.Contains(string(tb), "_gen : process") {
		return false // no clock
	}
	code, out, errb = runIn(sim, nil, "check", "--sim", "--tb", ent+"_tb")
	report := string(out) + string(errb)
	if strings.Contains(report, "budget-exceeded") {
		t.Fatalf("%s: over a budget:\n%s", at, report)
	}
	if strings.Contains(report, "(seed 0): stopped") {
		// Exit 1 only for an assertion of the design itself.
		if code != 0 && (ent == "budget_hang" ||
			strings.Contains(report, ent+"_tb.vhd:")) {
			t.Fatalf("%s: stopped with exit %d:\n%s", at, code, report)
		}
		return true
	}
	if !strings.Contains(report, "(seed 0): halted") ||
		strings.Contains(report, ent+"_tb.vhd:") {
		t.Fatalf("%s: neither stopped nor halted by the design:\n%s",
			at, report)
	}
	return false
}
