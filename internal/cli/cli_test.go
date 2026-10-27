package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
)

func newEnv(t *testing.T) (env.Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return realenv.New(realenv.Options{
		Dir: t.TempDir(), Stdout: &out, Stderr: &errb,
	}), &out, &errb
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		args    []string
		cmd     string
		pos     []string
		values  map[string][]string
		problem string
	}{
		{args: nil},
		{args: []string{"check"}, cmd: "check"},
		{
			args: []string{"-p", "4", "check", "a.vhd", "--strict",
				"--define", "A=1", "--define=B=2", "b.vhd"},
			cmd: "check", pos: []string{"a.vhd", "b.vhd"},
			values: map[string][]string{
				"jobs": {"4"}, "strict": {"true"},
				"define": {"A=1", "B=2"},
			},
		},
		{
			args: []string{"check", "-p16", "--sim=false", "--",
				"--strict"},
			cmd: "check", pos: []string{"--strict"},
			values: map[string][]string{
				"jobs": {"16"}, "sim": {"false"},
			},
		},
		{
			args: []string{"--", "version"}, cmd: "version",
		},
		{args: []string{"-"}, problem: `unknown command "-"`},
		{
			args:    []string{"--strict", "check"},
			problem: "unknown flag --strict",
		},
		{
			args:    []string{"check", "--bogus"},
			problem: "unknown flag --bogus for gohard check",
		},
		{
			args:    []string{"check", "-hx"},
			problem: "unknown flag -hx for gohard check",
		},
		{
			args:    []string{"nope"},
			problem: `unknown command "nope"`,
		},
		{
			args:    []string{"check", "--tb"},
			problem: "--tb needs a value",
		},
		{
			args:    []string{"check", "--tb", "a", "--tb", "b"},
			problem: "--tb is given more than once",
		},
		{
			args:    []string{"-p", "0", "check"},
			problem: `--jobs needs a positive integer, not "0"`,
		},
		{
			args:    []string{"check", "--format", "xml"},
			problem: `--format must be one of text, json, sarif, junit, not "xml"`,
		},
		{
			args:    []string{"check", "--sim=yes"},
			problem: `--sim must be true or false, not "yes"`,
		},
	}
	for _, tt := range tests {
		p, err := parseArgs(tt.args)
		got := ""
		if err != nil {
			got = err.problem
		}
		if got != tt.problem {
			t.Errorf("%q: problem %q, want %q", tt.args, got, tt.problem)
			continue
		}
		if err != nil {
			continue
		}
		name := ""
		if p.cmd != nil {
			name = p.cmd.Name
		}
		if name != tt.cmd || !slices.Equal(p.args, tt.pos) {
			t.Errorf("%q: cmd %q args %q", tt.args, name, p.args)
		}
		for k, v := range tt.values {
			if !slices.Equal(p.values[k], v) {
				t.Errorf("%q: --%s = %q, want %q", tt.args, k,
					p.values[k], v)
			}
		}
	}
}

