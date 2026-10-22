package env

import (
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
)

func TestSplitMix64KnownValues(t *testing.T) {
	r := &SplitMix64{}
	want := []uint64{
		0xe220a8397b1dcdaf, 0x6e789e6aa1b965f4, 0x06c45d188009454f,
	}
	for i, w := range want {
		if got := r.Uint64(); got != w {
			t.Fatalf("output %d = %#x, want %#x", i, got, w)
		}
	}
	fork := *r
	if fork.Uint64() != r.Uint64() {
		t.Fatal("a copy must be a fork")
	}
}

func TestIntn(t *testing.T) {
	r := &SplitMix64{State: 7}
	for _, n := range []int{-1, 0, 1, 2, 3, 7, 1000} {
		counts := make([]int, max(n, 1))
		for range 5000 {
			v := Intn(r, n)
			if v < 0 || v >= max(n, 1) {
				t.Fatalf("Intn(%d) = %d", n, v)
			}
			counts[v]++
		}
		if n == 2 || n == 3 {
			for v, c := range counts {
				if c < 5000/n*8/10 {
					t.Errorf("Intn(%d): %d drawn only %d times",
						n, v, c)
				}
			}
		}
	}
}

func isPerm(p []int, n int) bool {
	s := slices.Clone(p)
	slices.Sort(s)
	for i, v := range s {
		if v != i {
			return false
		}
	}
	return len(s) == n
}

func TestShuffleLaws(t *testing.T) {
	seeds := []uint64{0, 1, 2, 3, 42, 1 << 63, ^uint64(0)}
	for _, seed := range seeds {
		distinct := false
		for step := range uint64(20) {
			for _, n := range []int{-1, 0, 1, 2, 3, 5, 16} {
				p := Shuffle(seed, step, n)
				if !isPerm(p, max(n, 0)) {
					t.Fatalf("Shuffle(%d,%d,%d) = %v not a "+
						"permutation", seed, step, n, p)
				}
				if !slices.Equal(p, Shuffle(seed, step, n)) {
					t.Fatal("Shuffle is not a pure function")
				}
				if seed == 0 && !slices.IsSorted(p) {
					t.Fatalf("seed 0 must keep declaration order: %v",
						p)
				}
				if n == 16 && !slices.IsSorted(p) {
					distinct = true
				}
			}
		}
		if seed != 0 && !distinct {
			t.Errorf("seed %d never shuffled 16 processes", seed)
		}
	}
	if slices.Equal(Shuffle(1, 0, 16), Shuffle(2, 0, 16)) &&
		slices.Equal(Shuffle(1, 1, 16), Shuffle(2, 1, 16)) {
		t.Error("seeds 1 and 2 give the same orders")
	}
	o := SeedOrder{Seed: 9}
	if !slices.Equal(o.Permute(4, 8), Shuffle(9, 4, 8)) {
		t.Error("SeedOrder must use Shuffle")
	}
}

// reverse is an adversarial runner: it runs jobs last to first.
type reverse struct{}

func (reverse) Run(_, n int, job func(i int)) {
	for i := n - 1; i >= 0; i-- {
		job(i)
	}
}

func TestMapCanonicalOrder(t *testing.T) {
	in := []int{5, 3, 9, 1, 0, 7}
	sq := func(x int) int { return x * x }
	want := []int{25, 9, 81, 1, 0, 49}
	for _, j := range []Jobs{Sequential{}, reverse{}} {
		for _, limit := range []int{0, 1, 16} {
			if got := Map(j, limit, in, sq); !slices.Equal(got, want) {
				t.Fatalf("%T limit %d: %v", j, limit, got)
			}
		}
	}
	if got := Map(Sequential{}, 1, []int(nil), sq); len(got) != 0 {
		t.Fatalf("empty Map = %v", got)
	}
}

func TestLookupFunc(t *testing.T) {
	g := LookupFunc(map[string]string{"A": "1", "E": ""})
	if v, ok := g("A"); !ok || v != "1" {
		t.Error("A")
	}
	if _, ok := g("E"); !ok {
		t.Error("empty value must be set")
	}
	if _, ok := g("B"); ok {
		t.Error("B must be unset")
	}
}

func TestInterrupt(t *testing.T) {
	if SIGINT.Exit != 130 || SIGTERM.Exit != 143 ||
		SIGINT.Error() != "SIGINT" || SIGTERM.Error() != "SIGTERM" {
		t.Fatal(SIGINT, SIGTERM)
	}
}

func TestParseSeed(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"0", 0, true},
		{"1", 1, true},
		{"007", 7, true},
		{"18446744073709551615", 1<<64 - 1, true},
		{"18446744073709551616", 0, false},
		{"", 0, false},
		{"-1", 0, false},
		{"+1", 0, false},
		{" 1", 0, false},
		{"0x10", 0, false},
		{"1_000", 0, false},
		{"Random", 0, false},
	} {
		got, random, err := ParseSeed(tc.in, &SplitMix64{})
		if (err == nil) != tc.ok || got != tc.want || random {
			t.Errorf("ParseSeed(%q) = %d, %v, %v", tc.in, got, random, err)
		}
	}
}

