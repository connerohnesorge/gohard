// Command bench is the #19 latency benchmark: it drives the real
// language server (internal/server with the internal/ide analyzer, as
// `gohard lsp` builds it) through a scripted LSP client on the small,
// medium and large projects, and reports per-check keystroke-to-
// diagnostics p95 (cold and warm), the derived as-you-type / on-save
// tiers, and the #25 latency gates.
//
//	go run ./tools/bench [-projects small,medium,large] [-warm 60]
//	    [-cold 20] [-out bench19.md] [-json bench19.json]
//
// Every edit and query position is a pure function of the project's
// text and a seed (Keystrokes, Names), so every run measures the same
// work. Each cold sample, each simulation sample and each warm session
// is a fresh process (the driver runs itself with -child). The
// analyzer's phases are timed by the sema/ide Probe, which never
// changes a result. Numbers are recorded, never gated: the exit code
// is 0 unless the benchmark itself could not run.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func main() {
	child := flag.String("child", "", "internal: run one benchmark "+
		"process (first, cold, warm, sim)")
	dir := flag.String("dir", "", "internal: the built project")
	proj := flag.String("project", "", "internal: the project name")
	n := flag.Int("n", 0, "internal: samples per measure")
	seed := flag.Uint64("seed", 19, "edit and query seed")
	repo := flag.String("repo", ".", "repository root")
	projects := flag.String("projects", "small,medium,large,sim",
		"projects to measure")
	warm := flag.Int("warm", 60, "warm samples per measure")
	cold := flag.Int("cold", 20, "cold processes per measure")
	sims := flag.Int("sim", 20, "simulation-progress processes")
	maxLoad := flag.Float64("maxload", 2,
		"wait while the 1-minute load average is above this")
	outMD := flag.String("out", "", "write the Markdown report here "+
		"(default stdout)")
	outJSON := flag.String("json", "", "write the raw samples here")
	cpuprofile := flag.String("cpuprofile", "", "write a CPU profile "+
		"of the large project's warm session here")
	flag.Parse()
	if *child != "" {
		p, ok := projectByName(*proj)
		if !ok {
			fatal(fmt.Errorf("unknown project %q", *proj))
		}
		out, err := runChild(childArgs{mode: *child, dir: *dir, p: p,
			n: *n, seed: *seed, cpuprofile: *cpuprofile,
			keystrokeTO: 30 * time.Second})
		if err != nil {
			fatal(err)
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)
		os.Exit(0) // stop the in-process server and any simulation
	}
	d := driver{repo: *repo, seed: *seed, warm: *warm, cold: *cold,
		sims: *sims, maxLoad: *maxLoad, cpuprofile: *cpuprofile}
	var rs []result
	for _, name := range strings.Split(*projects, ",") {
		p, ok := projectByName(name)
		if !ok {
			fatal(fmt.Errorf("unknown project %q", name))
		}
		r, err := d.measure(p)
		if err != nil {
			fatal(err)
		}
		rs = append(rs, r)
	}
	rep := report(d.header(os.Args), rs)
	if *outJSON != "" {
		b, _ := json.MarshalIndent(rs, "", " ")
		if err := os.WriteFile(*outJSON, b, 0o644); err != nil {
			fatal(err)
		}
	}
	if *outMD == "" {
		fmt.Print(rep)
		return
	}
	if err := os.WriteFile(*outMD, []byte(rep), 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bench:", err)
	os.Exit(1)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

type driver struct {
	repo             string
	seed             uint64
	warm, cold, sims int
	maxLoad          float64
	cpuprofile       string
	loads            []float64
}

// result is every sample of one project.
type result struct {
	Project string
	SimOnly bool
	Files   int
	Cold    []Sample
	// First are the cold-start-to-first-diagnostics times.
	First []float64
	Warm  childOut
	Sim   childOut
	Notes []string
}

// load returns the 1-minute load average (Linux; -1 elsewhere).
func load() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return -1
	}
	f, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	if err != nil {
		return -1
	}
	return f
}

// quiet waits (up to an hour) for the load average to drop to
// maxLoad and records it.
func (d *driver) quiet() {
	for i := 0; i < 360 && load() > d.maxLoad; i++ {
		fmt.Fprintf(os.Stderr, "bench: load %.2f > %.2f, waiting\n",
			load(), d.maxLoad)
		time.Sleep(10 * time.Second)
	}
	d.loads = append(d.loads, load())
}

