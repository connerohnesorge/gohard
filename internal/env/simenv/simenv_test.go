package simenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
)

// workload drives every door of e with a pseudo-random series of calls
// chosen by script and returns a transcript of what it saw.
func workload(e env.Env, script uint64, steps int) string {
	g := &env.SplitMix64{State: script}
	var log strings.Builder
	names := []string{"a", "b", "d/x", "d/y", "d/e/z", "d", "d/e"}
	pick := func() string { return names[env.Intn(g, len(names))] }
	note := func(format string, args ...any) {
		fmt.Fprintf(&log, format+"\n", args...)
	}
	for i := range steps {
		switch env.Intn(g, 12) {
		case 0:
			err := e.FS.WriteFile(pick(), fmt.Appendf(nil, "v%d", i), 0o644)
			note("write %v", err)
		case 1:
			b, err := e.FS.ReadFile(pick())
			note("read %q %v", b, err)
		case 2:
			note("mkdir %v", e.FS.MkdirAll(pick(), 0o755))
		case 3:
			note("remove %v", e.FS.Remove(pick()))
		case 4:
			note("rename %v", e.FS.Rename(pick(), pick()))
		case 5:
			ents, err := e.FS.ReadDir(pick())
			for _, d := range ents {
				note("  %s %v", d.Name(), d.IsDir())
			}
			note("readdir %v", err)
		case 6:
			r, err := e.FS.Open(pick())
			if err == nil {
				var b []byte
				b, err = io.ReadAll(r)
				r.Close()
				note("stream %q", b)
			}
			note("open %v", err)
		case 7:
			w, err := e.FS.Append(pick())
			if err == nil {
				_, err = w.Write([]byte("+"))
				w.Close()
			}
			note("append %v", err)
		case 8:
			tmp, err := e.FS.WriteTemp(".", ".t-", []byte("tmp"))
			if err == nil {
				err = e.FS.Rename(tmp, pick())
			}
			note("temp %q %v", tmp, err)
		case 9:
			note("rand %d order %v", e.Rand.Uint64(),
				e.Order.Permute(uint64(i), 5))
		case 10:
			var got []int
			e.Jobs.Run(4, 6, func(j int) { got = append(got, j) })
			note("jobs %v", got)
		case 11:
			e.Clock.Sleep(context.Background(), time.Duration(i)*time.Second)
			note("now %v", e.Clock.Now())
		}
	}
	return log.String()
}

var allFaults = Faults{ShortRead: 300, ReadError: 100, Vanish: 100,
	WriteError: 100}

func TestSameSeedSameBehaviour(t *testing.T) {
	for seed := range uint64(20) {
		run := func() (string, string) {
			s := New(Options{Seed: seed, Faults: allFaults})
			return workload(s.Env(), seed*7+1, 300), s.FS.Dump()
		}
		log1, dump1 := run()
		log2, dump2 := run()
		if log1 != log2 || dump1 != dump2 {
			t.Fatalf("seed %d: two runs differ", seed)
		}
	}
}

func TestSeedChangesFaultsAndOrder(t *testing.T) {
	seen := map[string]bool{}
	for seed := range uint64(8) {
		s := New(Options{Seed: seed, Faults: allFaults})
		seen[workload(s.Env(), 1, 300)] = true
	}
	if len(seen) < 2 {
		t.Fatal("the seed has no effect on the run")
	}
}

func TestZeroSeedKeepsOrder(t *testing.T) {
	e := New(Options{}).Env()
	for step := range uint64(10) {
		var got []int
		e.Jobs.Run(0, 9, func(i int) { got = append(got, i) })
		if !slices.Equal(got, []int{0, 1, 2, 3, 4, 5, 6, 7, 8}) {
			t.Fatalf("seed 0 jobs ran in order %v", got)
		}
		if p := e.Order.Permute(step, 9); !slices.IsSorted(p) {
			t.Fatalf("seed 0 order %v", p)
		}
	}
}

func TestJobsArePermutations(t *testing.T) {
	g := &env.SplitMix64{State: 3}
	for seed := range uint64(30) {
		e := New(Options{Seed: seed}).Env()
		for range 20 {
			n := env.Intn(g, 40)
			calls := make([]int, n)
			e.Jobs.Run(1, n, func(i int) { calls[i]++ })
			for i, c := range calls {
				if c != 1 {
					t.Fatalf("seed %d: job %d ran %d times", seed, i, c)
				}
			}
			in := make([]int, n)
			for i := range in {
				in[i] = i * i
			}
			out := env.Map(e.Jobs, 0, in, func(x int) int { return x + 1 })
			for i := range out {
				if out[i] != i*i+1 {
					t.Fatalf("Map lost canonical order: %v", out)
				}
			}
		}
	}
}

