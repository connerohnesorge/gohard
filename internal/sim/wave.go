package sim

import "github.com/connerohnesorge/gohard/v2/internal/eval"

// txn is one transaction of a projected output waveform: a scalar
// value (or a disconnection) at an absolute time.
type txn struct {
	at   Time
	v    eval.Value
	null bool
}

// project updates the projected output waveform old of one driver with
// the new transactions neu (non-empty, strictly ascending in time, all
// at or after the current time), following LRM 10.5.2.2. The
// transaction that determines the driver's current value is not part
// of old and is never deleted. reject is the pulse rejection limit for
// inertial delay (ignored for transport). The result may share old's
// backing array.
func project(old, neu []txn, transport bool, reject Time) []txn {
	first := neu[0].at
	// 1. Old transactions at or after the first new one are deleted.
	n := 0
	for n < len(old) && old[n].at < first {
		n++
	}
	old = old[:n]
	if !transport && n > 0 {
		// 2. Old transactions before first-reject are kept (marked).
		limit := Time(0)
		if first > reject {
			limit = first - reject
		}
		keep := 0
		for keep < len(old) && old[keep].at < limit {
			keep++
		}
		// 3. Within the window, an old transaction is marked when it
		// immediately precedes a marked one with the same value: the
		// run of same-valued transactions right before the first new
		// one survives; everything else in the window is deleted.
		run := len(old)
		for run > keep && sameTxn(old[run-1], neu[0]) {
			run--
		}
		out := old[:keep:keep]
		out = append(out, old[run:]...)
		old = out
	}
	return append(old, neu...)
}

func sameTxn(a, b txn) bool {
	if a.null || b.null {
		return a.null == b.null
	}
	return a.v.Equal(b.v)
}
