package main

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env/realenv"
)

const sample = `library ieee;
use ieee.std_logic_1164.all;

-- a comment line
entity e is
  port (clk : in std_logic; q : out std_logic);
end entity;

architecture a of e is
  signal s : std_logic;
begin
  q <= s;
end architecture;
`

// Keystrokes is a pure function of (text, seed), alternates insert
// and delete on code lines only, and every pair restores the text.
func TestKeystrokes(t *testing.T) {
	text := []byte(sample)
	lines := bytes.Split(text, []byte("\n"))
	for seed := range uint64(64) {
		a := Keystrokes(text, seed, 21)
		if !slices.Equal(a, Keystrokes(text, seed, 21)) {
			t.Fatalf("seed %d: not deterministic", seed)
		}
		if len(a) != 21 {
			t.Fatalf("seed %d: %d edits", seed, len(a))
		}
		cur := text
		for i, e := range a {
			l := bytes.TrimSpace(lines[e.Line])
			if len(l) == 0 || bytes.HasPrefix(l, []byte("--")) {
				t.Fatalf("seed %d: edit %d on line %q", seed, i, l)
			}
			cur = Apply(cur, e)
			if i%2 == 1 && !bytes.Equal(cur, text) {
				t.Fatalf("seed %d: text not restored after %d", seed, i)
			}
			if i%2 == 0 && len(cur) != len(text)+len(typed) {
				t.Fatalf("seed %d: insert %d changed %d bytes", seed, i,
					len(cur)-len(text))
			}
		}
	}
	if Keystrokes([]byte("/* x */\nfoo;\n"), 1, 2) != nil {
		t.Error("a file with block comments has no edit lines")
	}
	if Keystrokes([]byte("-- only\n\n"), 1, 2) != nil {
		t.Error("comments only: want no edits")
	}
}

// Names picks positions inside non-keyword names, deterministically.
func TestNames(t *testing.T) {
	text := []byte(sample)
	lines := bytes.Split(text, []byte("\n"))
	for seed := range uint64(32) {
		ps := Names(text, seed, 10)
		if !slices.Equal(ps, Names(text, seed, 10)) {
			t.Fatal("not deterministic")
		}
		for _, p := range ps {
			l := lines[p.Line]
			c := l[p.Char]
			if !(c == '_' || c >= '0' && c <= '9' ||
				c|0x20 >= 'a' && c|0x20 <= 'z') {
				t.Fatalf("position %v is not in a name: %q", p, l)
			}
		}
	}
}

// Percentile is nearest-rank: an element of the input, monotone in p,
// order-independent.
func TestPercentile(t *testing.T) {
	if !math.IsNaN(Percentile(nil, 95)) {
		t.Error("no samples: want NaN")
	}
	xs := []float64{5, 1, 4, 2, 3, 10, 9, 8, 7, 6}
	if got := Percentile(xs, 95); got != 10 {
		t.Errorf("p95 = %v, want 10", got)
	}
	if got := Percentile(xs, 50); got != 5 {
		t.Errorf("p50 = %v, want 5", got)
	}
	r := slices.Clone(xs)
	slices.Reverse(r)
	prev := math.Inf(-1)
	for p := 1.0; p <= 100; p++ {
		v := Percentile(xs, p)
		if !slices.Contains(xs, v) || v < prev || v != Percentile(r, p) {
			t.Fatalf("p%v = %v", p, v)
		}
		prev = v
	}
}

func TestTierAndGate(t *testing.T) {
	if Tier(49.9) != "as you type" || Tier(50) != "on save" {
		t.Error("the 50 ms rule: under 50 ms is as you type")
	}
	if (Gate{LimitMS: 100, P95MS: 99, Measured: true}).Pass() != true ||
		(Gate{LimitMS: 100, P95MS: 100, Measured: true}).Pass() ||
		(Gate{LimitMS: 100}).Pass() {
		t.Error("gate: p95 must be under the limit and measured")
	}
}

