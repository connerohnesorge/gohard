package env

import "path/filepath"

// Pure helpers shared by realenv, simenv and their callers. None of them
// touches the world.

// Abs resolves name against dir, an absolute directory, the way the OS
// resolves a name against the current directory, and cleans the result
// lexically. An absolute name is kept. On Windows a rooted name without
// a volume (\x or /x, which filepath.IsAbs rejects) takes the volume of
// dir instead of being joined below it, so /work stays /work in simenv
// and \x is C:\x under C:\cwd, not C:\cwd\x. Any other name is joined to
// dir.
func Abs(dir, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	if filepath.VolumeName(name) == "" && name != "" &&
		(name[0] == '/' || name[0] == filepath.Separator) {
		return filepath.Join(filepath.VolumeName(dir), name)
	}
	return filepath.Join(dir, name)
}

// SplitMix64 is a tiny, fast, seedable generator (Steele, Lea and
// Flood). It implements Rand. Its whole state is one uint64, so a copy
// is a fork.
type SplitMix64 struct {
	State uint64
}

// Uint64 returns the next value.
func (s *SplitMix64) Uint64() uint64 {
	s.State += 0x9e3779b97f4a7c15
	return mix64(s.State)
}

// Intn returns a value in [0, n) for n > 0, and 0 otherwise. It uses
// rejection sampling, so every value is equally likely.
func Intn(r Rand, n int) int {
	if n <= 1 {
		return 0
	}
	bound := uint64(n)
	limit := -bound % bound // 2^64 mod n
	for {
		v := r.Uint64()
		if v >= limit {
			return int(v % bound)
		}
	}
}

func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Shuffle returns the in-delta order for n processes under seed at
// step. Seed 0 is declaration order (the identity); any other seed gives
// a Fisher-Yates shuffle drawn from SplitMix64 keyed by (seed, step), so
// the result is a pure function of its arguments (#29).
func Shuffle(seed, step uint64, n int) []int {
	out := make([]int, max(n, 0))
	for i := range out {
		out[i] = i
	}
	if seed == 0 {
		return out
	}
	r := &SplitMix64{State: mix64(seed) ^ mix64(step+0x632be59bd9b4e019)}
	for i := len(out) - 1; i > 0; i-- {
		j := Intn(r, i+1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// SeedOrder is the Order door for a seed; both realenv and simenv use it.
type SeedOrder struct {
	Seed uint64
}

// Permute implements Order with Shuffle.
func (o SeedOrder) Permute(step uint64, n int) []int {
	return Shuffle(o.Seed, step, n)
}

// Sequential is a Jobs runner that calls every job in index order on the
// calling goroutine. It is correct everywhere and is the reference model
// for the other runners.
type Sequential struct{}

// Run implements Jobs.
func (Sequential) Run(_, n int, job func(i int)) {
	for i := range n {
		job(i)
	}
}

// Map applies f to every element of in through j and returns the results
// in input order, whatever order the runner used (P3).
func Map[In, Out any](j Jobs, limit int, in []In, f func(In) Out) []Out {
	out := make([]Out, len(in))
	j.Run(limit, len(in), func(i int) { out[i] = f(in[i]) })
	return out
}

// LookupFunc adapts a map to a Getenv door (for tests and simenv). Keys
// missing from m are unset.
func LookupFunc(m map[string]string) Getenv {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}
