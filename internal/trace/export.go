package trace

import "slices"

// Exporter converts a native trace into a viewer format. The same
// trace always gives the same bytes on every platform.
type Exporter interface {
	// Name is the --format value, such as "vcd".
	Name() string
	Export(t *Trace) ([]byte, error)
}

// Settled is a signal's settled value at one time.
type Settled struct {
	Signal int
	Value  Value
}

// Step holds the settled values that differ from the previous step, in
// signal order.
type Step struct {
	Time    Time
	Changes []Settled
}

// Settle returns, for each time with changes, the value each signal
// settles on after the last delta of that time, keeping only the
// signals whose settled value differs from their previous one. A value
// that changes and changes back within one time step (a zero-width
// glitch) does not appear. Steps are in time order; a time whose
// settled values all equal the previous ones is omitted.
func Settle(t *Trace) []Step {
	var steps []Step
	cur := make([]*Value, len(t.Signals))
	last := make([]int, len(t.Signals)) // change index in this step
	seen := make([]int, len(t.Signals)) // step number + 1
	for i, n := 0, 1; i < len(t.Changes); n++ {
		at := t.Changes[i].Time
		var touched []int
		for ; i < len(t.Changes) && t.Changes[i].Time == at; i++ {
			s := t.Changes[i].Signal
			if seen[s] != n {
				seen[s] = n
				touched = append(touched, s)
			}
			last[s] = i
		}
		slices.Sort(touched)
		st := Step{Time: at}
		for _, s := range touched {
			v := &t.Changes[last[s]].Value
			if cur[s] != nil && cur[s].Equal(*v) {
				continue
			}
			cur[s] = v
			st.Changes = append(st.Changes, Settled{Signal: s, Value: *v})
		}
		if len(st.Changes) > 0 {
			steps = append(steps, st)
		}
	}
	return steps
}
