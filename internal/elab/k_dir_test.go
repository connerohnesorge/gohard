package elab

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/token"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// dirProject is the project of the DIR_* tests, at /work: files in
// an unsorted creation order, a directory, GoHard's own .gohard and a
// link out of the project.
func dirProject(t *testing.T) (*env.Overlay, string) {
	t.Helper()
	root := filepath.FromSlash("/work")
	base := simenv.New(simenv.Options{Dir: root, Files: map[string][]byte{
		"tb.vhd":         []byte("--\n"),
		"data/zeta.txt":  []byte("z\n"),
		"data/alpha.txt": []byte("a\n"),
		"data/Mid.txt":   []byte("m\n"),
		// Names that sort before "." in byte order: the listing is
		// sorted as a whole, "." and ".." included (dirList).
		"data/+a.txt":             []byte("p\n"),
		"data/-x.txt":             []byte("x\n"),
		"data/sub/deep.txt":       []byte("d\n"),
		".gohard/traces/tb.trace": []byte("an earlier run\n"),
		"/outside/secret.txt":     []byte("secret\n"),
	}}).FS
	if err := base.Symlink(filepath.FromSlash("/outside"),
		filepath.FromSlash("/work/out")); err != nil {
		t.Fatal(err)
	}
	return env.NewOverlay(base, root), root
}

// dirSim runs top of src (VHDL-2019) with the overlay as both doors.
func dirSim(t *testing.T, src, top string, ov *env.Overlay, root string,
) gRun {
	t.Helper()
	r := kAnalyze(t, src, "2019")
	b := eval.NewBudget(0)
	code := Interpreter(InterpOptions{FS: ov, Env: EnvOptions{
		Standard: token.Std2019, Root: root, FS: ov}})
	d := gElab(t, r, top, code, b)
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Budget: b,
		TimeBudget: 1_000_000_000_000, Writer: rec})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	res := gRun{k: k}
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	res.text = trace.Encode(tr)
	for _, e := range tr.Events {
		if e.Kind == trace.Report || e.Kind == trace.Assert {
			res.msgs = append(res.msgs, e.Message)
		}
	}
	for _, x := range k.Diagnostics() {
		if x.Severity == diag.Error {
			res.errs = append(res.errs, string(x.Code)+": "+x.Message)
		}
	}
	return res
}

const dirListSrc = `
use std.env.all;
use std.textio.all;
entity tb is end entity;
architecture sim of tb is
  procedure list(p : string) is
    variable d  : directory;
    variable st : dir_open_status;
    variable l  : line;
  begin
    st := dir_open(d, p);
    write(l, p & ": " & dir_open_status'image(st));
    if st = status_ok then
      write(l, " " & d.name.all & " [");
      for i in d.items'range loop
        write(l, " " & d.items(i).all);
      end loop;
      write(l, string'(" ]"));
      dir_close(d);
    end if;
    report l.all;
    deallocate(l);
  end procedure;
begin
  process
    variable st : dir_open_status;
    variable d  : directory;
  begin
    list("data");
    list(".");
    list("data/sub");
    list("missing");
    list("tb.vhd");
    list("..");
    list(".gohard");
    list("out");
    list("/work/data/sub");
    dir_open(d, "data", st);
    report "procedure " & dir_open_status'image(st) & " " &
      integer'image(d.items'length);
    dir_close(d);
    wait;
  end process;
end architecture;
`