func TestCommandTable(t *testing.T) {
	want := []string{"check", "config", "fmt", "help", "import", "libs",
		"lsp", "parse", "trace", "version"}
	var got []string
	for _, c := range commands {
		got = append(got, c.Name)
		if c.Summary == "" {
			t.Errorf("%s: no summary", c.Name)
		}
		seen := map[string]bool{}
		for _, f := range slices.Concat(globalFlags, c.Flags) {
			if seen[f.Name] || f.Short != "" && seen["-"+f.Short] {
				t.Errorf("%s: flag %s declared twice", c.Name, f.Name)
			}
			seen[f.Name], seen["-"+f.Short] = true, f.Short != ""
			if f.Usage == "" || f.Kind == 0 {
				t.Errorf("%s --%s: missing usage or kind",
					c.Name, f.Name)
			}
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
}

// TestExitCodesDefined runs many generated command lines and checks every
// run ends in an exit code #20 defines and writes stdout at most once.
func TestExitCodesDefined(t *testing.T) {
	words := []string{"check", "fmt", "parse", "config", "lsp", "trace",
		"libs", "version", "help", "nope", "--strict", "--format",
		"json", "sarif", "junit", "xml", "-p", "3", "-h", "--help", "--",
		"a.vhd", "check", "schema", "export", "detect", "--output",
		"--define", "--sim=maybe", "-"}
	s := uint64(1)
	for n := range 400 {
		var args []string
		for range n % 6 {
			s = s*6364136223846793005 + 1442695040888963407
			args = append(args, words[(s>>33)%uint64(len(words))])
		}
		if slices.Contains(args, "--output") {
			continue // covered by TestOutputFile
		}
		e, out, _ := newEnv(t)
		w := &countWriter{w: out}
		e.Transport.Stdout = w
		code := Run(context.Background(), e, args)
		if !slices.Contains([]int{0, 1, 2, 130, 143}, code) {
			t.Fatalf("%q: exit %d", args, code)
		}
		if w.writes > 1 {
			t.Fatalf("%q: stdout written %d times", args, w.writes)
		}
	}
}

type countWriter struct {
	w      *bytes.Buffer
	writes int
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.writes++
	return c.w.Write(p)
}

func TestNotImplemented(t *testing.T) {
	// Every shipped command is built, so the dispatcher path for a
	// command without Run is checked with libs' Run taken away.
	libs := lookup("libs")
	run := libs.Run
	libs.Run = nil
	libs.Flags = append(libs.Flags, formatFlag)
	defer func() {
		libs.Run = run
		libs.Flags = libs.Flags[:len(libs.Flags)-1]
	}()
	e, out, _ := newEnv(t)
	code := Run(context.Background(), e,
		[]string{"libs", "detect", "--format", "json"})
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
	var m struct {
		Status string
		Error  struct{ Code, Message string }
	}
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Status != "tool_error" || m.Error.Code != "cli/not-implemented" ||
		m.Error.Message != "gohard libs is not implemented in this build" {
		t.Fatalf("%+v", m)
	}
	e, out, _ = newEnv(t)
	if code := Run(context.Background(), e, []string{"libs"}); code != 2 ||
		!strings.Contains(out.String(),
			"gohard libs needs a subcommand: detect") {
		t.Fatalf("missing subcommand: %d %s", code, out)
	}
}

func TestOutputFile(t *testing.T) {
	e, out, _ := newEnv(t)
	dir, _ := e.FS.Getwd()
	if err := os.WriteFile(filepath.Join(dir, "v.txt"), []byte("old"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run(context.Background(), e,
		[]string{"version", "--output", "v.txt"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "v.txt"))
	if !strings.HasPrefix(string(b), "version "+Version+"\ncommit ") ||
		out.Len() != 0 {
		t.Fatalf("file %q, stdout %q", b, out)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}
	// A failing write is exit 2 with the reason on stderr, and the
	// target is untouched.
	e, out, errb := newEnv(t)
	code := Run(context.Background(), e,
		[]string{"version", "--output", "missing/dir/v.txt"})
	if code != 2 || out.Len() != 0 ||
		!strings.HasPrefix(errb.String(),
			"gohard: error[cli/output-failed]: cannot write "+
				"missing/dir/v.txt: ") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errb)
	}
	// The report is readable like any file the user writes, not 0600
	// as the temp file was created.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "v.txt"))
		if err != nil || fi.Mode().Perm() != 0o644 {
			t.Fatalf("mode %v (%v), want 0644", fi.Mode(), err)
		}
	}
	// The failure message never names the random temp file, so two
	// identical failing runs give identical stderr (P1).
	var first string
	for i := range 3 {
		e, _, errb := newEnv(t)
		dir, _ := e.FS.Getwd()
		if err := os.MkdirAll(filepath.Join(dir, "d", "x"),
			0o755); err != nil {
			t.Fatal(err)
		}
		if code := Run(context.Background(), e,
			[]string{"version", "--output", "d"}); code != 2 {
			t.Fatalf("exit %d", code)
		}
		got := errb.String()
		if strings.Contains(got, ".tmp") || strings.Contains(got, dir) {
			t.Fatalf("stderr names the temp file: %q", got)
		}
		if i == 0 {
			first = got
		} else if got != first {
			t.Fatalf("stderr differs between runs: %q vs %q", first, got)
		}
	}
}

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

// TestStdoutWriteError: a closed pipe keeps the run's exit code and is
// quiet; any other stdout failure is cli/output-failed, exit 2 (#20).
func TestStdoutWriteError(t *testing.T) {
	pipe := fmt.Errorf("write |1: %w", env.ErrBrokenPipe)
	other := errors.New("write /dev/stdout: no space left on device")
	for _, tc := range []struct {
		err    error
		exit   int
		stderr string
	}{
		{pipe, 0, ""},
		{other, 2, "gohard: error[cli/output-failed]: cannot write " +
			"standard output: write /dev/stdout: no space left on " +
			"device\n"},
	} {
		var errb bytes.Buffer
		e := realenv.New(realenv.Options{Dir: t.TempDir(),
			Stdout: failWriter{tc.err}, Stderr: &errb})
		if code := Run(context.Background(), e,
			[]string{"version"}); code != tc.exit ||
			errb.String() != tc.stderr {
			t.Errorf("%v: exit %d stderr %q", tc.err, code, errb.String())
		}
	}
}

func TestInterrupt(t *testing.T) {
	for _, intr := range []env.Interrupt{env.SIGINT, env.SIGTERM} {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(intr)
		e, out, _ := newEnv(t)
		code := Run(ctx, e, []string{"check", "--format", "json"})
		if code != intr.Exit {
			t.Fatalf("%v: exit %d", intr, code)
		}
		if !strings.Contains(out.String(), `"status": "interrupted"`) ||
			!strings.Contains(out.String(), "cli/interrupted") {
			t.Fatalf("%v: %s", intr, out)
		}
		e, out, _ = newEnv(t)
		if code := Run(ctx, e, []string{"version"}); code != intr.Exit ||
			out.Len() != 0 {
			t.Fatalf("%v: raw output must be dropped: %d %q",
				intr, code, out)
		}
	}
}

func TestCrash(t *testing.T) {
	commands = append(commands, Command{
		Name: "zz-crash", Summary: "test",
		Run: func(*Invocation) Result { panic("kaboom") },
	})
	defer func() { commands = commands[:len(commands)-1] }()
	var files []string
	for range 2 {
		e, out, errb := newEnv(t)
		code := Run(context.Background(), e, []string{"zz-crash"})
		if code != 2 {
			t.Fatalf("exit %d", code)
		}
		line := strings.SplitN(out.String(), "\n", 2)[0]
		const prefix = "gohard: error[internal/crash]: internal " +
			"error: kaboom; crash report written to "
		if !strings.HasPrefix(line, prefix) ||
			!strings.Contains(errb.String(), "kaboom") {
			t.Fatalf("stdout %q stderr %q", out, errb)
		}
		file := strings.TrimPrefix(line, prefix)
		dir, _ := e.FS.Getwd()
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || !bytes.Contains(b, []byte("panic: kaboom")) {
			t.Fatalf("crash file %s: %v", file, err)
		}
		files = append(files, file)
	}
	if files[0] != files[1] {
		t.Fatalf("crash file names differ: %v", files)
	}
}

func TestInvocationFlags(t *testing.T) {
	var got *Invocation
	commands = append(commands, Command{
		Name: "zz-flags", Summary: "test",
		Flags: []Flag{formatFlag, strictFlag, defineFlag, tbFlag},
		Run: func(inv *Invocation) Result {
			got = inv
			return Result{Report: inv.NewReport()}
		},
	})
	defer func() { commands = commands[:len(commands)-1] }()
	e, out, _ := newEnv(t)
	code := Run(context.Background(), e, []string{"zz-flags", "-p", "3",
		"--strict", "--define", "A=1", "--define", "B=2", "x"})
	if code != 0 || got == nil {
		t.Fatalf("exit %d", code)
	}
	if got.Jobs != 3 || !got.Bool("strict") || got.IsSet("tb") ||
		got.String("format") != "text" || got.Format() != diag.FormatText ||
		!slices.Equal(got.Strings("define"), []string{"A=1", "B=2"}) ||
		!slices.Equal(got.Args, []string{"x"}) ||
		!got.NewReport().Strict {
		t.Fatalf("%+v", got)
	}
	if out.String() != diag.SummaryLine(diag.Counts{})+"\n" {
		t.Fatalf("stdout %q", out)
	}
}
