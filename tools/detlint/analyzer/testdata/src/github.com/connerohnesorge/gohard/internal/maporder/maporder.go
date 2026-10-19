// Package maporder exercises the map-order rule.
package maporder

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Bad: the iteration order reaches output.

func write(w io.Writer, m map[string]int) {
	for k, v := range m { // want `range over a map whose body depends on`
		fmt.Fprintf(w, "%s=%d\n", k, v)
	}
}

func unsorted(m map[string]int) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k) // want `append to keys in map order without a`
	}
	return keys
}

func sortedBefore(m map[string]int) []string {
	var keys []string
	sort.Strings(keys)
	for k := range m {
		keys = append(keys, k) // want `append to keys in map order without a`
	}
	return keys
}

func field(m map[string]int, s *struct{ keys []string }) {
	for k := range m { // want `range over a map whose body depends`
		s.keys = append(s.keys, k)
	}
}

func concat(m map[string]int) string {
	s := ""
	for k := range m { // want `range over a map whose body depends`
		s += k
	}
	return s
}

func floatSum(m map[string]float64) float64 {
	var sum float64
	for _, v := range m { // want `range over a map whose body depends`
		sum += v
	}
	return sum
}

func lastWins(m map[string]int) (best string) {
	top := -1
	for k, v := range m { // want `range over a map whose body depends`
		if v > top {
			top, best = v, k
		}
	}
	return best
}

func returnKey(m map[string]int) string {
	for k := range m { // want `range over a map whose body depends`
		return k
	}
	return ""
}

func breakEarly(m map[string]int) int {
	n := 0
	for range m { // want `range over a map whose body depends`
		n++
		if n > 2 {
			break
		}
	}
	return n
}

func outerVar(m map[string]int) string {
	var k string
	for k = range m { // want `range over a map assigns to variables outside`
	}
	return k
}

func userCall(m map[string]int, f func(string)) {
	for k := range m { // want `range over a map whose body depends`
		f(k)
	}
}

func send(m map[string]int, c chan string) {
	for k := range m { // want `range over a map whose body depends`
		c <- k
	}
}

func generic[M ~map[K]V, K comparable, V any](m M, out *[]K) {
	for k := range m { // want `range over a map whose body depends`
		*out = append(*out, k)
	}
}

func keysIter(m map[string]int, w io.Writer) {
	for k := range maps.Keys(m) { // want `range over a map whose body depends`
		io.WriteString(w, k)
	}
	_ = slices.Collect(maps.Values(m)) // want `maps.Values yields in map order`
	ks := slices.Collect(maps.Keys(m)) // want `maps.Keys yields in map order`
	_ = ks
	seq := maps.All(m) // want `maps.All yields in map order`
	_ = seq
}

func fixedIndex(m map[string]int, out []int) {
	for _, v := range m { // want `range over a map whose body depends`
		out[0] = v // keeps whichever entry came last
	}
}

func invert(m map[string]int, inv map[int]string) {
	for k, v := range m { // want `range over a map whose body depends`
		inv[v] = k // values collide: the last key wins
	}
}

func mixedOps(m map[string]int) int {
	x := 1
	for _, v := range m { // want `range over a map whose body depends`
		x += v
		x *= 3
	}
	return x
}

func mixedBits(m map[string]int) int {
	x := 0
	for _, v := range m { // want `range over a map whose body depends`
		x |= v
		x &= v + 1
	}
	return x
}

func minThenMax(m map[string]int) int {
	x := 0
	for _, v := range m { // want `range over a map whose body depends`
		x = max(x, v)
		x = min(x, v)
	}
	return x
}

func twoConstants(m map[string]int) int {
	x := 0
	for _, v := range m { // want `range over a map whose body depends`
		if v > 0 {
			x = 1
		} else {
			x = 2
		}
	}
	return x
}

func throughPointer(m map[string]int) int {
	x := 0
	for _, v := range m { // want `range over a map whose body depends`
		p := &x
		*p = v
	}
	return x
}