// TestFSMatchesOS runs the same fault-free workload on simenv and on a
// real directory and requires the same transcript, so the in-memory FS
// follows the operating system's rules.
func TestFSMatchesOS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the reference semantics are Linux's")
	}
	// Errors are compared by kind only: messages name different roots.
	kind := func(err error) string {
		switch {
		case err == nil:
			return "ok"
		case errors.Is(err, fs.ErrNotExist):
			return "notexist"
		}
		return "error"
	}
	for script := range uint64(150) {
		dir := t.TempDir()
		real := realenv.New(realenv.Options{Dir: dir})
		sim := New(Options{}).Env()
		a := workloadFS(real, script, kind)
		b := workloadFS(sim, script, kind)
		if a != b {
			t.Fatalf("script %d differs:\n%s", script, diffLines(a, b))
		}
	}
}

// workloadFS is the file part of workload with errors reduced by kind
// and temporary names hidden.
func workloadFS(e env.Env, script uint64, kind func(error) string) string {
	g := &env.SplitMix64{State: script}
	var log strings.Builder
	names := []string{"a", "b", "d/x", "d/y", "d/e/z", "d", "d/e", "d/e/w"}
	pick := func() string { return names[env.Intn(g, len(names))] }
	for i := range 200 {
		var line string
		switch env.Intn(g, 9) {
		case 0:
			err := e.FS.WriteFile(pick(), fmt.Appendf(nil, "v%d", i), 0o644)
			line = "write " + kind(err)
		case 1:
			b, err := e.FS.ReadFile(pick())
			line = fmt.Sprintf("read %q %s", b, kind(err))
		case 2:
			line = "mkdir " + kind(e.FS.MkdirAll(pick(), 0o755))
		case 3:
			n := pick()
			line = "remove " + n + " " + kind(e.FS.Remove(n))
		case 4:
			from, to := pick(), pick()
			line = fmt.Sprintf("rename %s %s %s", from, to,
				kind(e.FS.Rename(from, to)))
		case 5:
			ents, err := e.FS.ReadDir(pick())
			var ns []string
			for _, d := range ents {
				if !strings.HasPrefix(d.Name(), ".t-") {
					ns = append(ns, fmt.Sprint(d.Name(), d.IsDir()))
				}
			}
			line = fmt.Sprintf("readdir %v %s", ns, kind(err))
		case 6:
			fi, err := e.FS.Stat(pick())
			if err == nil {
				line = fmt.Sprint("stat ", fi.Name(), fi.IsDir())
				if !fi.IsDir() {
					line += fmt.Sprint(" ", fi.Size())
				}
			}
			line += " " + kind(err)
		case 7:
			w, err := e.FS.Append(pick())
			if err == nil {
				_, err = w.Write([]byte("+"))
				w.Close()
			}
			line = "append " + kind(err)
		case 8:
			tmp, err := e.FS.WriteTemp("d", ".t-", []byte("tmp"))
			if err == nil {
				err = e.FS.Rename(tmp, pick())
			}
			line = "temp " + kind(err)
		}
		log.WriteString(line + "\n")
	}
	return log.String()
}

func diffLines(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d\nos:  %s\nsim: %s", i+1, al[i],
				bl[i])
		}
	}
	return "lengths differ"
}