// TestDirOpenSorted: DIR_OPEN lists a directory sorted by name, "."
// and ".." included, with its root-relative name; denied, missing and
// non-directory paths get their statuses and the listings the run
// made of the project are recorded.
func TestDirOpenSorted(t *testing.T) {
	ov, root := dirProject(t)
	r := dirSim(t, dirListSrc, "tb", ov, root)
	r.want(t,
		"data: status_ok data [ +a.txt -x.txt . .. Mid.txt alpha.txt "+
			"sub zeta.txt ]",
		".: status_ok . [ . .. data out tb.vhd ]",
		"data/sub: status_ok data/sub [ . .. deep.txt ]",
		"missing: status_not_found",
		"tb.vhd: status_no_directory",
		"..: status_access_denied",
		".gohard: status_access_denied",
		"out: status_access_denied",
		"/work/data/sub: status_ok data/sub [ . .. deep.txt ]",
		"procedure status_ok 8")
	var got []string
	for _, l := range ov.Listings() {
		got = append(got, l.Path)
		if l.SHA256 == "" {
			t.Errorf("listing %s recorded as absent", l.Path)
		}
	}
	if want := []string{".", "data", "data/sub"}; !slices.Equal(got,
		want) {
		t.Errorf("listings %v, want %v", got, want)
	}
	if len(ov.Files()) != 0 || len(ov.Dirs()) != 0 {
		t.Errorf("a listing wrote %v %v", ov.Files(), ov.Dirs())
	}
}

const dirWriteSrc = `
use std.env.all;
use std.textio.all;
entity tb is end entity;
architecture sim of tb is
begin
  process
    file f : text;
    variable l  : line;
    variable cs : dir_create_status;
    variable ds : dir_delete_status;
    variable fs : file_delete_status;
    variable os : dir_open_status;
    variable d  : directory;
  begin
    cs := dir_createdir("out2");
    report "mk " & dir_create_status'image(cs);
    cs := dir_createdir("out2");
    report "mk again " & dir_create_status'image(cs);
    cs := dir_createdir("a/b/c");
    report "mk no parents " & dir_create_status'image(cs);
    dir_createdir("a/b/c", true, cs);
    report "mk parents " & dir_create_status'image(cs);
    cs := dir_createdir("../x");
    report "mk outside " & dir_create_status'image(cs);
    cs := dir_createdir("tb.vhd");
    report "mk file " & dir_create_status'image(cs);
    report "isdir " & boolean'image(dir_itemisdir("a/b")) &
      " isfile " & boolean'image(dir_itemisfile("a/b")) &
      " exists " & boolean'image(dir_itemexists("a/b/c"));
    os := dir_workingdir("out2");
    report "cd " & dir_open_status'image(os) & " " & dir_workingdir;
    file_open(f, "r.txt", write_mode);
    write(l, string'("hello"));
    writeline(f, l);
    file_close(f);
    report "isfile " & boolean'image(dir_itemisfile("r.txt"));
    os := dir_workingdir("..");
    report "cd up " & dir_open_status'image(os) & " " & dir_workingdir;
    os := dir_workingdir("..");
    report "cd above " & dir_open_status'image(os) & " " &
      dir_workingdir;
    os := dir_open(d, "out2");
    report "items " & integer'image(d.items'length) & " " &
      d.items(2).all;
    dir_close(d);
    ds := dir_deletedir("a");
    report "rm nonempty " & dir_delete_status'image(ds);
    ds := dir_deletedir("a", true);
    report "rm recursive " & dir_delete_status'image(ds);
    ds := dir_deletedir("a");
    report "rm missing " & dir_delete_status'image(ds);
    ds := dir_deletedir("tb.vhd");
    report "rm file " & dir_delete_status'image(ds);
    ds := dir_deletedir(".");
    report "rm root " & dir_delete_status'image(ds);
    fs := dir_deletefile("data/zeta.txt");
    report "rmf " & file_delete_status'image(fs);
    fs := dir_deletefile("data/zeta.txt");
    report "rmf again " & file_delete_status'image(fs);
    fs := dir_deletefile("data");
    report "rmf dir " & file_delete_status'image(fs);
    dir_deletefile("out2/r.txt", fs);
    report "rmf written " & file_delete_status'image(fs) & " " &
      boolean'image(dir_itemexists("out2/r.txt"));
    file_open(f, "out2/s.txt", write_mode);
    write(l, string'("kept"));
    writeline(f, l);
    file_close(f);
    wait;
  end process;
end architecture;
`

