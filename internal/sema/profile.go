package sema

import (
	"maps"
	"slices"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// Profile attributes evaluation steps to the design unit that declares
// the interpreted subprogram running when they are charged ("ieee.
// numeric_std"), exclusive of nested calls into other units; steps
// charged outside every call go to Outside. It is the deterministic
// measure behind the #35 native-body rule (a package gets native Go
// bodies only when it takes more than 20% of a corpus run's kernel
// time). A Profile observes the budgets it is attached to (Attach),
// and the budgets that pass their observer on (the interpreter's
// scratch budgets).
type Profile struct {
	// OnSwitch, when set, is called with the unit that runs from now
	// on whenever an interpreted call starts or returns (Outside when
	// the outermost call returns): the hook a CPU profile uses to
	// label time by package (BenchmarkSimIEEE).
	OnSwitch func(unit string)

	steps map[string]int64 // accumulation only; Units sorts
	stack []string
}

// Outside is the unit of the steps charged outside every interpreted
// call (the kernel, the interpreter's own statements).
const Outside = ""

// NewProfile returns an empty profile.
func NewProfile() *Profile { return &Profile{steps: map[string]int64{}} }

// Attach makes p observe b: every step charged to b is attributed.
func (p *Profile) Attach(b *eval.Budget) { b.SetObserver(p) }

// Steps implements eval.StepObserver.
func (p *Profile) Steps(n int64) {
	u := Outside
	if len(p.stack) > 0 {
		u = p.stack[len(p.stack)-1]
	}
	p.steps[u] += n
}

// UnitSteps is the steps one design unit's subprograms took.
type UnitSteps struct {
	Unit  string
	Steps int64
}

// Units returns the recorded steps by unit, sorted by unit name
// (Outside first).
func (p *Profile) Units() []UnitSteps {
	out := make([]UnitSteps, 0, len(p.steps))
	for _, u := range slices.Sorted(maps.Keys(p.steps)) {
		out = append(out, UnitSteps{u, p.steps[u]})
	}
	return out
}

// Total returns every step observed.
func (p *Profile) Total() int64 {
	var n int64
	for _, u := range p.Units() {
		n += u.Steps
	}
	return n
}

func (p *Profile) enter(x *Decl) {
	name := "?"
	if x.Unit != nil {
		name = string(x.Unit.Library) + "." + string(x.Unit.Name)
	}
	p.stack = append(p.stack, name)
	if p.OnSwitch != nil {
		p.OnSwitch(name)
	}
}

func (p *Profile) exit() {
	if len(p.stack) > 0 {
		p.stack = p.stack[:len(p.stack)-1]
	}
	if p.OnSwitch != nil {
		u := Outside
		if len(p.stack) > 0 {
			u = p.stack[len(p.stack)-1]
		}
		p.OnSwitch(u)
	}
}