func TestAlwaysFaults(t *testing.T) {
	s := New(Options{
		Files:  map[string][]byte{"f": []byte("0123456789")},
		Faults: Faults{ReadError: Always, WriteError: Always},
	})
	f := s.FS
	if _, err := f.ReadFile("f"); !errors.Is(err, ErrInjected) {
		t.Fatalf("ReadFile = %v", err)
	}
	if _, err := f.Open("f"); !errors.Is(err, ErrInjected) {
		t.Fatalf("Open = %v", err)
	}
	if err := f.WriteFile("f", []byte("new content"), 0o644); !errors.Is(
		err, ErrInjected) {
		t.Fatalf("WriteFile = %v", err)
	}
	if _, err := f.WriteTemp(".", ".x-", nil); !errors.Is(err, ErrInjected) {
		t.Fatalf("WriteTemp = %v", err)
	}
	if err := f.MkdirAll("q", 0o755); !errors.Is(err, ErrInjected) {
		t.Fatalf("MkdirAll = %v", err)
	}
	if err := f.Rename("f", "g"); !errors.Is(err, ErrInjected) {
		t.Fatalf("Rename = %v", err)
	}
	// A failed WriteFile leaves a strict prefix behind.
	f.faults = Faults{}
	b, err := f.ReadFile("f")
	if err != nil || !bytes.HasPrefix([]byte("new content"), b) ||
		len(b) == len("new content") {
		t.Fatalf("after failed write: %q, %v", b, err)
	}
}

// TestAtomicReplaceUnderFaults is the P6 property behind WriteTemp and
// Rename: whatever fails, the target holds either the old or the new
// content, never a mixture.
func TestAtomicReplaceUnderFaults(t *testing.T) {
	old, new := []byte("old content"), []byte("brand new content")
	failures := 0
	for seed := range uint64(200) {
		s := New(Options{
			Seed:   seed,
			Files:  map[string][]byte{"t": old},
			Faults: Faults{WriteError: 500},
		})
		tmp, err := s.FS.WriteTemp(".", ".t-", new)
		if err == nil {
			err = s.FS.Rename(tmp, "t")
		}
		if err != nil {
			failures++
		}
		s.FS.faults = Faults{}
		got, _ := s.FS.ReadFile("t")
		if !bytes.Equal(got, old) && !bytes.Equal(got, new) {
			t.Fatalf("seed %d: target is %q", seed, got)
		}
		if err == nil && !bytes.Equal(got, new) {
			t.Fatalf("seed %d: success but target is %q", seed, got)
		}
	}
	if failures == 0 || failures == 200 {
		t.Fatalf("%d of 200 runs failed; the fault rate is not applied",
			failures)
	}
}

func TestShortReadsKeepContent(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefghij"), 100)
	short := 0
	for seed := range uint64(20) {
		s := New(Options{Seed: seed, Files: map[string][]byte{"f": data},
			Faults: Faults{ShortRead: 700}})
		r, err := s.FS.Open("f")
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		buf := make([]byte, 64)
		for {
			n, err := r.Read(buf)
			if n < len(buf) && err == nil {
				short++
			}
			got = append(got, buf[:n]...)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("seed %d: content changed", seed)
		}
	}
	if short == 0 {
		t.Fatal("no short read happened")
	}
}

func TestVanishMidWalk(t *testing.T) {
	files := map[string][]byte{}
	for i := range 50 {
		files[fmt.Sprintf("d/f%02d", i)] = nil
	}
	s := New(Options{Seed: 4, Files: files, Faults: Faults{Vanish: 300}})
	ents, err := s.FS.ReadDir("d")
	if err != nil || len(ents) != 50 {
		t.Fatalf("ReadDir = %d entries, %v", len(ents), err)
	}
	if !slices.IsSortedFunc(ents, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	}) {
		t.Fatal("ReadDir is not sorted")
	}
	gone := 0
	for _, d := range ents {
		_, err := s.FS.Stat("d/" + d.Name())
		if errors.Is(err, fs.ErrNotExist) {
			gone++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if gone == 0 || gone == 50 {
		t.Fatalf("%d of 50 files vanished", gone)
	}
}

func TestFailNext(t *testing.T) {
	s := New(Options{Files: map[string][]byte{"f": []byte("x")}})
	boom := errors.New("boom")
	s.FS.FailNext(OpRead, "f", boom)
	if _, err := s.FS.ReadFile("/work/f"); !errors.Is(err, boom) ||
		!errors.Is(err, ErrInjected) {
		t.Fatalf("scripted fault = %v", err)
	}
	if b, err := s.FS.ReadFile("f"); err != nil || string(b) != "x" {
		t.Fatalf("a scripted fault must fire once: %q %v", b, err)
	}
}

func TestSymlinks(t *testing.T) {
	f := New(Options{Files: map[string][]byte{"d/f": []byte("hi")}}).FS
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.Symlink("d", "l"))
	must(f.Symlink("f", "d/rel"))
	must(f.Symlink("loop2", "loop1"))
	must(f.Symlink("loop1", "loop2"))
	if b, err := f.ReadFile("l/rel"); err != nil || string(b) != "hi" {
		t.Fatalf("through links: %q %v", b, err)
	}
	if fi, err := f.Lstat("l"); err != nil ||
		fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("Lstat = %v %v", fi, err)
	}
	if fi, err := f.Stat("l"); err != nil || !fi.IsDir() {
		t.Fatalf("Stat = %v %v", fi, err)
	}
	if tg, err := f.Readlink("d/rel"); err != nil || tg != "f" {
		t.Fatalf("Readlink = %q %v", tg, err)
	}
	if _, err := f.Stat("loop1"); !errors.Is(err, ErrLoop) {
		t.Fatalf("loop = %v", err)
	}
	must(f.Symlink("new", "dangling"))
	must(f.WriteFile("dangling", []byte("via link"), 0o644))
	if b, _ := f.ReadFile("new"); string(b) != "via link" {
		t.Fatalf("write through dangling link made %q", b)
	}
}