// TestDirWrites: DIR_CREATEDIR, DIR_WORKINGDIR and the deletions work
// in the run's overlay; the project is never changed, what the run
// created is what the shell flushes to .gohard/run/<hash>/, and a
// path outside the sandbox is denied.
func TestDirWrites(t *testing.T) {
	ov, root := dirProject(t)
	r := dirSim(t, dirWriteSrc, "tb", ov, root)
	r.want(t,
		"mk status_ok",
		"mk again status_item_exists",
		"mk no parents status_error",
		"mk parents status_ok",
		"mk outside status_access_denied",
		"mk file status_item_exists",
		"isdir true isfile false exists true",
		"cd status_ok out2",
		"isfile true",
		"cd up status_ok .",
		"cd above status_access_denied .",
		"items 3 r.txt",
		"rm nonempty status_not_empty",
		"rm recursive status_ok",
		"rm missing status_no_directory",
		"rm file status_no_directory",
		"rm root status_access_denied",
		"rmf status_ok",
		"rmf again status_no_file",
		"rmf dir status_no_file",
		"rmf written status_ok false")
	if got, want := ov.Dirs(), []string{"out2"}; !slices.Equal(got,
		want) {
		t.Errorf("dirs %v, want %v", got, want)
	}
	var files []string
	for _, f := range ov.Files() {
		files = append(files, f.Path+"="+string(f.Data))
	}
	if want := []string{"out2/s.txt=kept\n"}; !slices.Equal(files,
		want) {
		t.Errorf("files %v, want %v", files, want)
	}
	// The project is untouched; the deleted file is only hidden.
	if b, err := ov.Base.ReadFile(filepath.FromSlash(
		"/work/data/zeta.txt")); err != nil || string(b) != "z\n" {
		t.Errorf("project file changed: %q %v", b, err)
	}
	if _, err := ov.ReadFile("data/zeta.txt"); err == nil {
		t.Error("deleted file still read")
	}
}

// TestDirNoDoor: without a file door the DIR_* natives see an empty,
// read-only project.
func TestDirNoDoor(t *testing.T) {
	src := `
use std.env.all;
entity tb is end entity;
architecture sim of tb is
begin
  process
    variable d : directory;
  begin
    report dir_open_status'image(dir_open(d, ".")) & " " &
      integer'image(d.items'length) & " " & d.name.all;
    report dir_open_status'image(dir_open(d, "x"));
    report dir_create_status'image(dir_createdir("x"));
    report boolean'image(dir_itemexists("x")) & " " &
      boolean'image(dir_itemisdir("."));
    report dir_workingdir;
    wait;
  end process;
end architecture;
`
	r := gSim(t, []gSource{{"t.vhd", src}}, "tb", EnvOptions{})
	r.want(t, "status_ok 2 .", "status_not_found", "status_access_denied",
		"false true", ".")
}

// TestDirDeterministic: the same run gives the same trace bytes and
// overlay contents every time, whatever the collector does (the
// DIR_* objects are heap objects too).
func TestDirDeterministic(t *testing.T) {
	var first []byte
	for i, gc := range []GCOptions{{}, {Every: 1}, {Off: true}} {
		ov, root := dirProject(t)
		r := kAnalyze(t, dirListSrc, "2019")
		b := eval.NewBudget(0)
		code := Interpreter(InterpOptions{FS: ov, GC: gc,
			Env: EnvOptions{Standard: token.Std2019, Root: root, FS: ov}})
		d := gElab(t, r, "tb", code, b)
		rec := trace.NewRecorder(nil)
		k, err := sim.New(d.Model, sim.Options{Budget: b,
			TimeBudget: 1_000_000_000_000, Writer: rec})
		if err != nil {
			t.Fatal(err)
		}
		k.RunAll(64, nil)
		tr, err := rec.Trace()
		if err != nil {
			t.Fatal(err)
		}
		text := trace.Encode(tr)
		if i == 0 {
			first = text
		} else if string(text) != string(first) {
			t.Errorf("gc %+v: trace differs", gc)
		}
	}
}

