package workspace

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
)

func discover(m *memFS, paths ...string) Result {
	return Discover(m, Options{Root: rootDir(), Dir: m.cwd, Paths: paths})
}

func paths(r Result) []string {
	var out []string
	for _, f := range r.Files {
		out = append(out, f.Path)
	}
	return out
}

func codes(r Result) []string {
	var out []string
	for _, d := range r.Diagnostics {
		out = append(out, d.Path+" "+string(d.Code))
	}
	return out
}

func TestIsVHDL(t *testing.T) {
	yes := []string{"a.vhd", "a.VHD", "a.Vhdl", "dir/x.vHdL", `d\x.vhd`,
		".vhd"}
	no := []string{"a.vh", "a.vhdx", "a.vhd.txt", "vhd", "a.v", ""}
	for _, n := range yes {
		if !IsVHDL(n) {
			t.Errorf("IsVHDL(%q) = false", n)
		}
	}
	for _, n := range no {
		if IsVHDL(n) {
			t.Errorf("IsVHDL(%q) = true", n)
		}
	}
}

func TestDiscoverBasics(t *testing.T) {
	m := newMemFS(rootDir())
	m.file(ospath("b.vhd"), "b")
	m.file(ospath("A.VHDL"), "a")
	m.file(ospath("notes.txt"), "")
	m.file(ospath("src/c.Vhd"), "c")
	m.file(ospath("src/sub/d.vhdl"), "d")
	m.file(ospath(".git/x.vhd"), "")
	m.file(ospath("src/.hidden/y.vhd"), "")
	m.file(ospath(".gohard/z.vhd"), "")
	r := discover(m)
	want := []string{"A.VHDL", "b.vhd", "src/c.Vhd", "src/sub/d.vhdl"}
	if got := paths(r); !slices.Equal(got, want) {
		t.Errorf("files = %q, want %q", got, want)
	}
	if len(r.Diagnostics) != 0 {
		t.Errorf("diagnostics = %q", codes(r))
	}
	// A dot directory named on the command line is walked.
	if got := paths(discover(m, "src/.hidden")); !slices.Equal(got,
		[]string{"src/.hidden/y.vhd"}) {
		t.Errorf("explicit dot dir: %q", got)
	}
	// Explicit non-VHDL files are skipped, explicit VHDL files kept.
	if got := paths(discover(m, "notes.txt", "b.vhd")); !slices.Equal(
		got, []string{"b.vhd"}) {
		t.Errorf("explicit files: %q", got)
	}
}

func TestDiscoverRelativeToRoot(t *testing.T) {
	m := newMemFS(rootDir())
	m.file(ospath("src/a.vhd"), "")
	m.file(ospath("lib/b.vhd"), "")
	m.cwd = ospath("src")
	r := discover(m, ".", "../lib/b.vhd", ospath("src/a.vhd"))
	if got := paths(r); !slices.Equal(got,
		[]string{"lib/b.vhd", "src/a.vhd"}) {
		t.Errorf("files = %q", got)
	}
}

func TestDiscoverDuplicatesCountOnce(t *testing.T) {
	m := newMemFS(rootDir())
	m.file(ospath("rtl/a.vhd"), "")
	m.symlink(ospath("alias.vhd"), "rtl/a.vhd")
	m.symlink(ospath("rtl2"), "rtl")
	m.symlink(ospath("abs"), ospath("rtl"))
	r := discover(m, "rtl/a.vhd", "./rtl/a.vhd", "rtl//a.vhd", "rtl",
		"alias.vhd", "rtl2", "abs/a.vhd", ".")
	if got := paths(r); !slices.Equal(got, []string{"abs/a.vhd"}) {
		t.Errorf("files = %q, want the smallest alias once", got)
	}
	if got := paths(discover(m)); !slices.Equal(got,
		[]string{"abs/a.vhd"}) {
		t.Errorf("walk files = %q", got)
	}
}

func TestDiscoverMissingPaths(t *testing.T) {
	m := newMemFS(rootDir())
	m.file(ospath("a.vhd"), "")
	m.symlink(ospath("dangling.vhd"), "nowhere.vhd")
	r := discover(m, "missing.vhd", "a.vhd", "gone/")
	if got := paths(r); !slices.Equal(got, []string{"a.vhd"}) {
		t.Errorf("files = %q", got)
	}
	want := []string{" cli/missing-path", " cli/missing-path"}
	if got := codes(r); !slices.Equal(got, want) {
		t.Errorf("diagnostics = %q, want %q", got, want)
	}
	if diag.ExitCode(r.Diagnostics, false, diag.NoSignal) != 2 {
		t.Error("missing paths must exit 2")
	}
	// A dangling VHDL link found in a walk cannot be read.
	r = discover(m)
	if got := codes(r); !slices.Equal(got,
		[]string{"dangling.vhd workspace/unreadable"}) {
		t.Errorf("walk diagnostics = %q", got)
	}
}