func TestLoad(t *testing.T) {
	src := fstest.MapFS{
		"a.vhd":     {Data: []byte("entity a")},
		"lib/b.vhd": {Data: []byte("entity b")},
		"lib/c":     {Mode: fs.ModeSymlink, Data: []byte("b.vhd")},
	}
	f := New(Options{}).FS
	if err := f.Load("proj", src); err != nil {
		t.Fatal(err)
	}
	if b, err := f.ReadFile("proj/lib/c"); err != nil ||
		string(b) != "entity b" {
		t.Fatalf("loaded link: %q %v", b, err)
	}

	// A real directory, through os.DirFS.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "x"), 0o755)
	os.WriteFile(filepath.Join(dir, "x", "y.vhd"), []byte("real"), 0o644)
	if err := f.Load("/real", os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if b, err := f.ReadFile("/real/x/y.vhd"); err != nil ||
		string(b) != "real" {
		t.Fatalf("loaded real file: %q %v", b, err)
	}
}

func TestClock(t *testing.T) {
	c := NewClock(Epoch)
	var fired []string
	t3 := c.NewTimer(3 * time.Second)
	t1 := c.NewTimer(time.Second)
	t1b := c.NewTimer(time.Second)
	t2 := c.NewTimer(2 * time.Second)
	now := c.NewTimer(0)
	if got := <-now.C(); !got.Equal(Epoch) {
		t.Fatalf("zero timer fired at %v", got)
	}
	if !t2.Stop() || t2.Stop() {
		t.Fatal("Stop must report pending exactly once")
	}
	if c.Pending() != 3 {
		t.Fatalf("Pending = %d", c.Pending())
	}
	c.Advance(1500 * time.Millisecond)
	for name, tm := range map[string]env.Timer{"t1": t1, "t1b": t1b,
		"t3": t3} {
		select {
		case at := <-tm.C():
			fired = append(fired, fmt.Sprint(name, " ", at.Sub(Epoch)))
		default:
		}
	}
	slices.Sort(fired)
	if !slices.Equal(fired, []string{"t1 1s", "t1b 1s"}) {
		t.Fatalf("fired %v", fired)
	}
	if c.Now().Sub(Epoch) != 1500*time.Millisecond {
		t.Fatalf("Now = %v", c.Now())
	}
	if next, ok := c.Next(); !ok || next.Sub(Epoch) != 3*time.Second {
		t.Fatalf("Next = %v %v", next, ok)
	}
	if !c.Fire() || c.Fire() {
		t.Fatal("Fire must run the one pending timer")
	}
	if at := <-t3.C(); at.Sub(Epoch) != 3*time.Second || t3.Stop() {
		t.Fatalf("t3 fired at %v", at)
	}
	c.Advance(-time.Hour)
	c.AdvanceTo(Epoch)
	if c.Now().Sub(Epoch) != 3*time.Second {
		t.Fatal("the clock went backwards")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); err == nil ||
		c.Now().Sub(Epoch) != 3*time.Second {
		t.Fatal("Sleep on a done context must not advance")
	}
	if err := c.Sleep(context.Background(), time.Hour); err != nil ||
		c.Now().Sub(Epoch) != time.Hour+3*time.Second {
		t.Fatal("Sleep must advance the clock")
	}
}

// TestTimerOrderProperty: whatever the deadlines, timers fire in
// (deadline, creation) order and the clock never goes backwards.
func TestTimerOrderProperty(t *testing.T) {
	g := &env.SplitMix64{State: 11}
	for range 200 {
		c := NewClock(Epoch)
		n := 1 + env.Intn(g, 30)
		type tm struct {
			d   time.Duration
			idx int
			t   env.Timer
		}
		ts := make([]tm, n)
		for i := range ts {
			d := time.Duration(env.Intn(g, 10)) * time.Millisecond
			ts[i] = tm{d, i, c.NewTimer(d)}
		}
		for _, x := range ts {
			if x.d == 0 {
				if at := <-x.t.C(); !at.Equal(Epoch) {
					t.Fatal("zero timer fired late")
				}
			}
		}
		var order []int
		last := Epoch
		for c.Fire() {
			for _, x := range ts {
				select {
				case at := <-x.t.C():
					if at.Before(last) {
						t.Fatal("clock went backwards")
					}
					last = at
					order = append(order, x.idx)
				default:
				}
			}
		}
		want := slices.Clone(ts)
		slices.SortStableFunc(want, func(a, b tm) int {
			return int(a.d - b.d)
		})
		var wantIdx []int
		for _, x := range want {
			if x.d > 0 {
				wantIdx = append(wantIdx, x.idx)
			}
		}
		if !slices.Equal(order, wantIdx) {
			t.Fatalf("fired %v, want %v", order, wantIdx)
		}
	}
}

func TestExecutor(t *testing.T) {
	echo := func(_ context.Context, c env.Command) (env.Result, error) {
		return env.Result{ExitCode: 3,
			Stdout: []byte(strings.Join(c.Args, " "))}, nil
	}
	s := New(Options{
		Vars: map[string]string{"PATH": "/usr/bin:/opt/ghdl/bin"},
		Programs: map[string]Program{
			"/opt/ghdl/bin/ghdl": echo,
			"/usr/bin/ghdl":      echo,
			"/opt/ghdl/bin/nvc":  echo,
		},
	})
	x := s.Env().Executor
	for file, want := range map[string]string{
		"ghdl": "/usr/bin/ghdl", "nvc": "/opt/ghdl/bin/nvc",
		"/opt/ghdl/bin/ghdl": "/opt/ghdl/bin/ghdl",
	} {
		if got, err := x.LookPath(file); err != nil || got != want {
			t.Errorf("LookPath(%s) = %q, %v", file, got, err)
		}
	}
	if _, err := x.LookPath("gtkwave"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing program: %v", err)
	}
	res, err := x.Run(context.Background(), env.Command{
		Path: "/usr/bin/ghdl", Args: []string{"--version"}})
	if err != nil || res.ExitCode != 3 || string(res.Stdout) != "--version" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if err := x.Start(env.Command{Path: "/no/such"}); err == nil {
		t.Fatal("Start of a missing program must fail")
	}
	calls := s.Executor.Calls()
	if len(calls) != 2 || calls[0].Start || !calls[1].Start {
		t.Fatalf("Calls = %+v", calls)
	}
	// An explicit Path wins over Vars.
	s2 := New(Options{Path: []string{"/opt/ghdl/bin"},
		Vars:     map[string]string{"PATH": "/usr/bin"},
		Programs: map[string]Program{"/opt/ghdl/bin/ghdl": echo}})
	if got, _ := s2.Env().Executor.LookPath("ghdl"); got !=
		"/opt/ghdl/bin/ghdl" {
		t.Fatalf("Options.Path ignored: %q", got)
	}
}

func TestTransportAndGetenv(t *testing.T) {
	s := New(Options{
		Stdin: [][]byte{[]byte("Content-Length: 2\r\n\r\n"),
			[]byte("{}")},
		Vars: map[string]string{"HOME": "/home/sim"},
	})
	e := s.Env()
	buf := make([]byte, 100)
	n, _ := e.Transport.Stdin.Read(buf)
	if string(buf[:n]) != "Content-Length: 2\r\n\r\n" {
		t.Fatalf("first read %q", buf[:n])
	}
	rest, _ := io.ReadAll(e.Transport.Stdin)
	if string(rest) != "{}" {
		t.Fatalf("rest %q", rest)
	}
	fmt.Fprint(e.Transport.Stdout, "out")
	fmt.Fprint(e.Transport.Stderr, "err")
	if s.Stdout.String() != "out" || s.Stderr.String() != "err" {
		t.Fatal("output not recorded")
	}
	if v, ok := e.Getenv("HOME"); !ok || v != "/home/sim" {
		t.Fatal("Getenv HOME")
	}
	if _, ok := e.Getenv("PATH"); ok {
		t.Fatal("unset variable reported set")
	}
	if wd, _ := e.FS.Getwd(); wd != DefaultDir {
		t.Fatalf("Getwd = %q", wd)
	}
}

func TestRandIsSeeded(t *testing.T) {
	a, b := New(Options{Seed: 5}).Env(), New(Options{Seed: 5}).Env()
	c := New(Options{Seed: 6}).Env()
	same, diff := true, false
	for range 10 {
		x, y, z := a.Rand.Uint64(), b.Rand.Uint64(), c.Rand.Uint64()
		same = same && x == y
		diff = diff || x != z
	}
	if !same || !diff {
		t.Fatal("Rand must be a function of the seed")
	}
	seed, random, err := env.ParseSeed(env.SeedRandom, a.Rand)
	if err != nil || !random || seed == 0 {
		t.Fatalf("ParseSeed(random) = %d %v %v", seed, random, err)
	}
}

// jobOrder records the order in which e's runner calls n jobs.
func jobOrder(e env.Env, n int) []int {
	var got []int
	e.Jobs.Run(0, n, func(i int) { got = append(got, i) })
	return got
}

// TestJobsOrderFollowsSeed: at a nonzero seed the job order is not index
// order, changes from one Run to the next, and is a function of the seed
// and the call number, so order-dependent callers are exposed (#29).
func TestJobsOrderFollowsSeed(t *testing.T) {
	ident := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	for seed := uint64(1); seed <= 10; seed++ {
		a, b := New(Options{Seed: seed}).Env(), New(Options{Seed: seed}).Env()
		var orders [][]int
		shuffled := false
		for range 6 {
			o := jobOrder(a, 10)
			if !slices.Equal(o, jobOrder(b, 10)) {
				t.Fatalf("seed %d: same seed, different job order", seed)
			}
			shuffled = shuffled || !slices.Equal(o, ident)
			orders = append(orders, o)
		}
		if !shuffled {
			t.Fatalf("seed %d: jobs always ran in index order", seed)
		}
		changed := false
		for _, o := range orders[1:] {
			changed = changed || !slices.Equal(o, orders[0])
		}
		if !changed {
			t.Fatalf("seed %d: every Run used order %v", seed, orders[0])
		}
	}
	x := jobOrder(New(Options{Seed: 1}).Env(), 10)
	y := jobOrder(New(Options{Seed: 2}).Env(), 10)
	if slices.Equal(x, y) {
		t.Fatalf("seeds 1 and 2 give the same job order %v", x)
	}
}

// TestJobsPanicLowestIndex: whatever the shuffled order, the runner
// re-raises the panic of the lowest panicking index (env.Jobs).
func TestJobsPanicLowestIndex(t *testing.T) {
	for seed := range uint64(30) {
		e := New(Options{Seed: seed}).Env()
		for _, lowest := range []int{0, 4, 11} {
			ran := 0
			got := func() (r any) {
				defer func() { r = recover() }()
				e.Jobs.Run(0, 12, func(i int) {
					ran++
					if i >= lowest && i%2 == lowest%2 {
						panic(i)
					}
				})
				return nil
			}()
			if got != lowest || ran != 12 {
				t.Fatalf("seed %d: recovered %v after %d jobs, want %d "+
					"after 12", seed, got, ran, lowest)
			}
		}
	}
}

// faultPattern reads one file 64 times at a 50% read-error rate and
// returns which reads failed.
func faultPattern(seed uint64) string {
	s := New(Options{Seed: seed, Files: map[string][]byte{"f": []byte("x")},
		Faults: Faults{ReadError: Always / 2}})
	var b strings.Builder
	for range 64 {
		if _, err := s.FS.ReadFile("f"); err != nil {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

// TestStreamsAreSeparate: Rand, the fault stream and the job order are
// separate streams of one seed. Drawing from one never moves another,
// the fault decisions are not the Rand stream's, and the first values
// are pinned, so a repro seed replays the same run across versions.
func TestStreamsAreSeparate(t *testing.T) {
	for seed := range uint64(16) {
		r := New(Options{Seed: seed}).Env().Rand
		var fromRand strings.Builder
		for range 64 {
			if env.Intn(r, int(Always)) < int(Always/2) {
				fromRand.WriteByte('1')
			} else {
				fromRand.WriteByte('0')
			}
		}
		if p := faultPattern(seed); p == fromRand.String() {
			t.Fatalf("seed %d: faults follow the Rand stream %s", seed, p)
		}
		raw := env.SplitMix64{State: seed}
		if New(Options{Seed: seed}).Env().Rand.Uint64() == raw.Uint64() {
			t.Fatalf("seed %d: Rand is the unsalted seed stream", seed)
		}
		// Drawing from Rand does not move the faults or the job order.
		s := New(Options{Seed: seed, Files: map[string][]byte{
			"f": []byte("x")}, Faults: Faults{ReadError: Always / 2}})
		for range 100 {
			s.Env().Rand.Uint64()
		}
		var b strings.Builder
		for range 64 {
			if _, err := s.FS.ReadFile("f"); err != nil {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		if b.String() != faultPattern(seed) {
			t.Fatalf("seed %d: drawing from Rand moved the faults", seed)
		}
	}
	const (
		wantRand  = uint64(0x26cfde794eb9fd22)
		wantFault = "01000010011110010100110000011110" +
			"10101000011100010001101111101100"
		wantJobs = "[7 3 5 0 6 4 1 2]"
	)
	gotRand := New(Options{Seed: 1}).Env().Rand.Uint64()
	gotJobs := fmt.Sprint(jobOrder(New(Options{Seed: 1}).Env(), 8))
	if gotRand != wantRand || faultPattern(1) != wantFault ||
		gotJobs != wantJobs {
		t.Errorf("seed 1 streams: rand %#x faults %s jobs %s", gotRand,
			faultPattern(1), gotJobs)
	}
}

// TestRenameIntoOwnSubtreeViaLink: moving a directory below itself is
// refused even when the destination is reached through a symbolic link,
// as the operating system does, and nothing is lost.
func TestRenameIntoOwnSubtreeViaLink(t *testing.T) {
	f := New(Options{Files: map[string][]byte{
		"/w/a/sub/f": []byte("keep")}}).FS
	if err := f.Symlink("/w/a/sub", "/w/link"); err != nil {
		t.Fatal(err)
	}
	for _, dst := range []string{"/w/link/moved",
		"/w/a/sub/x", "/w/a/x"} {
		if err := f.Rename("/w/a", dst); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Rename(/w/a, %s) = %v, want ErrInvalid", dst, err)
		}
	}
	if b, err := f.ReadFile("/w/a/sub/f"); err != nil ||
		string(b) != "keep" {
		t.Fatalf("data lost: %q %v", b, err)
	}
	if runtime.GOOS != "linux" {
		return
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "a", "sub"),
		filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	err := os.Rename(filepath.Join(dir, "a"),
		filepath.Join(dir, "link", "moved"))
	if err == nil {
		t.Fatal("the OS allowed the rename; simenv must match it")
	}
}

// kindPattern records 64 fault decisions of one kind at rate 500 for
// seed: '1' where the fault fired.
func kindPattern(seed uint64, kind string) string {
	var b strings.Builder
	mark := func(hit bool) {
		if hit {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	const n, rate = 64, Always / 2
	switch kind {
	case "ShortRead":
		s := New(Options{Seed: seed, Files: map[string][]byte{
			"f": bytes.Repeat([]byte("ab"), 2*n)},
			Faults: Faults{ShortRead: rate}})
		r, _ := s.FS.Open("f")
		buf := make([]byte, 2)
		for range n {
			k, _ := r.Read(buf)
			mark(k == 1)
		}
	case "Vanish":
		files := map[string][]byte{}
		for i := range n {
			files[fmt.Sprintf("d/f%02d", i)] = nil
		}
		s := New(Options{Seed: seed, Files: files,
			Faults: Faults{Vanish: rate}})
		ents, _ := s.FS.ReadDir("d")
		for _, d := range ents {
			_, err := s.FS.Stat("d/" + d.Name())
			mark(err != nil)
		}
	case "WriteError":
		s := New(Options{Seed: seed, Faults: Faults{WriteError: rate}})
		for range n {
			mark(s.FS.WriteFile("f", []byte("x"), 0o644) != nil)
		}
	case "ReadError":
		return faultPattern(seed)
	}
	return b.String()
}

// TestFaultsFollowSeed: every fault kind is drawn from the seed. At a
// rate of one half it fires on some opportunities and not on others,
// the pattern changes with the seed, and the seed-1 patterns are
// pinned. A fault that fired every time, or ignored the seed, fails.
func TestFaultsFollowSeed(t *testing.T) {
	// One fault stream serves every kind, so each kind's first 64
	// decisions for seed 1 are the same pinned sequence.
	const seed1 = "01000010011110010100110000011110" +
		"10101000011100010001101111101100"
	want := map[string]string{"ShortRead": seed1, "Vanish": seed1,
		"WriteError": seed1, "ReadError": seed1}
	for _, kind := range slices.Sorted(maps.Keys(want)) {
		seen := map[string]bool{}
		for seed := range uint64(16) {
			p := kindPattern(seed, kind)
			if len(p) != 64 || !strings.Contains(p, "0") ||
				!strings.Contains(p, "1") {
				t.Fatalf("%s seed %d: pattern %q", kind, seed, p)
			}
			if p != kindPattern(seed, kind) {
				t.Fatalf("%s seed %d: not a pure function", kind, seed)
			}
			seen[p] = true
		}
		if len(seen) < 15 {
			t.Errorf("%s: only %d patterns over 16 seeds", kind,
				len(seen))
		}
		if got := kindPattern(1, kind); got != want[kind] {
			t.Errorf("%s seed 1: %s, want %s", kind, got, want[kind])
		}
	}
}

// TestDotDotAfterLinkMatchesOS: a name with ".." after a symbolic link
// opens the same file in simenv and realenv, given absolute or relative.
// Both envs clean names lexically (lnk/../x is x), so a replay in
// simenv sees what the real run saw.
func TestDotDotAfterLinkMatchesOS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs symbolic links")
	}
	dir := t.TempDir()
	for name, data := range map[string]string{"a/x": "real",
		"x": "lexical", "a/b/y": "y"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a/b", filepath.Join(dir, "lnk")); err != nil {
		t.Fatal(err)
	}
	real := realenv.New(realenv.Options{Dir: dir}).FS
	s := New(Options{Dir: "/work", Files: map[string][]byte{
		"a/x": []byte("real"), "x": []byte("lexical"),
		"a/b/y": []byte("y"),
	}})
	if err := s.FS.Symlink("a/b", "/work/lnk"); err != nil {
		t.Fatal(err)
	}
	sim := s.Env().FS
	for _, rel := range []string{"lnk/../x", "lnk/./../x", "lnk/y",
		"a/b/../x", "lnk/../a/x", "lnk/../../x", "./lnk/../x"} {
		got := func(fsys env.FS, root string) string {
			b, err := fsys.ReadFile(rel)
			ab, aerr := fsys.ReadFile(root + "/" + rel)
			return fmt.Sprintf("%q %v | %q %v", b, err != nil, ab,
				aerr != nil)
		}
		if r, m := got(real, dir), got(sim, "/work"); r != m {
			t.Errorf("%s: realenv %s, simenv %s", rel, r, m)
		}
	}
}

// TestChmod shows Chmod replaces only the permission bits, follows a
// symbolic link, and fails on a missing file or a scripted fault.
func TestChmod(t *testing.T) {
	s := New(Options{Files: map[string][]byte{"a": []byte("x")}})
	f := s.FS
	if err := f.Symlink("a", "l"); err != nil {
		t.Fatal(err)
	}
	if err := f.Chmod("l", 0o4755|fs.ModeDir); err != nil {
		t.Fatal(err)
	}
	fi, err := f.Lstat("a")
	if err != nil || fi.Mode() != 0o755 {
		t.Fatalf("mode %v, %v; want -rwxr-xr-x", fi.Mode(), err)
	}
	if fi, _ := f.Lstat("l"); fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("the link itself changed: %v", fi.Mode())
	}
	if err := f.Chmod("missing", 0o600); !errors.Is(err,
		fs.ErrNotExist) {
		t.Fatalf("chmod of a missing file: %v", err)
	}
	f.FailNext(OpChmod, "a", nil)
	if err := f.Chmod("a", 0o600); !errors.Is(err, ErrInjected) {
		t.Fatalf("scripted chmod fault: %v", err)
	}
	if fi, _ := f.Lstat("a"); fi.Mode() != 0o755 {
		t.Fatalf("a failed chmod changed the mode: %v", fi.Mode())
	}
}
