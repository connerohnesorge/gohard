package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env"
)

// fakeTB records failures instead of failing the real test, so the
// harness can be run against fixtures that must fail.
type fakeTB struct {
	*testing.T
	errs []string
}

// stopRun ends a fake run on Fatal, as runtime.Goexit would.
type stopRun struct{}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.Errorf(format, args...)
	panic(stopRun{})
}

func (f *fakeTB) Fatal(args ...any) {
	f.errs = append(f.errs, fmt.Sprint(args...))
	panic(stopRun{})
}

func (f *fakeTB) Skip(args ...any) {
	f.errs = append(f.errs, "skip: "+fmt.Sprint(args...))
	panic(stopRun{})
}

// harness runs runFixture on dir with run and returns what it reported.
func harness(t *testing.T, dir string, run runner) []string {
	f := &fakeTB{T: t}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(stopRun); !ok {
					panic(r)
				}
			}
		}()
		runFixture(f, dir, run)
	}()
	return f.errs
}

// fixture writes a fixture directory from name -> content.
func fixture(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestHarness proves the fixture harness itself: it runs every case at
// -p 1 and -p 16 by inserting -p, each with a cold, warm and corrupted
// analysis cache, and it fails a case whose exit code, stdout or
// stderr differ from the want files, or whose output depends on -p or
// on the cache. Without this the #20 gate is only as strong as unreviewed
// harness code.
func TestHarness(t *testing.T) {
	if !slices.Equal(jobCounts, []int{1, 16}) {
		t.Fatalf("jobCounts = %v, want [1 16]", jobCounts)
	}
	if *update {
		t.Skip("-update rewrites want files instead of comparing")
	}
	var version strings.Builder
	cli.Run(context.Background(), env.Env{Transport: env.Transport{
		Stdout: &version, Stderr: &version}}, []string{"version"})
	good := map[string]string{
		"cmd": "gohard version\n", "want.exit": "0\n",
		"want.stdout": version.String(), "want.stderr": "",
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for key, val := range good {
			m[key] = val
		}
		m[k] = v
		return m
	}

	var seen [][]string
	record := func(ctx context.Context, e env.Env, args []string) int {
		seen = append(seen, slices.Clone(args))
		return cli.Run(ctx, e, args)
	}
	if errs := harness(t, fixture(t, good), record); len(errs) != 0 {
		t.Fatalf("a correct fixture failed: %q", errs)
	}
	want := [][]string{{"-p", "1", "version"}, {"-p", "16", "version"}}
	if !slices.EqualFunc(seen, want, slices.Equal) {
		t.Errorf("harness ran %q, want %q", seen, want)
	}

	bad := map[string]map[string]string{
		"exit":   with("want.exit", "1\n"),
		"stdout": with("want.stdout", "gohard 0.0.0-wrong\n"),
		"stderr": with("want.stderr", "noise\n"),
	}
	for name, files := range bad {
		if errs := harness(t, fixture(t, files), cli.Run); len(errs) == 0 {
			t.Errorf("a fixture with a wrong %s passed", name)
		}
	}
	// A command whose output depends on -p must fail even when the
	// want files match the -p 1 run.
	byJobs := func(ctx context.Context, e env.Env, args []string) int {
		fmt.Fprintf(e.Transport.Stdout, "%s\n", args[1])
		return 0
	}
	errs := harness(t, fixture(t, with("want.stdout", "1\n")), byJobs)
	if len(errs) == 0 || !strings.Contains(errs[0], "differs") {
		t.Errorf("a -p dependent run passed: %q", errs)
	}
	// So must one whose stderr or exit code depends on -p.
	errJobs := func(ctx context.Context, e env.Env, args []string) int {
		fmt.Fprintf(e.Transport.Stderr, "%s\n", args[1])
		return 0
	}
	if errs := harness(t, fixture(t, good), errJobs); len(errs) == 0 {
		t.Error("a -p dependent stderr passed")
	}
	// Even when want.stderr matches the -p 1 run, so only the cross-run
	// comparison can catch it.
	errs = harness(t, fixture(t, with("want.stderr", "1\n")), errJobs)
	if len(errs) == 0 || !strings.Contains(errs[0], "differs") {
		t.Errorf("a -p 16 only stderr change passed: %q", errs)
	}

	// The project tree after the run is compared with want/ and across
	// -p values.
	writes := func(body func(args []string) string) runner {
		return func(ctx context.Context, e env.Env, args []string) int {
			if err := e.FS.WriteFile("out.txt", []byte(body(args)),
				0o644); err != nil {
				t.Fatal(err)
			}
			return 0
		}
	}
	fixed := writes(func([]string) string { return "x" })
	tree := func(content string) map[string]string {
		m := with("want.stdout", "")
		m["want/out.txt"] = content
		return m
	}
	if errs := harness(t, fixture(t, tree("x")), fixed); len(errs) != 0 {
		t.Errorf("a correct want/ tree failed: %q", errs)
	}
	if errs := harness(t, fixture(t, tree("y")), fixed); len(errs) == 0 {
		t.Error("a wrong output file passed")
	}
	byJobsTree := writes(func(args []string) string { return args[1] })
	errs = harness(t, fixture(t, tree("1")), byJobsTree)
	if len(errs) == 0 || !strings.Contains(errs[0], "differs") {
		t.Errorf("a -p 16 only output file change passed: %q", errs)
	}
	// Each case also runs with the analysis cache the cold run wrote
	// and with that cache corrupted (#37): output that depends on the
	// cache fails, and so does a cache that depends on -p.
	byCache := func(ctx context.Context, e env.Env, args []string) int {
		b, err := e.FS.ReadFile(".gohard/cache/sema/x")
		switch {
		case err != nil:
			fmt.Fprintln(e.Transport.Stdout, "cold")
		case string(b) == "entry":
			fmt.Fprintln(e.Transport.Stdout, "warm")
		default:
			fmt.Fprintln(e.Transport.Stdout, "corrupted")
		}
		_ = e.FS.MkdirAll(".gohard/cache/sema", 0o755)
		_ = e.FS.WriteFile(".gohard/cache/sema/x", []byte("entry"),
			0o644)
		return 0
	}
	errs = harness(t, fixture(t, with("want.stdout", "cold\n")), byCache)
	if len(errs) == 0 || !strings.Contains(errs[0], "warm cache") {
		t.Errorf("a run that depends on the cache passed: %q", errs)
	}
	warmOnly := func(ctx context.Context, e env.Env, args []string) int {
		b, err := e.FS.ReadFile(".gohard/cache/sema/x")
		if err == nil && string(b) != "entry" {
			fmt.Fprintln(e.Transport.Stdout, "corrupted")
		}
		_ = e.FS.MkdirAll(".gohard/cache/sema", 0o755)
		_ = e.FS.WriteFile(".gohard/cache/sema/x", []byte("entry"),
			0o644)
		return 0
	}
	errs = harness(t, fixture(t, with("want.stdout", "")), warmOnly)
	if len(errs) == 0 || !strings.Contains(errs[0], "corrupted cache") {
		t.Errorf("a run that depends on a corrupted cache passed: %q",
			errs)
	}
	cacheByJobs := func(ctx context.Context, e env.Env,
		args []string,
	) int {
		_ = e.FS.MkdirAll(".gohard/cache/sema", 0o755)
		_ = e.FS.WriteFile(".gohard/cache/sema/x", []byte(args[1]),
			0o644)
		return 0
	}
	errs = harness(t, fixture(t, with("want.stdout", "")), cacheByJobs)
	if len(errs) == 0 || !strings.Contains(errs[0], "analysis cache") {
		t.Errorf("a -p dependent cache passed: %q", errs)
	}
	// The cache files are not part of the compared project tree.
	if errs := harness(t, fixture(t, tree("x")), func(ctx context.Context,
		e env.Env, args []string,
	) int {
		_ = e.FS.MkdirAll(".gohard/cache", 0o755)
		_ = e.FS.WriteFile(".gohard/cache/index", []byte("i"), 0o644)
		return fixed(ctx, e, args)
	}); len(errs) != 0 {
		t.Errorf("the cache folder counted as project tree: %q", errs)
	}

	exitJobs := func(ctx context.Context, e env.Env, args []string) int {
		if args[1] == "16" {
			return 1
		}
		return 0
	}
	if errs := harness(t, fixture(t, with("want.stdout", "")),
		exitJobs); len(errs) == 0 {
		t.Error("a -p dependent exit code passed")
	}
}
