package trace

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Validate checks that t is a well-formed trace in canonical order (see
// Trace) and builds its lookup tables. Recorder and Decode call it; a
// hand-built Trace must pass it before it is used.
func (t *Trace) Validate() error {
	if err := t.validateDecls(); err != nil {
		return err
	}
	t.index()
	if err := t.validateChanges(); err != nil {
		return err
	}
	return t.validateEvents()
}

func (t *Trace) validateDecls() error {
	names := map[string]bool{}
	for i := range t.Types {
		ty := &t.Types[i]
		if err := validName("type", ty.Name); err != nil {
			return err
		}
		if i > 0 && t.Types[i-1].Name >= ty.Name {
			return fmt.Errorf("type %s: types not sorted by name",
				ty.Name)
		}
		names[ty.Name] = true
	}
	for i := range t.Types {
		if err := validType(&t.Types[i], t.Types, names); err != nil {
			return fmt.Errorf("type %s: %w", t.Types[i].Name, err)
		}
	}
	for i, s := range t.Scopes {
		if err := validName("scope", s.Path); err != nil {
			return err
		}
		if i > 0 && t.Scopes[i-1].Path >= s.Path {
			return fmt.Errorf("scope %s: scopes not sorted by path",
				s.Path)
		}
		if s.Kind < Instance || s.Kind > Package {
			return fmt.Errorf("scope %s: bad kind %d", s.Path, s.Kind)
		}
	}
	for i := range t.Signals {
		s := &t.Signals[i]
		if err := validName("signal", s.Path); err != nil {
			return err
		}
		if i > 0 && t.Signals[i-1].Path >= s.Path {
			return fmt.Errorf("signal %s: signals not sorted by path",
				s.Path)
		}
		if err := validSignal(s, t.Types); err != nil {
			return fmt.Errorf("signal %s: %w", s.Path, err)
		}
	}
	for i, p := range t.Processes {
		if err := validName("process", p.Path); err != nil {
			return err
		}
		if i > 0 && t.Processes[i-1].Path >= p.Path {
			return fmt.Errorf("process %s: processes not sorted by "+
				"path", p.Path)
		}
	}
	return nil
}

func validName(what, s string) error {
	if s == "" {
		return fmt.Errorf("%s with an empty name", what)
	}
	if strings.ContainsAny(s, "\n\r") {
		return fmt.Errorf("%s %q: name contains a line break", what, s)
	}
	return nil
}

func findType(types []Type, name string) *Type {
	lo, hi := 0, len(types)
	for lo < hi {
		m := (lo + hi) / 2
		switch c := cmp.Compare(types[m].Name, name); {
		case c == 0:
			return &types[m]
		case c < 0:
			lo = m + 1
		default:
			hi = m
		}
	}
	return nil
}

func validType(ty *Type, types []Type, names map[string]bool) error {
	switch ty.Kind {
	case Logic, Real:
	case Enum:
		if len(ty.Literals) == 0 {
			return errors.New("enum without literals")
		}
		seen := map[string]bool{}
		for _, l := range ty.Literals {
			if err := validName("literal", l); err != nil {
				return err
			}
			if seen[l] {
				return fmt.Errorf("duplicate literal %s", l)
			}
			seen[l] = true
		}
	case Integer:
		if ty.Low > ty.High {
			return errors.New("empty range")
		}
	case Physical:
		if ty.Low > ty.High {
			return errors.New("empty range")
		}
		if len(ty.Units) == 0 || ty.Units[0].Mult != 1 {
			return errors.New("the first unit must have multiplier 1")
		}
		seen := map[string]bool{}
		for i, u := range ty.Units {
			if err := validName("unit", u.Name); err != nil {
				return err
			}
			if seen[u.Name] {
				return fmt.Errorf("duplicate unit %s", u.Name)
			}
			seen[u.Name] = true
			if i > 0 && u.Mult <= ty.Units[i-1].Mult {
				return errors.New("units not in ascending multiplier")
			}
		}
	case Array:
		if !names[ty.Elem] {
			return fmt.Errorf("unknown element type %s", ty.Elem)
		}
		if findType(types, ty.Elem).Kind == Array {
			return errors.New("array of arrays")
		}
	default:
		return fmt.Errorf("bad kind %d", ty.Kind)
	}
	return nil
}

func validSignal(s *Signal, types []Type) error {
	ty := findType(types, s.Type)
	if ty == nil {
		return fmt.Errorf("unknown type %s", s.Type)
	}
	if s.Dir < Internal || s.Dir > Linkage {
		return fmt.Errorf("bad mode %d", s.Dir)
	}
	if ty.Kind != Array && (s.Left != 0 || s.Right != 0 || s.Downto) {
		return errors.New("a range on a scalar signal")
	}
	if ty.Kind == Array && s.Len() > 1<<24 {
		return errors.New("array longer than 16777216 elements")
	}
	return nil
}

// checkValue reports whether v is a valid value of signal s.
func (t *Trace) checkValue(s *Signal, v Value) error {
	ty, _ := t.Type(s.Type)
	if ty.Kind != Array {
		return t.checkScalar(ty, v)
	}
	if len(v.Elems) != s.Len() {
		return fmt.Errorf("%d elements for a range of %d", len(v.Elems),
			s.Len())
	}
	if v.Int != 0 || math.Float64bits(v.Real) != 0 {
		return errors.New("array value with a scalar part")
	}
	et, _ := t.Type(ty.Elem)
	for _, e := range v.Elems {
		if err := t.checkScalar(et, e); err != nil {
			return err
		}
	}
	return nil
}

func (t *Trace) checkScalar(ty *Type, v Value) error {
	if v.Elems != nil {
		return errors.New("scalar value with elements")
	}
	if ty.Kind != Real && math.Float64bits(v.Real) != 0 {
		return fmt.Errorf("%s value with a real part", ty.Kind)
	}
	if ty.Kind == Real && v.Int != 0 {
		return errors.New("real value with an integer part")
	}
	switch ty.Kind {
	case Logic:
		if v.Int < 0 || v.Int >= int64(len(LogicChars)) {
			return fmt.Errorf("std_ulogic position %d", v.Int)
		}
	case Enum:
		if v.Int < 0 || v.Int >= int64(len(ty.Literals)) {
			return fmt.Errorf("position %d out of %s", v.Int, ty.Name)
		}
	case Integer, Physical:
		if v.Int < ty.Low || v.Int > ty.High {
			return fmt.Errorf("%d out of range of %s", v.Int, ty.Name)
		}
	}
	return nil
}

func (t *Trace) validateChanges() error {
	last := make([]int, len(t.Signals))
	for i := range last {
		last[i] = -1
	}
	for i := range t.Changes {
		c := &t.Changes[i]
		if c.Signal < 0 || c.Signal >= len(t.Signals) {
			return fmt.Errorf("change %d: no signal %d", i, c.Signal)
		}
		s := &t.Signals[c.Signal]
		where := fmt.Sprintf("change of %s at %d+%d", s.Path, c.Time,
			c.Delta)
		if i > 0 {
			p := &t.Changes[i-1]
			if cmp.Or(cmp.Compare(p.Time, c.Time),
				cmp.Compare(p.Delta, c.Delta),
				cmp.Compare(p.Signal, c.Signal)) >= 0 {
				return fmt.Errorf("%s: changes not sorted by time, "+
					"delta and path", where)
			}
		}
		if c.Time > t.Header.End {
			return fmt.Errorf("%s: after the end time %d", where,
				t.Header.End)
		}
		if c.Process < 0 || c.Process > len(t.Processes) {
			return fmt.Errorf("%s: no process %d", where, c.Process)
		}
		if c.Process > 0 && t.Processes[c.Process-1].Modeled &&
			!c.Modeled {
			return fmt.Errorf("%s: driven by a model but not marked "+
				"modeled", where)
		}
		if err := t.checkValue(s, c.Value); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if j := last[c.Signal]; j >= 0 &&
			t.Changes[j].Value.Equal(c.Value) {
			return fmt.Errorf("%s: the value does not change", where)
		}
		last[c.Signal] = i
	}
	return nil
}

func (t *Trace) validateEvents() error {
	for i := range t.Events {
		e := &t.Events[i]
		where := fmt.Sprintf("event at %d+%d", e.Time, e.Delta)
		if i > 0 {
			p := &t.Events[i-1]
			if cmp.Or(cmp.Compare(p.Time, e.Time),
				cmp.Compare(p.Delta, e.Delta),
				cmp.Compare(p.Process, e.Process)) > 0 {
				return fmt.Errorf("%s: events not sorted by time, "+
					"delta and process", where)
			}
		}
		if e.Time > t.Header.End {
			return fmt.Errorf("%s: after the end time %d", where,
				t.Header.End)
		}
		switch e.Kind {
		case Assert, Report:
			if e.Severity < Note || e.Severity > Failure {
				return fmt.Errorf("%s: %s needs a severity", where,
					e.Kind)
			}
		case CoverHit, CoverMiss:
			if e.Severity != NoSeverity {
				return fmt.Errorf("%s: %s has no severity", where,
					e.Kind)
			}
		default:
			return fmt.Errorf("%s: bad kind %d", where, e.Kind)
		}
		if err := validName("event file", e.File); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if e.Line < 1 {
			return fmt.Errorf("%s: line %d", where, e.Line)
		}
		if e.Process < 0 || e.Process > len(t.Processes) {
			return fmt.Errorf("%s: no process %d", where, e.Process)
		}
	}
	return nil
}