func (d *driver) spawn(mode, dir string, p Project, n int, seed uint64,
	extra ...string,
) (childOut, time.Time, error) {
	self, err := os.Executable()
	if err != nil {
		return childOut{}, time.Time{}, err
	}
	args := append([]string{"-child", mode, "-dir", dir, "-project",
		p.Name, "-n", strconv.Itoa(n), "-seed",
		strconv.FormatUint(seed, 10)}, extra...)
	cmd := exec.Command(self, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	t0 := time.Now()
	if err := cmd.Run(); err != nil {
		return childOut{}, t0, fmt.Errorf("%s %s: %v: %s", p.Name, mode,
			err, stderr.Bytes())
	}
	var out childOut
	err = json.Unmarshal(stdout.Bytes(), &out)
	return out, t0, err
}

func (d *driver) measure(p Project) (result, error) {
	r := result{Project: p.Name, SimOnly: p.SimOnly}
	tmp, err := os.MkdirTemp("", "gohard-bench19-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, p.Name)
	if r.Files, err = p.Build(d.repo, dir); err != nil {
		return r, err
	}
	fmt.Fprintf(os.Stderr, "bench: %s: %d files\n", p.Name, r.Files)
	d.quiet()
	for i := range d.cold * btoi(!p.SimOnly) {
		out, t0, err := d.spawn("first", dir, p, 1, d.seed)
		if err != nil {
			return r, err
		}
		r.First = append(r.First,
			ms(time.Unix(0, out.FirstUnixNano).Sub(t0)))
		r.Notes = append(r.Notes, out.Notes...)
		// Each cold keystroke edits a different line (seed + i).
		out, _, err = d.spawn("cold", dir, p, 1, d.seed+uint64(i))
		if err != nil {
			return r, err
		}
		r.Cold = append(r.Cold, out.Samples...)
	}
	d.quiet()
	var extra []string
	if d.cpuprofile != "" && p.Name == "large" {
		extra = []string{"-cpuprofile", d.cpuprofile}
	}
	if !p.SimOnly {
		r.Warm, _, err = d.spawn("warm", dir, p, d.warm, d.seed,
			extra...)
		if err != nil {
			return r, err
		}
	}
	r.Notes = append(r.Notes, r.Warm.Notes...)
	if p.Testbench != "" {
		d.quiet()
		for range d.sims {
			out, _, err := d.spawn("sim", dir, p, 1, d.seed)
			if err != nil {
				return r, err
			}
			r.Sim.SimBegin = append(r.Sim.SimBegin, out.SimBegin...)
			r.Sim.SimElab = append(r.Sim.SimElab, out.SimElab...)
			r.Sim.SimKernel = append(r.Sim.SimKernel, out.SimKernel...)
			r.Notes = append(r.Notes, out.Notes...)
		}
	}
	return r, nil
}

// header describes the run: commit, machine, Go version, load and
// the command.
func (d *driver) header(argv []string) string {
	var b strings.Builder
	commit := "unknown"
	if out, err := exec.Command("git", "-C", d.repo, "rev-parse",
		"--short", "HEAD").Output(); err == nil {
		commit = strings.TrimSpace(string(out))
		if st, err := exec.Command("git", "-C", d.repo, "status",
			"--porcelain").Output(); err == nil && len(st) > 0 {
			commit += " (with uncommitted changes)"
		}
	}
	fmt.Fprintf(&b, "* Commit: %s\n", commit)
	fmt.Fprintf(&b, "* Machine: %s, %d CPUs, %s/%s, %s RAM\n", cpuModel(),
		runtime.NumCPU(), runtime.GOOS, runtime.GOARCH, memTotal())
	fmt.Fprintf(&b, "* Go: %s\n", runtime.Version())
	ls := make([]string, len(d.loads))
	for i, l := range d.loads {
		ls[i] = strconv.FormatFloat(l, 'f', 2, 64)
	}
	fmt.Fprintf(&b, "* Load average (1 min) before each phase: %s\n",
		strings.Join(ls, ", "))
	fmt.Fprintf(&b, "* Samples: %d warm keystrokes, queries and saves; "+
		"%d cold processes; %d simulation processes; seed %d\n",
		d.warm, d.cold, d.sims, d.seed)
	fmt.Fprintf(&b, "* Command: `%s`\n", strings.Join(argv[1:], " "))
	return b.String()
}

func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return runtime.GOARCH
	}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok &&
			strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return runtime.GOARCH
}

func memTotal() string {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return "?"
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "MemTotal:"); ok {
			kb, _ := strconv.ParseFloat(strings.TrimSuffix(
				strings.TrimSpace(v), " kB"), 64)
			return fmt.Sprintf("%.0f GB", kb/1024/1024)
		}
	}
	return "?"
}
