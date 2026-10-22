package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/simrun"
	"github.com/connerohnesorge/gohard/internal/token"
)

// The trace section (#36, #8): GHDL and NVC run each design with an
// oracle and dump VCD; the dumps are converted to the settled-value
// form (corpus.Oracle) and recorded as golden/oracle.<tool>.txt. The
// check compares GoHard's settled projection of its own trace with
// every recording once the sequential interpreter is registered, and
// always checks that a recording is not stale.

// fieldOracle is the hash field of the trace and tree recordings.
const fieldOracle = "oracle_sha256"

// oracleTools returns the tools of a design's oracle field.
func oracleTools(d design) []string {
	switch d.Oracle {
	case "both":
		return []string{"ghdl", "nvc"}
	case "ghdl", "nvc":
		return []string{d.Oracle}
	}
	return nil
}

func oracleName(tool string) string { return "oracle." + tool + ".txt" }

var traceSection = &section{
	name: "trace",
	targets: func(x *ctx) ([]target, error) {
		var ts []target
		for _, d := range x.man.Designs {
			var names []string
			for _, t := range oracleTools(d) {
				names = append(names, oracleName(t))
			}
			if len(names) == 0 {
				continue
			}
			ts = append(ts, target{
				Ref: entryRef{List: "designs", Name: d.Name,
					Field: fieldOracle, After: "trace_sha256"},
				Dir:   path.Join(d.dir(), "golden"),
				Files: names,
			})
		}
		return ts, nil
	},
	record: recordTrace,
	check:  checkTrace,
}

// runSources is the main run of a design: the stand-in models, then
// the files, in analysis order, with their bytes.
func runSources(x *ctx, d design) ([]string, map[string][]byte, error) {
	order := d.modelPaths()
	for _, f := range d.Files {
		order = append(order, f.Path)
	}
	files := map[string][]byte{}
	for _, p := range order {
		rel := path.Join(d.dir(), p)
		b, err := readRepo(x.root, rel)
		if err != nil {
			return nil, nil, corpus.Missing(rel, err)
		}
		files[p] = b
	}
	return order, files, nil
}

// sourcesHash identifies the analysed sources and config of a run.
func sourcesHash(x *ctx, d design) (string, error) {
	order, files, err := runSources(x, d)
	if err != nil {
		return "", err
	}
	var gs []goldenFile
	for i, p := range order {
		gs = append(gs, goldenFile{Name: fmt.Sprintf("%03d %s", i, p),
			Data: files[p]})
	}
	if d.Config != "" {
		b, err := readRepo(x.root, path.Join(d.dir(), d.Config))
		if err != nil {
			return "", err
		}
		gs = append(gs, goldenFile{Name: "config", Data: b})
	}
	return goldenHash(gs)[:16], nil
}

// stopLevel is the design's simulation.stop_level.
func stopLevel(x *ctx, d design) (string, error) {
	cfg := config.Defaults()
	if d.Config != "" {
		b, err := readRepo(x.root, path.Join(d.dir(), d.Config))
		if err != nil {
			return "", err
		}
		cfg, _ = config.Parse(config.FileName, b)
	}
	return cfg.Simulation.StopLevel, nil
}

// toolStop is the bounded --stop-time of a tool whose recorded result
// is "bounded" ("1 ms" -> "1ms"), else "".
func toolStop(d design, tool string) string {
	r := d.GHDL.Result
	if tool == "nvc" {
		r = d.NVC.Result
	}
	if r != "bounded" {
		return ""
	}
	return strings.ReplaceAll(d.Expected.StopTime, " ", "")
}

// simulate runs one tool on the main run of d and returns its VCD.
func simulate(x *ctx, tl *toolset, d design, tool string) ([]byte,
	string, error) {
	tt, err := tl.get(tool)
	if err != nil {
		return nil, "", err
	}
	order, files, err := toolSources(x, d)
	if err != nil {
		return nil, "", err
	}
	std, ok := token.ParseStandard(d.Standard)
	if !ok {
		return nil, "", fmt.Errorf("bad standard %q", d.Standard)
	}
	lvl, err := stopLevel(x, d)
	if err != nil {
		return nil, "", err
	}
	w, err := newWorkDir(files, order)
	if err != nil {
		return nil, "", err
	}
	defer w.close()
	stop := toolStop(d, tool)
	var steps [][]string
	switch tool {
	case "ghdl":
		a := append([]string{"-a", "--std=" + ghdlStd(std)}, order...)
		r := []string{"--elab-run", "--std=" + ghdlStd(std), d.Testbench,
			"--vcd=dump.vcd", "--assert-level=" + lvl}
		if stop != "" {
			r = append(r, "--stop-time="+stop)
		}
		steps = [][]string{a, r}
	default:
		a := []string{"--std=" + std.String(), "-a"}
		for _, k := range sortedKeys(d.Defines) {
			a = append(a, "-D", k+"="+d.Defines[k])
		}
		a = append(a, order...)
		a = append(a, "-e", d.Testbench, "-r", "--wave=dump.vcd",
			"--format=vcd", "--dump-arrays", "--exit-severity="+lvl)
		if stop != "" {
			a = append(a, "--stop-time="+stop)
		}
		steps = [][]string{a}
	}
	var log strings.Builder
	for _, s := range steps {
		out, _, err := tt.run(w.dir, s...)
		if err != nil {
			return nil, "", err
		}
		log.WriteString(out)
	}
	b, err := os.ReadFile(filepath.Join(w.dir, "dump.vcd"))
	if err != nil {
		return nil, "", fmt.Errorf("%s wrote no dump: %w\n%s", tool, err,
			log.String())
	}
	return b, tt.version, nil
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}

