package trace

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Time is simulated time in femtoseconds, the VHDL base unit.
type Time uint64

// Kind is the representation class of a VHDL type in a trace.
type Kind uint8

// Type kinds. Records are not a kind: a record signal is recorded as
// one signal per scalar or array field under a Record scope.
const (
	// Logic is std_ulogic and its subtypes (std_logic): the nine values
	// of LogicChars, by position.
	Logic Kind = iota + 1
	// Enum is any other enumeration type (boolean, bit, character, user
	// types); a value is the literal's position.
	Enum
	// Integer is an integer type; a value is the integer.
	Integer
	// Real is a floating-point type; a value is a float64.
	Real
	// Physical is a physical type such as time; a value is a count of
	// the base unit.
	Physical
	// Array is a one-dimensional array of a scalar element type; the
	// signal gives the index range.
	Array
)

var kindNames = [...]string{
	Logic: "logic", Enum: "enum", Integer: "integer", Real: "real",
	Physical: "physical", Array: "array",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// LogicChars are the nine std_ulogic values in position order.
const LogicChars = "UX01ZWLH-"

// Unit is one unit of a physical type.
type Unit struct {
	Name string
	Mult int64 // in base units; the first unit has Mult 1
}

// Type is a VHDL type as the trace needs it. Name is the type's simple
// name in lower case (std_logic, state_t), unique within a trace.
type Type struct {
	Name string
	Kind Kind
	// Literals are an Enum's literals in position order, as written in
	// VHDL: identifiers in lower case, character literals with their
	// quotes ('0').
	Literals []string
	// Low and High bound Integer and Physical values.
	Low, High int64
	// Units are a Physical type's units, the base unit first, in
	// ascending Mult.
	Units []Unit
	// Elem is an Array's element type name; it must be a scalar type.
	Elem string
}

// IsScalar reports whether t is not an array.
func (t *Type) IsScalar() bool { return t.Kind != Array }

// ScopeKind is the kind of a hierarchy level.
type ScopeKind uint8

// Scope kinds.
const (
	Instance ScopeKind = iota + 1 // a design entity instance
	Block                         // a block statement
	ForGenerate
	IfGenerate
	CaseGenerate
	Record // the fields of a record signal
	Package
)

var scopeNames = [...]string{
	Instance: "instance", Block: "block", ForGenerate: "for-generate",
	IfGenerate: "if-generate", CaseGenerate: "case-generate",
	Record: "record", Package: "package",
}

func (k ScopeKind) String() string {
	if int(k) < len(scopeNames) && scopeNames[k] != "" {
		return scopeNames[k]
	}
	return fmt.Sprintf("scope(%d)", uint8(k))
}

// Scope describes one hierarchy level. Levels that have no Scope entry
// are instances without a unit name.
type Scope struct {
	Path string
	Kind ScopeKind
	// Unit is the design unit (counter(rtl)) for an instance, or the
	// record type name for a record.
	Unit string
}

// Dir is the mode of a port, or Internal for a signal.
type Dir uint8

// Signal modes.
const (
	Internal Dir = iota + 1
	In
	Out
	Inout
	Buffer
	Linkage
)

var dirNames = [...]string{
	Internal: "signal", In: "in", Out: "out", Inout: "inout",
	Buffer: "buffer", Linkage: "linkage",
}

func (d Dir) String() string {
	if int(d) < len(dirNames) && dirNames[d] != "" {
		return dirNames[d]
	}
	return fmt.Sprintf("dir(%d)", uint8(d))
}

// Signal is one recorded port or signal.
type Signal struct {
	// Path is the hierarchical path the language server uses, such as
	// tb.dut.counter_q.
	Path string
	Type string // a Type name
	Dir  Dir
	// Left, Right and Downto are the index range of an Array signal:
	// (Left to Right) or (Left downto Right).
	Left, Right int64
	Downto      bool
}

// Len returns the element count of an Array signal's range (0 for a
// null range).
func (s *Signal) Len() int {
	n := s.Right - s.Left
	if s.Downto {
		n = -n
	}
	if n < 0 {
		return 0
	}
	return int(n) + 1
}

// Index returns the VHDL index of element i of an Array signal.
func (s *Signal) Index(i int) int64 {
	if s.Downto {
		return s.Left - int64(i)
	}
	return s.Left + int64(i)
}

// Process is one process (or stand-in model process) that drives
// signals. Its ID in a Trace is its one-based position.
type Process struct {
	Path string
	// Modeled marks a process of a simulation.models stand-in (#14):
	// every value it drives is modeled.
	Modeled bool
}

// Value is a signal value. Its meaning depends on the signal's type:
// Int holds a Logic position (into LogicChars), an Enum position, an
// Integer or a Physical count; Real holds a Real; Elems holds the
// elements of an Array. Values are immutable once recorded.
type Value struct {
	Int   int64
	Real  float64
	Elems []Value
}

// LogicValue returns the Logic value for one of the characters of
// LogicChars; it panics on any other byte.
func LogicValue(c byte) Value {
	i := strings.IndexByte(LogicChars, c)
	if i < 0 {
		panic(fmt.Sprintf("trace: %q is not a std_ulogic value", c))
	}
	return Value{Int: int64(i)}
}

// Vector returns the Logic array value of s, leftmost element first,
// such as Vector("0101").
func Vector(s string) Value {
	v := Value{Elems: make([]Value, len(s))}
	for i := range len(s) {
		v.Elems[i] = LogicValue(s[i])
	}
	return v
}

// Int returns an Integer, Physical or Enum value.
func Int(n int64) Value { return Value{Int: n} }

// Float returns a Real value.
func Float(f float64) Value { return Value{Real: f} }

// Elems returns an Array value.
func Elems(e ...Value) Value { return Value{Elems: slices.Clone(e)} }

// Equal reports whether v and w are the same value. Reals compare by
// bits, so a NaN equals itself.
func (v Value) Equal(w Value) bool {
	if v.Int != w.Int ||
		math.Float64bits(v.Real) != math.Float64bits(w.Real) ||
		len(v.Elems) != len(w.Elems) {
		return false
	}
	for i := range v.Elems {
		if !v.Elems[i].Equal(w.Elems[i]) {
			return false
		}
	}
	return true
}

// Logic returns the LogicChars character of a Logic value.
func (v Value) Logic() byte {
	if v.Int < 0 || v.Int >= int64(len(LogicChars)) {
		return '?'
	}
	return LogicChars[v.Int]
}

// Change is one signal value change. In a Trace, Signal is an index
// into Trace.Signals and Process is a one-based index into
// Trace.Processes, 0 for none (an initial value).
type Change struct {
	Time    Time
	Delta   uint32
	Signal  int
	Value   Value
	Process int
	// Modeled marks a value driven by a simulation.models stand-in.
	Modeled bool
}

// EventKind is the kind of an Event.
type EventKind uint8

// Event kinds.
const (
	Assert    EventKind = iota + 1 // a failed assertion
	Report                         // a report statement
	CoverHit                       // a cover directive was hit
	CoverMiss                      // a cover directive was never hit
)

var eventNames = [...]string{
	Assert: "assert", Report: "report", CoverHit: "cover-hit",
	CoverMiss: "cover-miss",
}

func (k EventKind) String() string {
	if int(k) < len(eventNames) && eventNames[k] != "" {
		return eventNames[k]
	}
	return fmt.Sprintf("event(%d)", uint8(k))
}

// Severity is a VHDL severity level; cover events have NoSeverity.
type Severity uint8

// Severity levels.
const (
	NoSeverity Severity = iota
	Note
	Warning
	Error
	Failure
)

var severityNames = [...]string{
	NoSeverity: "-", Note: "note", Warning: "warning", Error: "error",
	Failure: "failure",
}

func (s Severity) String() string {
	if int(s) < len(severityNames) {
		return severityNames[s]
	}
	return fmt.Sprintf("severity(%d)", uint8(s))
}

// Event is an assert failure, report, or cover hit or miss, tied to its
// source line. In a Trace, Process is a one-based index into
// Trace.Processes, 0 for none.
type Event struct {
	Time     Time
	Delta    uint32
	Kind     EventKind
	Severity Severity
	// File is the project-relative slash path of the source; Line is
	// one-based.
	File    string
	Line    int
	Process int
	Message string
}

// Header identifies a run.
type Header struct {
	Testbench string
	// Seed is the run's seed. It names the trace file (FileName) but
	// is not part of the canonical text or of any export: a race-free
	// design gives the same bytes at every seed, and any difference
	// between seeds is the race warning (#36, #29). Decode leaves it 0.
	Seed uint64
	// End is the simulated time the run stopped at.
	End Time
	// NonReproducible marks a --real-env run (#18): it read the real
	// clock or environment, so the same design and seed need not give
	// the same bytes, and the trace can never be a corpus answer key.
	// It is encoded as the optional header line "reproducible no".
	NonReproducible bool
}

// Trace is a complete native trace. Every slice is in canonical order:
// Types by name, Scopes, Signals and Processes by path, Changes by
// (time, delta, signal path) and Events by (time, delta, process path,
// emission order). Build one with a Recorder or Decode; treat it as
// immutable.
type Trace struct {
	Header    Header
	Types     []Type
	Scopes    []Scope
	Signals   []Signal
	Processes []Process
	Changes   []Change
	Events    []Event

	types   map[string]int // name -> index in Types
	signals map[string]int // path -> index in Signals
	bySig   [][]int        // signal -> indices into Changes
}

// index builds the lookup tables.
func (t *Trace) index() {
	t.types = make(map[string]int, len(t.Types))
	for i, ty := range t.Types {
		t.types[ty.Name] = i
	}
	t.signals = make(map[string]int, len(t.Signals))
	for i, s := range t.Signals {
		t.signals[s.Path] = i
	}
	t.bySig = make([][]int, len(t.Signals))
	for i, c := range t.Changes {
		t.bySig[c.Signal] = append(t.bySig[c.Signal], i)
	}
}

// Type returns the type called name.
func (t *Trace) Type(name string) (*Type, bool) {
	i, ok := t.types[name]
	if !ok {
		return nil, false
	}
	return &t.Types[i], true
}

// SignalType returns the type of signal i.
func (t *Trace) SignalType(i int) *Type {
	ty, _ := t.Type(t.Signals[i].Type)
	return ty
}

// ElemType returns the element type of an Array type, or nil.
func (t *Trace) ElemType(ty *Type) *Type {
	if ty.Kind != Array {
		return nil
	}
	e, _ := t.Type(ty.Elem)
	return e
}

// Lookup returns the index of the signal at path.
func (t *Trace) Lookup(path string) (int, bool) {
	i, ok := t.signals[path]
	return i, ok
}

// ChangesOf returns the changes of signal i in time order.
func (t *Trace) ChangesOf(i int) []Change {
	idx := t.bySig[i]
	out := make([]Change, len(idx))
	for k, j := range idx {
		out[k] = t.Changes[j]
	}
	return out
}

// ValueAt returns the value signal path settled on at time at: the
// value after the last delta of the last time not after at. ok is false
// when the signal is unknown or has no value yet.
func (t *Trace) ValueAt(path string, at Time) (Value, Change, bool) {
	return t.ValueAtDelta(path, at, math.MaxUint32)
}

// ValueAtDelta returns the value of signal path after delta cycle
// delta of time at, with the change that set it.
func (t *Trace) ValueAtDelta(path string, at Time, delta uint32) (
	Value, Change, bool,
) {
	i, ok := t.signals[path]
	if !ok {
		return Value{}, Change{}, false
	}
	idx := t.bySig[i]
	// The first change strictly after (at, delta).
	n, _ := slices.BinarySearchFunc(idx, struct{}{},
		func(j int, _ struct{}) int {
			c := &t.Changes[j]
			if cmp.Or(cmp.Compare(c.Time, at),
				cmp.Compare(c.Delta, delta)) <= 0 {
				return -1
			}
			return 1
		})
	if n == 0 {
		return Value{}, Change{}, false
	}
	c := t.Changes[idx[n-1]]
	return c.Value, c, true
}

// InScope reports whether path is selected by the instance-path
// prefixes of simulation.dump_scope: an empty list selects everything;
// a prefix selects itself and everything below it (tb.dut selects
// tb.dut and tb.dut.q, not tb.dut2).
func InScope(path string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p) &&
			len(path) > len(p) && path[len(p)] == '.' {
			return true
		}
	}
	return false
}

// SplitPath splits a hierarchical path at its dots. A dot inside an
// extended identifier (\a.b\, where \\ is a literal backslash) does not
// split.
func SplitPath(path string) []string {
	var out []string
	start, ext := 0, false
	for i := 0; i < len(path); i++ {
		switch c := path[i]; {
		case c == '\\' && ext && i+1 < len(path) && path[i+1] == '\\':
			i++
		case c == '\\':
			ext = !ext
		case c == '.' && !ext:
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	return append(out, path[start:])
}