func TestDiscoverSymlinkLoops(t *testing.T) {
	m := newMemFS(rootDir())
	m.file(ospath("src/a.vhd"), "")
	m.symlink(ospath("src/up"), "..")             // back to the root
	m.symlink(ospath("self"), "self")             // a link to itself
	m.symlink(ospath("ping"), "pong")             // a two-link cycle
	m.symlink(ospath("pong"), "ping")             //
	m.symlink(ospath("src/again"), ospath("src")) // to its own parent
	r := discover(m)
	if got := paths(r); !slices.Equal(got, []string{"src/a.vhd"}) {
		t.Errorf("files = %q", got)
	}
	want := []string{
		"ping workspace/symlink-loop",
		"pong workspace/symlink-loop",
		"self workspace/symlink-loop",
		"src/again workspace/symlink-loop",
		"src/up workspace/symlink-loop",
	}
	if got := codes(r); !slices.Equal(got, want) {
		t.Errorf("diagnostics = %q, want %q", got, want)
	}
	if diag.ExitCode(r.Diagnostics, false, diag.NoSignal) != 2 {
		t.Error("a symlink loop must exit 2")
	}
	r = discover(m, "self/x.vhd")
	if got := codes(r); !slices.Equal(got,
		[]string{"self/x.vhd workspace/symlink-loop"}) {
		t.Errorf("arg through a loop: %q", got)
	}
}

func TestDiscoverTooLarge(t *testing.T) {
	m := newMemFS(rootDir())
	m.sized(ospath("big.vhd"), MaxFileSize+1)
	m.sized(ospath("edge.vhd"), MaxFileSize)
	r := discover(m)
	if got := paths(r); !slices.Equal(got,
		[]string{"big.vhd", "edge.vhd"}) {
		t.Fatalf("files = %q", got)
	}
	if !r.Files[0].TooLarge || r.Files[1].TooLarge {
		t.Errorf("TooLarge = %v, %v", r.Files[0].TooLarge,
			r.Files[1].TooLarge)
	}
	if got := codes(r); !slices.Equal(got,
		[]string{"big.vhd workspace/file-too-large"}) {
		t.Errorf("diagnostics = %q", got)
	}
	d := r.Diagnostics[0]
	if d.Severity != diag.Warning ||
		diag.ExitCode(r.Diagnostics, false, diag.NoSignal) != 0 ||
		diag.ExitCode(r.Diagnostics, true, diag.NoSignal) != 1 {
		t.Errorf("too large must be a warning that fails --strict")
	}
	want := "file not parsed: size 10000001 bytes exceeds the limit " +
		"of 10000000 bytes (10 MB)"
	if d.Message != want {
		t.Errorf("message = %q, want %q", d.Message, want)
	}
	srcs, ds := Read(env.Sequential{}, 0, m, r.Files, 0)
	if len(srcs) != 1 || srcs[0].Path != "edge.vhd" || len(ds) != 0 {
		t.Errorf("Read = %d sources, %v", len(srcs), ds)
	}
	if m.reads[ospath("big.vhd")] != 0 {
		t.Error("a too-large file was read")
	}
}

func TestDiscoverUnreadable(t *testing.T) {
	m := newMemFS(rootDir())
	m.file(ospath("ok/a.vhd"), "")
	m.file(ospath("locked/b.vhd"), "")
	m.denied[ospath("locked/b.vhd")] = true
	r := discover(m)
	if got := codes(r); !slices.Equal(got,
		[]string{"locked/b.vhd workspace/unreadable"}) {
		t.Errorf("diagnostics = %q", got)
	}
	if !strings.HasSuffix(r.Diagnostics[0].Message,
		": permission denied") ||
		strings.Contains(r.Diagnostics[0].Message, rootDir()) {
		t.Errorf("message = %q", r.Diagnostics[0].Message)
	}
}

// reverse runs jobs backwards, to show results never depend on order.
type reverse struct{}

func (reverse) Run(_, n int, job func(int)) {
	for i := n - 1; i >= 0; i-- {
		job(i)
	}
}

func TestReadOrderIndependent(t *testing.T) {
	m := newMemFS(rootDir())
	for i := range 20 {
		m.file(ospath(fmt.Sprintf("f%02d.vhd", i)), fmt.Sprint(i))
	}
	m.denied[ospath("f07.vhd")] = true
	r := discover(m)
	s1, d1 := Read(env.Sequential{}, 1, m, r.Files, 0)
	s2, d2 := Read(reverse{}, 16, m, r.Files, 0)
	if fmt.Sprint(s1, d1) != fmt.Sprint(s2, d2) {
		t.Error("Read depends on the job order")
	}
	if len(s1) != 19 || len(d1) != 0 {
		// f07 is denied at discovery already.
		t.Errorf("got %d sources, %d diagnostics", len(s1), len(d1))
	}
}