func throughLocalSlice(m map[string]int, out []int) {
	for _, v := range m { // want `range over a map whose body depends`
		s := out
		s[0] = v
	}
}

func valueAssign(m map[string]int) int {
	var v int
	for _, v = range m { // want `range over a map assigns to variables outside`
	}
	return v
}

func maxOfKey(m map[int]int) int {
	best := 0
	for k := range m { // want `range over a map whose body depends`
		best = max(k, 3) // does not mention best: the last entry wins
	}
	return best
}

func laterVar(m map[string]int) {
	for k := range m { // want `range over a map whose body depends`
		later = k
	}
}

var later string // declared after the loop: not loop-local

// Sorts that leave map order in place.

func sortAfterOutput(m map[string]int, w io.Writer) {
	var s []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
	}
	io.WriteString(w, strings.Join(s, ","))
	sort.Strings(s)
}

func sortInBranch(m map[string]int, never bool) []string {
	var s []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
	}
	if never {
		sort.Strings(s)
	}
	return s
}

func sortUnreachable(m map[string]int) []string {
	var s []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
	}
	return s
	sort.Strings(s)
	return nil
}

func sortAfterCopy(m map[string]int) []string {
	var s []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
	}
	ret := slices.Clone(s)
	sort.Strings(s)
	return ret
}

func sortSubslice(m map[string]int) []string {
	var s, t []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
		t = append(t, k) // want `append to t in map order without a`
	}
	sort.Strings(s[:0])
	sort.Strings(t[:1])
	return append(s, t...)
}

func sortZero(m map[string]int) []string {
	var s []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
	}
	slices.SortFunc(s, func(a, b string) int { return 0 })
	return s
}

func sortByLen(m map[string]int) []string {
	var s []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
	}
	sort.Slice(s, func(i, j int) bool { return len(s[i]) < len(s[j]) })
	return s
}

func sortOneField(m map[string]int) []pair {
	var out, out2 []pair
	for k, v := range m {
		out = append(out, pair{k, v})   // want `append to out in map order`
		out2 = append(out2, pair{k, v}) // want `append to out2 in map order`
	}
	slices.SortStableFunc(out, func(a, b pair) int {
		return cmp.Compare(a.v, b.v)
	})
	sort.Slice(out2, func(i, j int) bool { return out2[i].v < out2[j].v })
	return append(out, out2...)
}

func sortOtherSlice(m map[string]int, other []string) []string {
	var s []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
	}
	sort.Strings(other)
	return s
}

func sortInLoopSeen(m map[string]int, ms []map[string]int,
	w io.Writer,
) {
	var s []string
	for _, m := range ms {
		io.WriteString(w, strings.Join(s, ","))
		for k := range m {
			s = append(s, k) // want `append to s in map order without a`
		}
	}
	sort.Strings(s)
}

func collectThenOutput(m map[string]int, w io.Writer) {
	ks := slices.Collect(maps.Keys(m)) // want `maps.Keys yields in map order`
	io.WriteString(w, strings.Join(ks, ","))
	slices.Sort(ks)
}

// Good: the order cannot reach output.

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type pair struct {
	k string
	v int
}

// sortedPairs compares every field, so no two distinct pairs tie.
func sortedPairs(m map[string]int) []pair {
	var out []pair
	for k, v := range m {
		if v == 0 {
			continue
		}
		out = append(out, pair{strings.ToLower(k), v * 2})
	}
	slices.SortFunc(out, func(a, b pair) int {
		return cmp.Or(strings.Compare(a.k, b.k), cmp.Compare(b.v, a.v))
	})
	return out
}

func sortedFuncs(m map[string]int, w io.Writer) {
	var keys, more, last []string
	for k := range m {
		keys = append(keys, k)
		more = append(more, k)
		last = append(last, k)
	}
	slices.SortStableFunc(keys, strings.Compare)
	slices.SortFunc(more, func(a, b string) int { return cmp.Compare(a, b) })
	sort.Slice(last, func(i, j int) bool { return last[j] < last[i] })
	io.WriteString(w, strings.Join(slices.Concat(keys, more, last), ""))
}