func recordTrace(x *ctx, tl *toolset, t target) ([]goldenFile, error) {
	d, _ := x.design(t.Ref.Name)
	src, err := sourcesHash(x, d)
	if err != nil {
		return nil, err
	}
	var out []goldenFile
	for _, tool := range oracleTools(d) {
		vcd, ver, err := simulate(x, tl, d, tool)
		if err != nil {
			return nil, err
		}
		o, err := corpus.ParseVCD(vcd, d.Testbench)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tool, err)
		}
		o.Tool, o.Version, o.Sources = tool, ver, src
		out = append(out, goldenFile{Name: oracleName(tool),
			Data: o.Encode()})
	}
	return out, nil
}

func checkTrace(x *ctx, t target, golden []goldenFile) []string {
	d, _ := x.design(t.Ref.Name)
	src, err := sourcesHash(x, d)
	if err != nil {
		return []string{err.Error()}
	}
	var probs []string
	var ors []*corpus.Oracle
	for _, g := range golden {
		o, err := corpus.DecodeOracle(g.Data)
		if err != nil {
			probs = append(probs, g.Name+": "+err.Error()+": "+rerun)
			continue
		}
		if o.Sources != src {
			probs = append(probs, g.Name+": the sources changed "+
				"since the recording: "+rerun)
			continue
		}
		ors = append(ors, o)
	}
	if len(probs) > 0 {
		return probs
	}
	ors, probs = applyDisagreements(d, ors)
	if len(probs) > 0 {
		return probs
	}
	return append(probs, compareGoHard(x, d, ors)...)
}

// applyDisagreements drops from each recording the signals of the
// disagreements that follow another tool, then requires the remaining
// recordings to agree on every signal both dump.
func applyDisagreements(d design, ors []*corpus.Oracle) (
	[]*corpus.Oracle, []string) {
	var probs []string
	out := make([]*corpus.Oracle, 0, len(ors))
	for _, o := range ors {
		var drop []string
		for _, g := range d.Disagreements {
			if g.Follows != o.Tool {
				drop = append(drop, g.Signals...)
			}
		}
		f, err := o.Without(drop)
		if err != nil {
			probs = append(probs, err.Error())
			continue
		}
		out = append(out, f)
	}
	if len(out) == 2 {
		if ds := corpus.DiffSignals(out[0], out[1]); len(ds) > 0 {
			probs = append(probs, "GHDL and NVC disagree on "+
				strings.Join(ds, ", ")+": record a disagreement "+
				"with signals and follows in corpus.yaml")
		}
	}
	return out, probs
}

// compareGoHard runs GoHard on the main run of d at seed 0 and
// compares its settled projection with each recording. Without a
// registered interpreter there is no GoHard trace to compare; the
// corpus harness (internal/corpus) reports that.
func compareGoHard(x *ctx, d design, ors []*corpus.Oracle) []string {
	// A design of a later milestone than corpus.Release (none since
	// the v1.1 flip: prot_cond and gc_heap are compared) waits for
	// the milestone that simulates it.
	if simrun.DefaultCode == nil || len(ors) == 0 ||
		!corpus.Reached(d.Milestone) {
		return nil
	}
	m, err := corpus.ParseManifest(x.src)
	if err != nil {
		return []string{err.Error()}
	}
	cd, _ := m.Design(d.Name)
	r := cd.Runs()[0]
	src, err := r.Sources(func(rel string) ([]byte, error) {
		return readRepo(x.root, rel)
	})
	if err != nil {
		return []string{err.Error()}
	}
	res, err := corpus.Execute(r, src,
		corpus.Options{Code: simrun.DefaultCode})
	if err != nil {
		return []string{err.Error()}
	}
	if res.Trace == nil {
		return []string{"GoHard did not simulate the design"}
	}
	// A run that hit a budget was cut by the tool's own stop time,
	// which dumps the final step; one the design stopped did not.
	cmp := corpus.CompareOracleStopped
	for _, m := range res.Messages {
		if m.Code == diag.SimBudgetExceeded {
			cmp = corpus.CompareOracle
		}
	}
	var probs []string
	for _, o := range ors {
		for _, p := range cmp(res.Trace, o) {
			probs = append(probs, o.Tool+": "+p)
		}
	}
	return probs
}

// toolSources is runSources without the opaque files (an IEEE 1735
// envelope): GHDL and NVC simulate the stand-in model in their place.
func toolSources(x *ctx, d design) ([]string, map[string][]byte,
	error) {
	order, files, err := runSources(x, d)
	if err != nil {
		return nil, nil, err
	}
	keep := order[:0]
	for _, p := range order {
		if !bytes.Contains(bytes.ToLower(files[p]),
			[]byte("`protect begin_protected")) {
			keep = append(keep, p)
		}
	}
	return keep, files, nil
}
