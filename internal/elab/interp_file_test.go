package elab

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// fileRun is a simulation whose file door is a sandbox over a small
// project at /work (in.txt, and GoHard's own .gohard/traces/tb.trace).
type fileRun struct {
	ov    *env.Overlay
	k     *sim.Kernel
	out   []string // report messages, in order
	diags []diag.Diagnostic
}

func simFiles(t *testing.T, src, std string) fileRun {
	t.Helper()
	return simFilesRun(t, src, std, func(k *sim.Kernel) {
		k.RunAll(64, nil)
	})
}

// simFilesRun is simFiles with run driving the kernel. The project
// also has data/stim.txt, a file outside it (/outside/secret.txt) and
// links: out -> /outside, gh -> .gohard, data/ok.txt -> ../in.txt.
func simFilesRun(t *testing.T, src, std string,
	run func(*sim.Kernel),
) fileRun {
	t.Helper()
	root := filepath.FromSlash("/work")
	base := simenv.New(simenv.Options{Dir: root, Files: map[string][]byte{
		"in.txt":                  []byte("stimulus\n"),
		"data/stim.txt":           []byte("data\n"),
		".gohard/traces/tb.trace": []byte("an earlier run\n"),
		"/outside/secret.txt":     []byte("secret\n"),
	}}).FS
	for _, l := range [][2]string{{"/outside", "/work/out"},
		{".gohard", "/work/gh"}, {"../in.txt", "/work/data/ok.txt"}} {
		if err := base.Symlink(filepath.FromSlash(l[0]),
			filepath.FromSlash(l[1])); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.Standard = std
	r := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "t.vhd", Text: []byte(src)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %d:%d: %s", d.Line, d.Col, d.Message)
		}
	}
	res := fileRun{ov: env.NewOverlay(base, root)}
	code := Interpreter(InterpOptions{FS: res.ov})
	b := eval.NewBudget(0)
	d, ds := Elaborate(r, Options{Top: "tb", Budget: b, Code: code})
	if d == nil {
		t.Fatalf("elaboration: %v", ds)
	}
	k, err := sim.New(d.Model, sim.Options{Budget: b,
		TimeBudget: 1_000_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	run(k)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	res.k, res.diags = k, k.Diagnostics()
	for _, x := range res.diags {
		if x.Code == diag.SimAssertNote {
			res.out = append(res.out, strings.TrimPrefix(x.Message,
				"report note: "))
		}
	}
	return res
}

// files returns what the run wrote, as path=content lines.
func (r fileRun) files() []string {
	var out []string
	for _, f := range r.ov.Files() {
		out = append(out, f.Path+"="+string(f.Data))
	}
	return out
}

func (r fileRun) errors(t *testing.T) {
	t.Helper()
	for _, d := range r.diags {
		if d.Severity == diag.Error && d.Code != diag.SimAssertError {
			t.Errorf("%s: %s", d.Code, d.Message)
		}
	}
}

// TestFileImplicitClose: a file the design never closes is closed at
// the end of the run (LRM 5.5.2) and written like a closed one, for
// declared and FILE_OPENed files, in a run that ends quietly, stops,
// fails on an assertion, fails on a runtime error or is cancelled.
func TestFileImplicitClose(t *testing.T) {
	const src = `
use std.textio.all;
use std.env.all;
entity tb is end;
architecture a of tb is
begin
  process
    file d : text open write_mode is "decl.txt";
    file o : text;
    variable l : line;
    variable a : bit_vector(0 to 1);
    variable i : integer := 5;
  begin
    file_open(o, "opened.txt", write_mode);
    write(l, string'("d1")); writeline(d, l);
    write(l, string'("o1")); writeline(o, l);
    wait for 1 ns;
    write(l, string'("d2")); writeline(d, l);
    %s
  end process;
end;
`
	want := []string{"decl.txt=d1\nd2\n", "opened.txt=o1\n"}
	// An index out of range after the writes: the run fails.
	const fail = "a(i) := '1'; wait;"
	for _, end := range []string{"wait;", "stop;",
		"assert false severity failure; wait;", fail} {
		for _, std := range []string{"2008", "2019"} {
			r := simFiles(t, fmt.Sprintf(src, end), std)
			if end != fail {
				r.errors(t)
			} else if st := r.k.Status(); st != sim.Failed {
				t.Errorf("%s: status %v, want failed", std, st)
			}
			if got := r.files(); !slices.Equal(got, want) {
				t.Errorf("%s %q: wrote %q, want %q", std, end, got, want)
			}
		}
	}
	// Cancelled while waiting forever in 1 ns steps, after the writes.
	for _, std := range []string{"2008", "2019"} {
		r := simFilesRun(t, fmt.Sprintf(src,
			"loop wait for 1 ns; end loop;"), std, func(k *sim.Kernel) {
			k.Run(16)
			k.Cancel()
		})
		r.errors(t)
		if st := r.k.Status(); st != sim.Cancelled {
			t.Errorf("%s: status %v, want cancelled", std, st)
		}
		if got := r.files(); !slices.Equal(got, want) {
			t.Errorf("%s cancelled: wrote %q, want %q", std, got, want)
		}
	}
}

// TestFileOpenSandbox: a name outside the project root, inside
// .gohard (directly or through a symbolic link) or naming a directory
// (the empty name is the root) is NAME_ERROR when opened in any mode,
// never a failure at FILE_CLOSE or a write dropped silently; nothing
// is written. A link inside the project reads its target.
func TestFileOpenSandbox(t *testing.T) {
	const src = `
use std.textio.all;
entity tb is end;
architecture a of tb is
begin
  process
    file f : text;
    variable st : file_open_status;
    variable l : line;
  begin
    file_open(st, f, %q, %s);
    report file_open_status'image(st);
    if st = open_ok then
      write(l, string'("x")); writeline(f, l);
    end if;
    wait;
  end process;
end;
`
	for _, name := range []string{"../escape.txt", "sub/../../x.txt",
		"/etc/x.txt", "/work/../x.txt", ".gohard/traces/tb.trace",
		".gohard/run/x.txt", "/work/.gohard/new.txt",
		"out/secret.txt", "out/new.txt", "gh/traces/tb.trace",
		"gh/new.txt", "", ".", "data", "/work", "/work/data/"} {
		for _, mode := range []string{"read_mode", "write_mode",
			"append_mode"} {
			r := simFiles(t, fmt.Sprintf(src, name, mode), "2008")
			r.errors(t)
			if !slices.Equal(r.out, []string{"name_error"}) {
				t.Errorf("%s %s: %q, want name_error", name, mode, r.out)
			}
			if got := r.files(); len(got) != 0 {
				t.Errorf("%s %s: wrote %q", name, mode, got)
			}
		}
	}
	r := simFiles(t, fmt.Sprintf(src, "/work/sub/in.txt", "write_mode"),
		"2008")
	r.errors(t)
	if want := []string{"sub/in.txt=x\n"}; !slices.Equal(r.files(),
		want) {
		t.Errorf("absolute name under the root: %q, want %q", r.files(),
			want)
	}
	const read = `
use std.textio.all;
entity tb is end;
architecture a of tb is
begin
  process
    file f : text;
    variable l : line;
  begin
    file_open(f, "data/ok.txt", read_mode);
    readline(f, l);
    report l.all;
    wait;
  end process;
end;
`
	r = simFiles(t, read, "2008")
	r.errors(t)
	if !slices.Equal(r.out, []string{"stimulus"}) {
		t.Errorf("read through an in-project link: %q", r.out)
	}
	if in := r.ov.Inputs(); len(in) != 1 || in[0].Path != "in.txt" {
		t.Errorf("inputs %+v, want in.txt", in)
	}
}

// TestFileOfNonText: READ and WRITE of a file whose values are not
// STRING (a file of INTEGER) are unsupported/construct, never a wrong
// value read or written silently, and no value is written.
func TestFileOfNonText(t *testing.T) {
	const src = `
entity tb is end;
architecture a of tb is
  type int_file is file of integer;
begin
  process
    file f : int_file;
    variable i : integer;
  begin
    file_open(f, %q, %s);
    %s;
    report integer'image(i);
    wait;
  end process;
end;
`
	for _, op := range [][3]string{{"x.bin", "write_mode", "write(f, 42)"},
		{"in.txt", "read_mode", "read(f, i)"}} {
		for _, std := range []string{"2008", "2019"} {
			r := simFiles(t, fmt.Sprintf(src, op[0], op[1], op[2]), std)
			n := 0
			for _, d := range r.diags {
				if d.Code == diag.UnsupportedConstruct {
					n++
				}
			}
			// The file opened in write mode is closed empty.
			files := slices.DeleteFunc(r.files(), func(f string) bool {
				return f == "x.bin="
			})
			if n != 1 || len(r.out) != 0 || len(files) != 0 {
				t.Errorf("%s %s: %d unsupported, reports %q, files %q",
					std, op[2], n, r.out, r.files())
			}
		}
	}
}

// TestFileOpenKinds: WRITE_MODE truncates, APPEND_MODE appends and
// READ_WRITE_MODE (no native yet) is MODE_ERROR, under every standard,
// whatever the positions of the FILE_OPEN_KIND literals.
func TestFileOpenKinds(t *testing.T) {
	const src = `
use std.textio.all;
entity tb is end;
architecture a of tb is
begin
  process
    file f : text;
    variable st : file_open_status;
    variable l : line;
    procedure put(s : string; k : file_open_kind) is
    begin
      file_open(st, f, "o.txt", k);
      report file_open_status'image(st);
      if st = open_ok then
        write(l, s); writeline(f, l); file_close(f);
      end if;
    end procedure;
  begin
    put("first", write_mode);
    put("second", write_mode);
    put("third", append_mode);
    put("in", append_mode);
    %s
    wait;
  end process;
end;
`
	for _, std := range []string{"1993", "2008", "2019"} {
		r := simFiles(t, fmt.Sprintf(src, ""), std)
		r.errors(t)
		want := []string{"open_ok", "open_ok", "open_ok", "open_ok"}
		if !slices.Equal(r.out, want) {
			t.Errorf("%s: statuses %q, want %q", std, r.out, want)
		}
		if got, w := r.files(), []string{
			"o.txt=second\nthird\nin\n"}; !slices.Equal(got, w) {
			t.Errorf("%s: wrote %q, want %q", std, got, w)
		}
	}
	r := simFiles(t, fmt.Sprintf(src, `put("rw", read_write_mode);`),
		"2019")
	r.errors(t)
	if got := r.out[len(r.out)-1]; got != "mode_error" {
		t.Errorf("read_write_mode: %s, want mode_error", got)
	}
}
