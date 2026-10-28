package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

func TestValidSeed(t *testing.T) {
	for _, s := range []string{"", "0", "7", "random",
		"18446744073709551615"} {
		if !validSeed(s) {
			t.Errorf("validSeed(%q) = false", s)
		}
	}
	for _, s := range []string{"-1", "Random", "1.5", "0x10",
		"18446744073709551616", " 1"} {
		if validSeed(s) {
			t.Errorf("validSeed(%q) = true", s)
		}
	}
}

// reverseJobs runs jobs last to first.
type reverseJobs struct{}

func (reverseJobs) Run(_, n int, job func(int)) {
	for i := n - 1; i >= 0; i-- {
		job(i)
	}
}

// project writes a random project for seed into dir and returns the
// paths it created plus a few that do not exist.
func project(t *testing.T, dir string, seed uint64) []string {
	r := &env.SplitMix64{State: seed}
	var paths []string
	for i := range 1 + env.Intn(r, 12) {
		p := fmt.Sprintf("d%d/f%d.%s", env.Intn(r, 3), i,
			[]string{"vhd", "VHDL", "txt", "Vhd"}[env.Intn(r, 4)])
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p, filepath.Dir(p))
	}
	if env.Intn(r, 2) == 0 {
		paths = append(paths, "missing.vhd")
	}
	return paths
}

// TestCheckDeterministic runs check on random projects and requires
// byte-identical output and exit code whatever the job count, the job
// order, the order and repetition of the paths, and the environment
// variables (check never reads them: LANG and COLUMNS cannot matter).
func TestCheckDeterministic(t *testing.T) {
	for seed := range uint64(40) {
		e, _, _ := newEnv(t)
		dir, _ := e.FS.Getwd()
		paths := project(t, dir, seed)
		format := []string{"text", "json", "sarif", "junit"}[seed%4]
		var args []string
		if seed%2 == 1 {
			args = paths
		}
		perm := env.Shuffle(seed+1, 0, len(args))
		shuffled := slices.Clone(args)
		for i, j := range perm {
			shuffled[i] = args[j]
		}
		shuffled = append(shuffled, args...)
		runs := []struct {
			p    int
			jobs env.Jobs
			args []string
		}{
			{1, nil, args}, {16, nil, args}, {3, reverseJobs{}, args},
			{16, nil, shuffled}, {1, reverseJobs{}, shuffled},
		}
		var first string
		for i, run := range runs {
			var out, errb bytes.Buffer
			e.Transport.Stdout, e.Transport.Stderr = &out, &errb
			e.Getenv = func(k string) (string, bool) {
				panic("check read the environment variable " + k)
			}
			if run.jobs != nil {
				e.Jobs = run.jobs
			}
			a := append([]string{"-p", strconv.Itoa(run.p), "check",
				"--format", format, "--"}, run.args...)
			code := Run(context.Background(), e, a)
			got := fmt.Sprintf("exit %d\n%s\n%s", code, out.String(),
				errb.String())
			if strings.Contains(got, "internal/crash") {
				t.Fatalf("seed %d: %s", seed, got)
			}
			if i == 0 {
				first = got
			} else if got != first {
				t.Fatalf("seed %d run %d differs:\n%s\nvs\n%s", seed, i,
					got, first)
			}
		}
	}
}