// TestDirSeparator: DIR_SEPARATOR is "/" on every host, supplied for
// the deferred constant without a package body.
func TestDirSeparator(t *testing.T) {
	r := kAnalyze(t, `
use std.env.all;
entity tb is end entity;
architecture sim of tb is begin end architecture;
`, "2019")
	var sep *sema.Decl
	for _, u := range r.Units {
		if string(u.Library) != "std" || string(u.Name) != "env" ||
			u.Region == nil {
			continue
		}
		for _, d := range u.Region.Decls {
			if d.Name == "dir_separator" {
				sep = d
			}
		}
	}
	d := gElab(t, r, "tb", Interpreter(InterpOptions{}), eval.NewBudget(0))
	if sep == nil {
		if lib, ok := d.r.Library("std"); ok {
			if u, ok := lib.Unit("env"); ok && u.Region != nil {
				for _, x := range u.Region.Decls {
					if x.Name == "dir_separator" {
						sep = x
					}
				}
			}
		}
	}
	if sep == nil {
		t.Fatal("no std.env.DIR_SEPARATOR")
	}
	w := newWorld(d, InterpOptions{})
	m := &machine{w: w, idx: -1, ev: d.Top.Evaluator().WithDynamic(nil),
		b: eval.NewBudget(0)}
	if !m.envConstant(sep) {
		t.Fatal("DIR_SEPARATOR not supplied")
	}
	s := w.shared[sharedKey{nil, sep}]
	if got := stringOf(s.v); got != "/" {
		t.Errorf("DIR_SEPARATOR = %q", got)
	}
	if s.v.T == nil || !s.v.T.Constrained() {
		t.Errorf("DIR_SEPARATOR layout %v", s.v.T)
	}
	if m.envConstant(&sema.Decl{Name: "dir_separator"}) {
		t.Error("a design constant taken for DIR_SEPARATOR")
	}
}

// TestDirConformance runs the DIR_* conformance cases with the
// detectors bypassed (testdata/conformance/std_env/dir_*.vhd): the
// status case gives NVC 1.23's messages exactly, the listing case its
// sorted listing.
func TestDirConformance(t *testing.T) {
	for _, c := range []struct {
		name, top string
		want      []string
	}{
		{"dir_status", "dir_status_tb", []string{
			"createdir: status_ok",
			"createdir again: status_item_exists",
			"createdir, no parent: status_error",
			"exists true, isdir true, isfile false, missing false",
			"workingdir: status_ok",
			"out.txt is a file: true",
			"back: status_ok, work_dir/out.txt is a file: true",
			"workingdir to a missing folder: status_not_found",
			"deletedir, not empty: status_not_empty",
			"deletefile: status_ok",
			"deletefile again: status_no_file",
			"deletedir: status_ok, exists false",
			"deletedir again: status_no_directory",
		}},
		{"dir_listing", "dir_listing_tb", []string{
			"DIR_OPEN: status_ok, name scratch, 6 items",
			"items: . .. Beta.txt alpha.txt mid zeta.txt",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("..", "..", "testdata",
				"conformance", "std_env", c.name+".vhd"))
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.FromSlash("/case")
			base := simenv.New(simenv.Options{Dir: root,
				Files: map[string][]byte{c.name + ".vhd": src}}).FS
			ov := env.NewOverlay(base, root)
			dirSim(t, string(src), c.top, ov, root).want(t, c.want...)
			var files []string
			for _, f := range ov.Files() {
				files = append(files, f.Path)
			}
			got := strings.Join(ov.Dirs(), " ") + " | " +
				strings.Join(files, " ")
			want := " | " // dir_status deletes what it made
			if c.name == "dir_listing" {
				want = "scratch scratch/mid | scratch/Beta.txt " +
					"scratch/alpha.txt scratch/zeta.txt"
			}
			if got != want {
				t.Errorf("overlay %q, want %q", got, want)
			}
		})
	}
}
