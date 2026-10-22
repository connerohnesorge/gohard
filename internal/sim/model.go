package sim

import (
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// Time is simulated time in femtoseconds. It is a value: the kernel has
// no clock.
type Time = trace.Time

// TimeHigh is TIME'HIGH, the result of 'last_event and 'last_active of
// a signal that never had an event or transaction.
const TimeHigh = int64(^uint64(0) >> 1)

// SigID indexes Model.Signals.
type SigID int32

// SrcRef locates model elements in the source: the project-relative
// file and a byte span, plus the one-based line and column (UTF-8
// bytes) of the start for diagnostics. Content-derived node IDs are
// never used as source references (#35).
type SrcRef struct {
	Path       string
	Start, End int
	Line, Col  int
}

// ImplicitKind is the kind of an implicit signal (LRM 16.2.3).
type ImplicitKind uint8

// Implicit signal kinds; ImplicitNone is an explicit signal or port.
const (
	ImplicitNone        ImplicitKind = iota
	ImplicitStable                   // S'STABLE(T), BOOLEAN
	ImplicitQuiet                    // S'QUIET(T), BOOLEAN
	ImplicitTransaction              // S'TRANSACTION, BIT
	ImplicitDelayed                  // S'DELAYED(T), the type of S
)

var implicitNames = [...]string{
	ImplicitStable: "stable", ImplicitQuiet: "quiet",
	ImplicitTransaction: "transaction", ImplicitDelayed: "delayed",
}

// String returns the attribute name.
func (k ImplicitKind) String() string {
	if int(k) < len(implicitNames) && implicitNames[k] != "" {
		return implicitNames[k]
	}
	return "signal"
}

// Signal is one elaborated signal or port.
type Signal struct {
	// Path is the hierarchical path (tb.dut.counter_q), the same the
	// language server and the trace use.
	Path string
	Type *eval.Type
	// Init is the initial (default) value; it also gives the shape of
	// the signal (its scalar subelements in order).
	Init eval.Value
	// Dir is the port mode (0 or trace.Internal for a signal).
	Dir trace.Dir
	// Resolve resolves each scalar subelement (std_logic) from the
	// values of its drivers; nil for an unresolved signal. It is used
	// only on a signal that owns its net elements (not Collapsed).
	Resolve Resolver
	// LeafResolve, when non-nil, gives the resolver of each scalar
	// subelement instead (records whose fields resolve differently;
	// nil entries are unresolved).
	LeafResolve []Resolver
	// LeafDirs, when non-nil, gives the mode of each scalar subelement
	// of a mode-view port (#17); the trace shows each record field
	// with its own mode.
	LeafDirs []trace.Dir
	// Collapsed puts the signal onto elements Offset .. Offset+n-1 of
	// signal Of (a port associated with an actual signal, an alias).
	// Collapsed signals may chain but not loop.
	Collapsed bool
	Of        SigID
	Offset    int
	// Pieces, when non-nil on a Collapsed signal, put its elements
	// onto the concatenation of these parts of other signals instead
	// (a port associated subelement by subelement, LRM 6.5.7.1); Of
	// and Offset then name the first piece.
	Pieces []Target
	// Implicit, Prefix and Delay describe an implicit signal; Delay is
	// T in femtoseconds. Implicit signals are not traced.
	Implicit ImplicitKind
	Prefix   SigID
	Delay    int64
	// Modeled marks a signal driven by a stand-in model (#14).
	Modeled bool
	Src     SrcRef
}

// Target is a part of a signal: the scalar subelements First ..
// First+N-1 in the order of Signal.Init's leaves. N < 0 means "to the
// end of the signal".
type Target struct {
	Sig   SigID
	First int
	N     int
}

// Whole returns the target of all of s.
func Whole(s SigID) Target { return Target{Sig: s, N: -1} }

// Process is one process statement, concurrent statement equivalent,
// stand-in model process or PSL directive.
type Process struct {
	Path string
	Src  SrcRef
	// Postponed marks a postponed process (LRM 11.3).
	Postponed bool
	// Modeled marks a process of a simulation.models stand-in: every
	// value it drives is modeled in the trace.
	Modeled bool
	// Drivers are the signal parts the process drives (LRM 14.7.2):
	// their drivers exist from initialization on and take part in
	// resolution with the signal's initial value. Assigning to a part
	// that is not listed creates its driver at that moment.
	Drivers []Target
	Code    Code
}

// Model is one elaborated design, ready for New.
type Model struct {
	// Testbench names the run (the trace header).
	Testbench string
	Signals   []Signal
	Processes []Process
	// Scopes are the hierarchy levels for the trace (instances,
	// blocks, generates); record scopes are added by the kernel.
	Scopes []trace.Scope
	// Src is the testbench's location, for run-level diagnostics.
	Src SrcRef
	// Init are the assertion violations and reports elaboration ran
	// (the functions a constant, a generic or a signal's initial
	// value calls), in order: the kernel reports them, counted as any
	// other, when it initializes, before any process runs (NVC's
	// "(init)").
	Init []Report

	implicit []implicitKey
}

type implicitKey struct {
	kind   ImplicitKind
	prefix SigID
	delay  int64
	id     SigID
}

// AddSignal appends s and returns its ID.
func (m *Model) AddSignal(s Signal) SigID {
	m.Signals = append(m.Signals, s)
	return SigID(len(m.Signals) - 1)
}

// Implicit returns the implicit signal prefix'kind(delay), adding it
// on first use, so every occurrence of S'STABLE(5 ns) in a design
// denotes one signal.
func (m *Model) Implicit(kind ImplicitKind, prefix SigID,
	delay int64,
) SigID {
	if kind == ImplicitTransaction {
		delay = 0
	}
	for _, k := range m.implicit {
		if k.kind == kind && k.prefix == prefix && k.delay == delay {
			return k.id
		}
	}
	p := m.Signals[prefix]
	s := Signal{
		Path: p.Path + "'" + kind.String(), Implicit: kind,
		Prefix: prefix, Delay: delay, Src: p.Src,
	}
	switch kind {
	case ImplicitStable, ImplicitQuiet:
		s.Type, s.Init = eval.Boolean, eval.Bool(true)
	case ImplicitTransaction:
		s.Type, s.Init = eval.Bit, eval.Enum(eval.Bit, 0)
	default:
		s.Type, s.Init = p.Type, p.Init
	}
	id := m.AddSignal(s)
	m.implicit = append(m.implicit, implicitKey{kind, prefix, delay, id})
	return id
}

// Resolver is a resolution function. It is called with the current
// values of all drivers of one net element and must be pure.
type Resolver interface {
	Resolve(b *eval.Budget, drivers []eval.Value) (eval.Value, error)
}

// Memoizable is implemented by a comparable Resolver (a pointer) whose
// calls with the same driver values return the same value and, once
// two calls in a row cost the same steps, cost that from then on (a
// first call may cost more: it warms caches). The kernel then resolves
// the initial values of the many single-driver nets once per distinct
// value (initCache): only wall time changes, never a value or a step
// count.
type Memoizable interface {
	Resolver
	Memoizable()
}

// ResolverFunc adapts a Go function to Resolver.
type ResolverFunc func(b *eval.Budget, drivers []eval.Value) (
	eval.Value, error)

// Resolve implements Resolver.
func (f ResolverFunc) Resolve(b *eval.Budget, d []eval.Value) (
	eval.Value, error,
) {
	return f(b, d)
}

// Code is the behaviour of a process. Resume runs the process from
// where it last suspended (from the start on the first call) until it
// executes a wait statement, and returns that wait. It reaches signals
// only through c. Code must be a value the kernel can copy: Clone
// returns an independent copy of the process state (variables,
// program counter), used by Kernel.Clone.
type Code interface {
	Resume(c *Ctx) (Wait, error)
	Clone() Code
}

// Condition is implemented by Code whose waits may have an until
// clause: Until evaluates the condition of the current wait. The
// kernel calls it when a signal of the sensitivity set has an event.
type Condition interface {
	Until(c *Ctx) (bool, error)
}

// Wait is a wait statement (LRM 10.2): wait on On until (Until) for
// For. A Wait with no On, no Until and no For waits forever (a process
// that ends, or `wait;`).
type Wait struct {
	On     []SigID
	Until  bool
	For    int64 // femtoseconds; used when HasFor
	HasFor bool
}

// Mechanism is a delay mechanism (LRM 10.5.2.1).
type Mechanism struct {
	Transport bool
	// Reject is the pulse rejection limit of `reject R inertial`;
	// without HasReject the first element's delay is used.
	Reject    int64
	HasReject bool
}

// WaveElem is one waveform element: a value (for the target's
// subelements, any composite shape with the right leaf count) after a
// delay, or null (a disconnection, for guarded signals: hook).
type WaveElem struct {
	Value eval.Value
	After int64
	Null  bool
}