// TestReadPanicDeterministic: when several reads panic, the panic of
// the first file is the one raised, whatever runner, job order or limit
// is used (the env.Jobs contract).
func TestReadPanicDeterministic(t *testing.T) {
	m := newMemFS(rootDir())
	for i := range 8 {
		m.file(ospath(fmt.Sprintf("f%d.vhd", i)), "")
	}
	r := discover(m)
	pfs := panicFS{m}
	runners := []env.Jobs{env.Sequential{},
		realenv.New(realenv.Options{Workers: 16}).Jobs}
	for seed := range uint64(8) {
		runners = append(runners, simenv.New(simenv.Options{
			Seed: seed}).Jobs)
	}
	for _, jobs := range runners {
		func() {
			defer func() {
				if v := recover(); v != "panic f0.vhd" {
					t.Errorf("%T: panic %v", jobs, v)
				}
			}()
			Read(jobs, 0, pfs, r.Files, 0)
		}()
	}
}

type panicFS struct{ *memFS }

func (panicFS) ReadFile(name string) ([]byte, error) {
	panic("panic " + filepath.Base(name))
}

// randomTree builds a random project with VHDL and other files, dot
// directories and symbolic links (relative, absolute, dangling and
// looping) from seed.
type entry struct {
	kind         byte // 'd', 'f' or 'l'
	path, target string
}

func randomTree(seed uint64) []entry {
	r := &env.SplitMix64{State: seed}
	names := []string{"a.vhd", "B.VHDL", "c.txt", "d.Vhd", "e", ".h",
		"f.vhdl", "g.vhd.bak"}
	dirs := []string{"."}
	var es []entry
	for range 4 + env.Intn(r, 25) {
		parent := dirs[env.Intn(r, len(dirs))]
		name := names[env.Intn(r, len(names))]
		p := path.Join(parent, name)
		if slices.ContainsFunc(es, func(e entry) bool {
			return e.path == p || strings.HasPrefix(p, e.path+"/") &&
				e.kind != 'd'
		}) {
			continue
		}
		switch env.Intn(r, 3) {
		case 0:
			es = append(es, entry{kind: 'd', path: p})
			dirs = append(dirs, p)
		case 1:
			es = append(es, entry{kind: 'f', path: p})
		default:
			var tgt string
			switch env.Intn(r, 4) {
			case 0:
				tgt = "missing"
			case 1:
				tgt = "/" + dirs[env.Intn(r, len(dirs))] // absolute
			default:
				tgt = strings.Repeat("../", strings.Count(p, "/")) +
					dirs[env.Intn(r, len(dirs))]
				if env.Intn(r, 2) == 0 && len(es) > 0 {
					tgt = strings.Repeat("../",
						strings.Count(p, "/")) +
						es[env.Intn(r, len(es))].path
				}
			}
			es = append(es, entry{kind: 'l', path: p, target: tgt})
		}
	}
	return es
}

func buildMem(es []entry) *memFS {
	m := newMemFS(rootDir())
	for _, e := range es {
		switch e.kind {
		case 'd':
			m.mkdir(ospath(e.path))
		case 'f':
			m.file(ospath(e.path), e.path)
		case 'l':
			tgt := e.target
			if strings.HasPrefix(tgt, "/") {
				tgt = ospath(tgt)
			}
			m.symlink(ospath(e.path), tgt)
		}
	}
	return m
}

func summary(r Result) string {
	var b strings.Builder
	for _, f := range r.Files {
		fmt.Fprintf(&b, "%s %d %v\n", f.Path, f.Size, f.TooLarge)
	}
	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "%s %s %s\n", d.Path, d.Code, d.Message)
	}
	return b.String()
}