// split takes the last complete analysis inside [t0, pub] and its
// ready times are cumulative and ordered.
func TestSplit(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time {
		return t0.Add(time.Duration(ms) * time.Millisecond)
	}
	evs := []event{
		{at: at(-5), name: "parse"}, // an earlier analysis
		{at: at(1), name: "parse"},
		{at: at(3), name: "units"},
		{at: at(10), name: "cross-file", ovl: 4 * time.Millisecond},
		{at: at(12), name: "static"},
		{at: at(20), name: "done"},
		{at: at(21), name: "hierarchy"},
		{at: at(25), name: "analyzed"},
		{at: at(40), name: "parse"}, // a query after the publish
	}
	s, ok := split(evs, t0, at(26))
	if !ok {
		t.Fatal("no analysis found")
	}
	want := map[string]float64{"parse": 3, "names": 10, "overloads": 10,
		"static": 18, "cross-file": 16}
	for c, w := range want {
		if s.Ready[c] != w {
			t.Errorf("ready %s = %v, want %v", c, s.Ready[c], w)
		}
	}
	if s.Self["overloads"] != 4 || s.Publish != 26 {
		t.Errorf("self overloads %v, publish %v", s.Self["overloads"],
			s.Publish)
	}
	for _, c := range Checks {
		if s.Ready[c] > s.Publish || s.Self[c] > s.Ready[c] {
			t.Errorf("%s: ready %v self %v publish %v", c, s.Ready[c],
				s.Self[c], s.Publish)
		}
	}
	if _, ok := split(evs[:7], t0, at(26)); ok {
		t.Error("no analyzed event: want no sample")
	}
	if _, ok := split(evs, at(2), at(26)); ok {
		t.Error("parse before t0: want no sample")
	}
}

// Every project builds from testdata with its target present and
// only VHDL files and gohard.yaml copied.
func TestProjects(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the corpus projects")
	}
	for _, p := range Projects {
		dir := t.TempDir()
		n, err := p.Build("../..", dir)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatalf("%s: no files", p.Name)
		}
		err = filepath.WalkDir(dir, func(path string, d os.DirEntry,
			err error,
		) error {
			if err != nil || d.IsDir() {
				return err
			}
			switch filepath.Ext(path) {
			case ".vhd", ".vhdl", ".yaml":
				return nil
			}
			t.Errorf("%s: copied %s", p.Name, path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		text, _ := os.ReadFile(filepath.Join(dir, p.Target))
		if Keystrokes(text, 1, 2) == nil || Names(text, 1, 2) == nil {
			t.Errorf("%s: target has no edit lines or names", p.Name)
		}
	}
}

// The large project is the files GHDL compiles for NEORV32's
// testbench with OSVVM, so it analyzes with no error: its testbench
// is the first-simulation-progress measure.
func TestLargeProjectChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("analyzes NEORV32 and OSVVM")
	}
	p, _ := projectByName("large")
	dir := t.TempDir()
	if _, err := p.Build("../..", dir); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	e := realenv.New(realenv.Options{Stdout: &stdout, Stderr: &stderr,
		Dir: dir})
	code := cli.Run(context.Background(), e, []string{"check"})
	if code != 0 || !strings.Contains(stdout.String(),
		"summary: 0 errors") {
		t.Errorf("gohard check: exit %d\n%s%s", code, stdout.String(),
			stderr.String())
	}
}

// The report derives tiers by the 50 ms rule and marks a missed gate.
func TestReport(t *testing.T) {
	mk := func(names, pub float64) Sample {
		return Sample{Publish: pub,
			Ready: map[string]float64{"parse": 1, "names": names,
				"overloads": names, "static": names, "cross-file": names},
			Self: map[string]float64{}}
	}
	large := result{Project: "large", First: []float64{2500},
		Warm: childOut{Samples: []Sample{mk(10, 20), mk(60, 70)},
			Hover: []float64{1}, Definition: []float64{1},
			Completion: []float64{1}, Save: []float64{1}},
		Sim: childOut{SimKernel: []float64{3}}}
	out := report("", []result{large})
	for _, want := range []string{"| parse | 1.0 ms | as you type |",
		"| names | 60.0 ms | on save |",
		"| First diagnostics from cold start, p95 < 2 s | 2.50 s | MISS |",
		"1 gate(s) missed"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