// zeroThenRand returns zeros before handing over to a real generator.
type zeroThenRand struct {
	zeros int
	r     SplitMix64
}

func (z *zeroThenRand) Uint64() uint64 {
	if z.zeros > 0 {
		z.zeros--
		return 0
	}
	return z.r.Uint64()
}

func TestParseSeedRandom(t *testing.T) {
	r := &zeroThenRand{zeros: 3, r: SplitMix64{State: 9}}
	want := (&SplitMix64{State: 9}).Uint64()
	got, random, err := ParseSeed(SeedRandom, r)
	if err != nil || !random || got != want || got == 0 {
		t.Fatalf("ParseSeed(random) = %d, %v, %v; want %d", got, random,
			err, want)
	}
	if SeedLabel(got) != "seed "+strconv.FormatUint(want, 10) {
		t.Fatalf("SeedLabel = %q", SeedLabel(got))
	}
	// Whatever the seed, its label parses back to it.
	s := &SplitMix64{State: 1}
	for range 500 {
		v := s.Uint64() >> (s.Uint64() % 64)
		back, _, err := ParseSeed(SeedLabel(v)[len("seed "):], s)
		if err != nil || back != v {
			t.Fatalf("label of %d parses to %d, %v", v, back, err)
		}
	}
}

// TestShuffleDependsOnStep: under a nonzero seed the in-delta order is a
// function of the step too, so different deltas see different orders
// and order-dependent designs are exposed (#29).
func TestShuffleDependsOnStep(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		first := Shuffle(seed, 0, 8)
		differs := false
		for step := uint64(1); step < 16 && !differs; step++ {
			differs = !slices.Equal(Shuffle(seed, step, 8), first)
		}
		if !differs {
			t.Fatalf("seed %d: every step gives order %v", seed, first)
		}
	}
}

// TestShuffleUniform: Shuffle is an unbiased Fisher-Yates shuffle. Over
// many steps every permutation of three elements, the identity
// included, appears about equally often (a Sattolo shuffle would give
// only the two cyclic ones).
func TestShuffleUniform(t *testing.T) {
	const steps = 60000
	for _, seed := range []uint64{1, 7, 1 << 40} {
		count := map[string]int{}
		for step := range uint64(steps) {
			p := Shuffle(seed, step, 3)
			count[strconv.Itoa(p[0])+strconv.Itoa(p[1])+
				strconv.Itoa(p[2])]++
		}
		if len(count) != 6 {
			t.Fatalf("seed %d: only %d of 6 permutations: %v", seed,
				len(count), count)
		}
		for p, c := range count {
			if c < steps/6*9/10 || c > steps/6*11/10 {
				t.Errorf("seed %d: permutation %s seen %d times of %d",
					seed, p, c, steps)
			}
		}
	}
}

// TestSequentialPanicsAtLowestIndex pins the env.Jobs panic contract on
// the reference runner.
func TestSequentialPanicsAtLowestIndex(t *testing.T) {
	defer func() {
		if r := recover(); r != 2 {
			t.Fatalf("recovered %v, want 2", r)
		}
	}()
	Sequential{}.Run(0, 9, func(i int) {
		if i >= 2 {
			panic(i)
		}
	})
}

func TestAbs(t *testing.T) {
	cases := []struct{ dir, name, want string }{
		{"/work", "a/b.vhd", filepath.FromSlash("/work/a/b.vhd")},
		{"/work", "/x/../y", filepath.FromSlash("/y")},
		{"/work", "../z", filepath.FromSlash("/z")},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			struct{ dir, name, want string }{`C:\cwd`, `\x`, `C:\x`},
			struct{ dir, name, want string }{`C:\cwd`, `/x/y`, `C:\x\y`},
			struct{ dir, name, want string }{`C:\cwd`, `D:\x`, `D:\x`},
			struct{ dir, name, want string }{`\work`, `\x`, `\x`},
		)
	}
	for _, c := range cases {
		if got := Abs(c.dir, c.name); got != c.want {
			t.Errorf("Abs(%q, %q) = %q, want %q", c.dir, c.name, got,
				c.want)
		}
	}
}

// TestIsRooted: a rooted name is exactly one Abs keeps at the root
// instead of joining below dir, on every OS.
func TestIsRooted(t *testing.T) {
	cases := map[string]bool{
		"": false, "a": false, "a/b": false, "./a": false, "../a": false,
		"/": true, "/work": true, "/x/y": true,
	}
	if runtime.GOOS == "windows" {
		cases[`\x`] = true
		cases[`C:\x`] = true
		cases[`C:x`] = false
		cases[`\\srv\share\x`] = true
		cases[`a\b`] = false
	}
	const dir = "/work/sub"
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		want := cases[name]
		if got := IsRooted(name); got != want {
			t.Errorf("IsRooted(%q) = %v, want %v", name, got, want)
		}
		joined := filepath.Join(dir, name)
		if name != "" && (Abs(dir, name) != filepath.Clean(joined)) != want {
			t.Errorf("IsRooted(%q) = %v disagrees with Abs: %q",
				name, want, Abs(dir, name))
		}
	}
}
