package corpus

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/trace"
)

// Project returns GoHard's settled-value projection of t onto the
// signals of o: for each dumped signal, the value it settles on at
// each time (trace.Settle), written the way the oracle writes it. A
// dumped signal GoHard does not record is reported in missing.
func Project(t *trace.Trace, o *Oracle) (p *Oracle, missing []string) {
	return project(t, o, false)
}

func project(t *trace.Trace, o *Oracle, stopped bool) (p *Oracle,
	missing []string,
) {
	p = &Oracle{Tool: "gohard", Testbench: o.Testbench,
		Signals: o.Signals}
	want := map[int]OracleSignal{}
	for _, s := range o.Signals {
		i, ok := t.Lookup(s.Path)
		if !ok {
			missing = append(missing, s.Path)
			continue
		}
		want[i] = s
	}
	last := map[int]string{}
	for _, st := range trace.Settle(t) {
		// A design that calls std.env.stop/finish ends GHDL and NVC
		// inside that time step, which they never dump, though LRM
		// 14.7.5 updates the signals before the stopping process
		// runs: CompareOracleStopped skips that step.
		if stopped && st.Time == t.Header.End {
			continue
		}
		for _, c := range st.Changes {
			s, ok := want[c.Signal]
			if !ok {
				continue
			}
			v := oracleValue(t, t.SignalType(c.Signal), c.Value, s)
			if old, ok := last[c.Signal]; ok && old == v {
				continue
			}
			last[c.Signal] = v
			p.Changes = append(p.Changes, OracleChange{
				Time: uint64(st.Time), Path: s.Path, Value: v})
		}
	}
	p.Changes = sortChanges(p.Changes)
	return p, missing
}

// oracleValue writes v of type ty in the form of oracle class s:
// logic characters (enumeration characters such as bit's '0' as the
// character, boolean and other enumerations as their position in
// binary over the dumped width), integers in decimal.
func oracleValue(t *trace.Trace, ty *trace.Type, v trace.Value,
	s OracleSignal) string {
	switch s.Class {
	case ClassInteger:
		return strconv.FormatInt(v.Int, 10)
	case ClassReal:
		return strconv.FormatFloat(v.Real, 'g', -1, 64)
	}
	if ty.Kind == trace.Array {
		et := t.ElemType(ty)
		var b strings.Builder
		for _, e := range v.Elems {
			b.WriteString(scalarChar(et, e, 1))
		}
		return b.String()
	}
	return scalarChar(ty, v, s.Width)
}

func scalarChar(ty *trace.Type, v trace.Value, width int) string {
	switch ty.Kind {
	case trace.Logic:
		return string(v.Logic())
	case trace.Enum:
		l := ty.Literals[v.Int]
		if len(l) == 3 && l[0] == '\'' {
			return strings.ToUpper(l[1:2])
		}
		return fmt.Sprintf("%0*b", width, v.Int)
	case trace.Integer, trace.Physical:
		return strconv.FormatInt(v.Int, 10)
	}
	return strconv.FormatFloat(v.Real, 'g', -1, 64)
}

// CompareOracle compares GoHard's settled projection of t with the
// oracle recording o and returns the problems: dumped signals GoHard
// does not record, the first differing change line, and a different
// digest of the changes past the kept lines.
func CompareOracle(t *trace.Trace, o *Oracle) []string {
	return compareOracle(t, o, false)
}

// CompareOracleStopped is CompareOracle for a run the design ended
// with std.env.stop or finish: GoHard's changes in the final time
// step are left out, as GHDL and NVC do not dump that step.
func CompareOracleStopped(t *trace.Trace, o *Oracle) []string {
	return compareOracle(t, o, true)
}

func compareOracle(t *trace.Trace, o *Oracle, stopped bool) []string {
	p, missing := project(t, o, stopped)
	if o.Tool == "nvc" {
		p.Changes = dropUndumped(p.Changes, o.Changes)
	}
	var probs []string
	for _, m := range missing {
		probs = append(probs, "signal "+m+" is not in the GoHard trace")
	}
	n, d := digest(p.Changes)
	head := p.Changes[:min(len(p.Changes), len(o.Changes))]
	if len(o.Changes) < OracleLines {
		head = p.Changes
	}
	a, b := changeText(o.Changes), changeText(head)
	if k, la, lb, ok := trace.FirstDiff(a, b); ok {
		probs = append(probs, fmt.Sprintf("settled values differ at "+
			"change %d: oracle %q, GoHard %q", k, la, lb))
	} else if n != o.Total || d != o.Digest {
		probs = append(probs, fmt.Sprintf("settled values differ "+
			"after change %d: oracle has %d changes, GoHard %d",
			len(o.Changes), o.Total, n))
	}
	return probs
}

func changeText(cs []OracleChange) []byte {
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "%d %s %s\n", c.Time, c.Path, c.Value)
	}
	return []byte(b.String())
}

// Without returns o without the signals in paths. The recording must
// keep all its changes (a digest cannot be filtered).
func (o *Oracle) Without(paths []string) (*Oracle, error) {
	if len(paths) == 0 {
		return o, nil
	}
	if o.Total != len(o.Changes) {
		return nil, fmt.Errorf("%s recording is truncated; signals "+
			"cannot be dropped", o.Tool)
	}
	drop := map[string]bool{}
	for _, p := range paths {
		drop[p] = true
	}
	out := *o
	out.Signals, out.Changes = nil, nil
	for _, s := range o.Signals {
		if !drop[s.Path] {
			out.Signals = append(out.Signals, s)
		}
	}
	for _, c := range o.Changes {
		if !drop[c.Path] {
			out.Changes = append(out.Changes, c)
		}
	}
	out.limit()
	return &out, nil
}

// DiffSignals returns, sorted, the signals both recordings dump whose
// settled values differ. A signal only one tool can dump is not a
// disagreement.
func DiffSignals(a, b *Oracle) []string {
	by := func(o *Oracle) map[string][]string {
		m := map[string][]string{}
		for _, c := range o.Changes {
			m[c.Path] = append(m[c.Path], fmt.Sprint(c.Time, " ", c.Value))
		}
		return m
	}
	ma, mb := by(a), by(b)
	var out []string
	for _, s := range a.Signals {
		if _, ok := b.Signal(s.Path); !ok {
			continue
		}
		if !slices.Equal(ma[s.Path], mb[s.Path]) {
			out = append(out, s.Path)
		}
	}
	return out
}

// dropUndumped leaves out GoHard's time-0 values of the signals the
// recording has none for: NVC 1.23's VCD dumps a signal at time 0 only
// once it has an event, where GHDL dumps every initial value. Later
// changes are compared as usual.
func dropUndumped(gh, rec []OracleChange) []OracleChange {
	at0 := map[string]bool{}
	for _, c := range rec {
		if c.Time == 0 {
			at0[c.Path] = true
		}
	}
	out := gh[:0:0]
	for _, c := range gh {
		if c.Time == 0 && !at0[c.Path] {
			continue
		}
		out = append(out, c)
	}
	return out
}