// TestCheckDuplicatesCountOnce checks that naming the same file many
// ways never changes the file count.
func TestCheckDuplicatesCountOnce(t *testing.T) {
	e, out, _ := newEnv(t)
	dir, _ := e.FS.Getwd()
	if err := os.MkdirAll(filepath.Join(dir, "rtl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rtl", "a.vhd"), nil,
		0o644); err != nil {
		t.Fatal(err)
	}
	code := Run(context.Background(), e, []string{"check", "rtl/a.vhd",
		"rtl", ".", "./rtl/../rtl/a.vhd",
		filepath.Join(dir, "rtl", "a.vhd")})
	if code != 0 || !strings.HasSuffix(out.String(), ", 1 files\n") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// crashFS panics on every read, like a bug in a later stage would.
type crashFS struct{ env.FS }

func (crashFS) ReadFile(name string) ([]byte, error) {
	if strings.HasSuffix(name, ".vhd") {
		panic("boom in " + filepath.Base(name))
	}
	return nil, os.ErrNotExist
}

func TestCheckCrashWritesFile(t *testing.T) {
	var names []string
	for _, p := range []string{"1", "16", "1", "16"} {
		e, out, errb := newEnv(t)
		dir, _ := e.FS.Getwd()
		for _, n := range []string{"a.vhd", "b.vhd"} {
			if err := os.WriteFile(filepath.Join(dir, n), nil,
				0o644); err != nil {
				t.Fatal(err)
			}
		}
		e.FS = crashFS{e.FS}
		code := Run(context.Background(), e, []string{"-p", p, "check"})
		if code != 2 || !strings.Contains(out.String(),
			"error[internal/crash]") {
			t.Fatalf("-p %s: exit %d\n%s", p, code, out)
		}
		matches, _ := filepath.Glob(filepath.Join(dir, ".gohard", "crash",
			"*.txt"))
		if len(matches) != 1 || !strings.Contains(errb.String(),
			filepath.ToSlash(".gohard/crash/"+
				filepath.Base(matches[0]))) {
			t.Fatalf("-p %s: crash files %q, stderr %s", p, matches,
				errb)
		}
		names = append(names, out.String()+errb.String())
	}
	if len(slices.Compact(names)) != 1 {
		t.Fatalf("crash output depends on -p:\n%q", names)
	}
}

// TestCheckAndConfigCheckAgree: check and config check share one root
// rule and one config loader, so for any project they report the same
// config, library-folder and --define diagnostics and the same stderr
// override log, and an invalid configuration exits 2 from both.
func TestCheckAndConfigCheckAgree(t *testing.T) {
	yamls := []string{
		"", "lenient: yes\n", "libraries:\n  mylib: [nothere]\n",
		"libraries:\n  mylib: [src/a.vhd]\n", "libraries:\n  l: [src]\n",
		"defines:\n  WIDTH: \"8\"\n", "standard: 2019\n", "bogus: 1\n",
		"\xef\xbb\xbflenient: yes\n",
	}
	defines := [][]string{nil, {"WIDTH=16"}, {"WIDTH=1", "width=2"},
		{"X"}, {"1X=2"}, {"vhdl_version=2008"}}
	for i, y := range yamls {
		for j, ds := range defines {
			var args []string
			for _, d := range ds {
				args = append(args, "--define", d)
			}
			run := func(cmd ...string) (int, string, string) {
				e, out, errb := newEnv(t)
				dir, _ := e.FS.Getwd()
				src := filepath.Join(dir, "src")
				if err := os.MkdirAll(src, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(src, "a.vhd"),
					nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if y != "" {
					if err := os.WriteFile(filepath.Join(dir,
						"gohard.yaml"), []byte(y), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				code := Run(context.Background(), e,
					append(append(cmd, "--format", "text"), args...))
				var keep []string
				for l := range strings.Lines(out.String()) {
					if !strings.HasPrefix(l, "summary:") {
						keep = append(keep, l)
					}
				}
				return code, strings.Join(keep, ""), errb.String()
			}
			cc, cOut, cErr := run("check")
			kc, kOut, kErr := run("config", "check")
			if cOut != kOut || cErr != kErr || (kc == 2) != (cc == 2) {
				t.Errorf("yaml %d defines %d:\ncheck exit %d\n%s%s\n"+
					"config check exit %d\n%s%s", i, j, cc, cOut, cErr,
					kc, kOut, kErr)
			}
		}
	}
}

// limitJobs records the limit of every Run call and runs jobs in order.
type limitJobs struct{ limits *[]int }

func (l limitJobs) Run(limit, n int, job func(int)) {
	*l.limits = append(*l.limits, limit)
	for i := range n {
		job(i)
	}
}

// TestCheckPassesJobLimit: check hands -p N to the job runner, so -p 16
// can really run in parallel; without -p the runner's default (0) is
// used.
func TestCheckPassesJobLimit(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"-p", "16", "check"}, 16},
		{[]string{"-p", "3", "check"}, 3},
		{[]string{"--jobs=5", "check"}, 5},
		{[]string{"check"}, 0},
	} {
		e, _, _ := newEnv(t)
		dir, _ := e.FS.Getwd()
		project(t, dir, 1)
		var limits []int
		e.Jobs = limitJobs{&limits}
		if code := Run(context.Background(), e, tc.args); code != 0 &&
			code != 1 {
			t.Fatalf("%q: exit %d", tc.args, code)
		}
		if len(limits) == 0 {
			t.Fatalf("%q: the job runner was never used", tc.args)
		}
		for _, l := range limits {
			if l != tc.want {
				t.Errorf("%q: limits %v, want all %d", tc.args, limits,
					tc.want)
				break
			}
		}
	}
}