// sortAfterBlocks: the sort follows the if or for holding the loop.
func sortAfterBlocks(m map[string]int, ms []map[string]int, ok bool) {
	var keys []string
	if ok {
		for k := range m {
			keys = append(keys, k)
		}
	} else if len(m) > 1 {
		for k := range m {
			keys = append(keys, k)
		}
	}
	for _, m := range ms {
		{
			for k := range m {
				keys = append(keys, k)
			}
		}
	}
	switch {
	case ok:
		for k := range m {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	fmt.Sprint(keys)
}

func sortWrapped(m map[string]int) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	return keys
}

func idioms(m map[string]int) {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		fmt.Sprint(k)
	}
	_ = slices.SortedStableFunc(maps.Keys(m), strings.Compare)
	ks := slices.Collect(maps.Keys(m))
	slices.Sort(ks)
	var vs = slices.Collect(maps.Values(m))
	sort.Ints(vs)
	_ = maps.Collect(maps.All(m))
	maps.Insert(m, maps.All(m))
}

func aggregates(m map[string]int, inv map[string]int) (int, int, bool) {
	n, total, mask, flip, best, worst, prod := 0, 0, 0, 0, 0, 0, 1
	found := false
	counts := map[int]int{}
	for k, v := range m {
		n++
		total += v
		total -= v / 2
		mask |= v
		flip ^= v << 1
		prod *= v
		best = max(best, v)
		worst = min(v, worst)
		counts[v]++
		inv[k] = v
		lk := strings.ToUpper(k)
		lk += "!"
		if lk == "X!" {
			found = true
		}
		delete(m, k)
	}
	return n + total + mask + flip + best + worst + prod + len(counts),
		len(inv), found
}

func validate(m map[string][]int) bool {
	for k, vs := range m {
		if len(k) == 0 {
			return false
		}
		var parts []string
		for _, v := range vs {
			if v < 0 {
				break
			}
			parts = append(parts, fmt.Sprint(v))
		}
		switch strings.Join(parts, ",") {
		case "":
			return false
		}
	}
	return true
}

func nested(m map[string]map[string]int) []string {
	var keys []string
	for _, inner := range m {
		for k := range inner {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

func set(m map[string]bool, xs map[string][]string) map[string]bool {
	out := map[string]bool{}
	for k, ok := range m {
		if !ok {
			continue
		}
		out[k] = true
		for _, x := range xs[k] {
			out[x] = true
		}
	}
	return out
}

func genericSorted[M ~map[K]V, K ~string, V any](m M) []K {
	var keys []K
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func slice(s []string, w io.Writer) {
	for _, x := range s { // ranging over a slice is ordered
		io.WriteString(w, x)
	}
}

func sortAfterBreak(m map[string]int, stop bool) []string {
	var s []string
	for {
		for k := range m {
			s = append(s, k) // want `append to s in map order without a`
		}
		if stop {
			break
		}
		sort.Strings(s)
		break
	}
	return s
}

func sortAfterContinue(ms []map[string]int, skip bool) []string {
	var s []string
	for _, m := range ms {
		for k := range m {
			s = append(s, k) // want `append to s in map order without a`
		}
		if skip {
			continue
		}
		sort.Strings(s)
	}
	return s
}

func sortAfterGoto(m map[string]int, skip bool) []string {
	var s []string
	for k := range m {
		s = append(s, k) // want `append to s in map order without a`
	}
	if skip {
		goto out
	}
	sort.Strings(s)
out:
	return s
}

func keysValue(m map[string]int, w io.Writer) {
	k := maps.Keys[map[string]int] // want `maps.Keys used as a function`
	for x := range k(m) {
		io.WriteString(w, x)
	}
	type M = map[string]int
	v := maps.Values[M, string, int] // want `maps.Values used as`
	_ = v
	all := (maps.All[M]) // want `maps.All used as a function value`
	_ = all
}

func keysCallsOK(m map[string]int) []string {
	return slices.Sorted((maps.Keys[map[string]int])(m))
}