// TestDiscoverProperties checks the invariants on random trees: sorted
// unique paths, only VHDL names, no dot directory in a walk, one entry
// per real file, termination with loops, and independence from the
// order and repetition of the command-line paths.
func TestDiscoverProperties(t *testing.T) {
	seen := map[diag.Code]int{}
	files := 0
	defer func() {
		// The generator must reach every interesting case.
		if files == 0 || seen[diag.WorkspaceSymlinkLoop] == 0 ||
			seen[diag.WorkspaceUnreadable] == 0 ||
			seen[diag.CLIMissingPath] == 0 {
			t.Errorf("weak generator: %d files, %v", files, seen)
		}
	}()
	for seed := range uint64(500) {
		es := randomTree(seed)
		m := buildMem(es)
		r := discover(m)
		files += len(r.Files)
		for _, d := range r.Diagnostics {
			seen[d.Code]++
		}
		real := map[string]bool{}
		for i, f := range r.Files {
			if i > 0 && r.Files[i-1].Path >= f.Path {
				t.Fatalf("seed %d: not sorted: %q", seed, paths(r))
			}
			if !IsVHDL(f.Path) || real[f.Name] {
				t.Fatalf("seed %d: bad file %+v", seed, f)
			}
			real[f.Name] = true
			for _, part := range strings.Split(path.Dir(f.Path),
				"/") {
				if strings.HasPrefix(part, ".") && part != "." {
					t.Fatalf("seed %d: walked a dot dir: %s",
						seed, f.Path)
				}
			}
		}
		if !slices.IsSortedFunc(r.Diagnostics, diag.Compare) {
			t.Fatalf("seed %d: diagnostics not sorted", seed)
		}
		if got := discover(m, "."); summary(got) != summary(r) {
			t.Fatalf("seed %d: '.' differs from no paths:\n%s\n%s",
				seed, summary(got), summary(r))
		}
		var args []string
		for _, e := range es {
			if !strings.Contains(e.path, ".h") {
				args = append(args, e.path)
			}
		}
		args = append(args, "nope.vhd")
		a := discover(m, args...)
		perm := env.Shuffle(seed+1, 0, len(args))
		var shuffled []string
		for _, i := range perm {
			shuffled = append(shuffled, args[i])
		}
		shuffled = append(shuffled, args...) // repeats count once
		for _, d := range a.Diagnostics {
			seen[d.Code]++
		}
		if b := discover(m, shuffled...); summary(a) != summary(b) {
			t.Fatalf("seed %d: path order matters:\n%s\nvs\n%s",
				seed, summary(a), summary(b))
		}
	}
}

// TestDiscoverMatchesOS builds the same random trees on disk and checks
// that discovery through realenv agrees with the in-memory model.
func TestDiscoverMatchesOS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	for seed := range uint64(60) {
		es := randomTree(seed)
		dir := t.TempDir()
		for _, e := range es {
			p := filepath.Join(dir, filepath.FromSlash(e.path))
			var err error
			switch e.kind {
			case 'd':
				err = os.MkdirAll(p, 0o755)
			case 'f':
				err = os.WriteFile(p, []byte(e.path), 0o644)
			case 'l':
				tgt := e.target
				if strings.HasPrefix(tgt, "/") {
					tgt = filepath.Join(dir, tgt)
				}
				err = os.Symlink(tgt, p)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		e := realenv.New(realenv.Options{Dir: dir})
		got := Discover(e.FS, Options{Root: dir, Dir: dir})
		want := discover(buildMem(es))
		if summary(got) != summary(want) {
			t.Fatalf("seed %d: OS:\n%s\nmodel:\n%s", seed,
				summary(got), summary(want))
		}
	}
}

// TestDiscoverHardLinks: two hard links to one file are one file
// (#20: duplicate paths count once), listed under the smaller path,
// whatever order the paths are given in. Equal-size distinct files and
// a symbolic link are unaffected.
func TestDiscoverHardLinks(t *testing.T) {
	dir := t.TempDir()
	w := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body),
			0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("b.vhd", "entity e is end;")
	w("c.vhd", "entity f is end;") // same size, different file
	if err := os.Link(filepath.Join(dir, "b.vhd"),
		filepath.Join(dir, "a.vhd")); err != nil {
		t.Skip("hard links not supported: ", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "b.vhd"),
		filepath.Join(dir, "sub", "z.vhd")); err != nil {
		t.Fatal(err)
	}
	e := realenv.New(realenv.Options{Dir: dir})
	want := []string{"a.vhd", "c.vhd"}
	for _, args := range [][]string{nil, {"sub/z.vhd", "b.vhd", "a.vhd",
		"c.vhd"}, {"c.vhd", "b.vhd", "sub", "a.vhd"}, {"."}} {
		got := Discover(e.FS, Options{Root: dir, Dir: dir, Paths: args})
		if !slices.Equal(paths(got), want) || len(got.Diagnostics) != 0 {
			t.Errorf("%v: files %v diags %v, want %v", args,
				paths(got), codes(got), want)
		}
	}
	got := Discover(e.FS, Options{Root: dir, Dir: dir,
		Paths: []string{"sub/z.vhd", "b.vhd"}})
	if !slices.Equal(paths(got), []string{"b.vhd"}) {
		t.Errorf("smallest of the given paths: %v", paths(got))
	}
}
