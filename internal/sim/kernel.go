package sim

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// Options configure a run. The zero value runs without budgets in
// declaration order and halts at severity error.
type Options struct {
	// Seed is the run's seed (the trace header). Order gives the
	// in-delta process order; nil is declaration order (seed 0).
	Seed  uint64
	Order env.Order
	// TimeBudget is simulation.time_budget in femtoseconds (0: none).
	TimeBudget Time
	// DeltaBudget is simulation.delta_budget, deltas per time step (0:
	// none).
	DeltaBudget int64
	// Budget counts evaluation steps against simulation.step_budget.
	// Elaboration passes the budget it already charged, so its steps
	// count too; nil means a fresh budget of StepBudget steps.
	Budget     *eval.Budget
	StepBudget int64
	// StopLevel is simulation.stop_level: an assert or report of this
	// severity or above halts the run (default trace.Error). It does
	// not change any diagnostic.
	StopLevel trace.Severity
	// Writer receives the trace; nil records nothing.
	Writer trace.Writer
	// Observe, when set, is told the processes that run in every
	// cycle, in run order (race detection, race.go). It must not
	// change the kernel.
	Observe func(now Time, delta uint32, order []int32)
}

// Status is how far a run got.
type Status uint8

// Run statuses.
const (
	// Running: the run has more cycles to execute.
	Running Status = iota
	// Quiet: no transaction or timeout is pending (the design ran out
	// of events).
	Quiet
	// Stopped: std.env.stop or finish was called.
	Stopped
	// Halted: an assert or report at simulation.stop_level or above.
	Halted
	// Exceeded: a budget ran out (sim/budget-exceeded).
	Exceeded
	// Failed: a runtime error (sim/runtime-error) or a trace failure.
	Failed
	// Cancelled: Cancel was called between slices.
	Cancelled
)

var statusNames = [...]string{
	Running: "running", Quiet: "quiet", Stopped: "stopped",
	Halted: "halted", Exceeded: "budget-exceeded", Failed: "failed",
	Cancelled: "cancelled",
}

func (s Status) String() string {
	if int(s) < len(statusNames) {
		return statusNames[s]
	}
	return "status(" + strconv.Itoa(int(s)) + ")"
}

// Done reports whether the run has ended.
func (s Status) Done() bool { return s != Running }

type net struct {
	cur, last  eval.Value
	lastEvent  int64 // absolute fs; -1: never
	lastActive int64
	eventCyc   uint64
	activeCyc  uint64
	actProc    int32 // lowest process (0-based) applied this cycle; -1
	// forced is 1 + the index of the net's forces in Kernel.forced, 0
	// when the net was never forced: forces are rare, so a net keeps
	// only the index.
	forced    int32
	drivers   []int32
	resolve   Resolver
	users     []int32 // signals on this net element
	waiters   []waiter
	compactAt int
}

// noForce is the forces of a net that was never forced (read only).
var noForce netForce

// netForce is the forces of one net element. A force of mode in
// (forced, force) replaces the effective value; oforced and oforce are
// a force of mode out (force.go): they replace the driving value,
// below a force of mode in.
type netForce struct {
	forced  bool
	force   eval.Value
	oforced bool
	oforce  eval.Value
}

type waiter struct {
	proc int32
	gen  uint64
}

type driver struct {
	proc int32 // -1: the kernel (implicit signals)
	net  int32
	cur  eval.Value
	null bool
	wave []txn
}

type sigState struct {
	nets      []int32
	eventCyc  uint64
	activeCyc uint64
	cache     eval.Value
	cacheOK   bool
	// glitch tracking within one time step
	stepNo     uint64
	stepStart  eval.Value
	stepEvents int
	glitched   bool
	traces     []int32 // indices into Kernel.traceSigs
}

type procState struct {
	code      Code
	gen       uint64
	waiting   bool
	done      bool
	wait      Wait
	readyCyc  uint64
	postReady bool
}

type qitem struct {
	at   Time
	seq  uint64
	drv  int32 // >= 0: a driver transaction
	proc int32 // >= 0: a process timeout
	gen  uint64
}

type forceOp struct {
	net     int32
	release bool
	out     bool // force mode out (force.go)
	v       eval.Value
}

// Kernel is the state of one run. It is a value: Clone copies it.
type Kernel struct {
	m    *Model
	opts Options

	now    Time
	delta  uint32
	cyc    uint64 // cycle counter, 1 = initialization
	stepNo uint64 // time step counter
	seq    uint64
	status Status
	inited bool
	err    error

	nets    []net
	drivers []driver
	sigs    []sigState
	procs   []procState
	queue   []qitem // binary min-heap by (at, seq)
	forces  []forceOp
	forced  []netForce // net.forced indexes it
	post    []int32    // postponed processes ready in this time step
	multi   []int32    // signals with two or more events in this step

	traceSigs []traceSig
	stopReq   Status
	stopCode  int64
	current   int32 // the running process, -1 outside processes
	budget    *eval.Budget
	diags     []diag.Diagnostic
	ctx       Ctx
}

// New builds the kernel for m. The model must not change afterwards.
// Structural errors (a collapse loop, a bad offset) are returned as an
// error; they are elaboration bugs, not design problems.
func New(m *Model, o Options) (*Kernel, error) {
	if o.StopLevel == trace.NoSeverity {
		o.StopLevel = trace.Error
	}
	k := &Kernel{m: m, opts: o, current: -1, budget: o.Budget}
	if k.budget == nil {
		k.budget = eval.NewBudget(o.StepBudget)
	}
	if err := k.buildNets(); err != nil {
		return nil, err
	}
	k.procs = make([]procState, len(m.Processes))
	k.allocDrivers()
	for i, p := range m.Processes {
		if p.Code == nil {
			return nil, fmt.Errorf("sim: process %s has no code", p.Path)
		}
		k.procs[i].code = p.Code.Clone()
		for _, t := range p.Drivers {
			lo, hi, err := k.span(t)
			if err != nil {
				return nil, fmt.Errorf("sim: process %s: %w", p.Path, err)
			}
			j := lo
			eachLeaf(m.Signals[t.Sig].Init, lo, hi, func(v eval.Value) {
				k.newDriver(int32(i), t.Sig, j, v)
				j++
			})
		}
	}
	for i, s := range m.Signals {
		if s.Implicit != ImplicitNone {
			j := 0
			eachLeaf(s.Init, 0, len(k.sigs[i].nets), func(v eval.Value) {
				k.newDriver(-1, SigID(i), j, v)
				j++
			})
		}
	}
	k.ctx.k = k
	return k, nil
}

func (k *Kernel) buildNets() error {
	ss := k.m.Signals
	k.sigs = make([]sigState, len(ss))
	state := make([]uint8, len(ss)) // 0 new, 1 visiting, 2 done
	owned := 0
	for i := range ss {
		if !ss[i].Collapsed {
			owned += leaves(ss[i].Init)
		}
	}
	k.nets = slices.Grow(k.nets, owned) // no regrowth of a large net table
	// The nets of the signals that own them, from one allocation.
	arena := make([]int32, owned)
	var build func(i int) error
	build = func(i int) error {
		switch state[i] {
		case 1:
			return fmt.Errorf("sim: signal %s: collapse loop", ss[i].Path)
		case 2:
			return nil
		}
		state[i] = 1
		s := &ss[i]
		n := leaves(s.Init)
		st := &k.sigs[i]
		if s.Collapsed && s.Pieces != nil {
			st.nets = make([]int32, 0, n)
			for _, pc := range s.Pieces {
				if int(pc.Sig) < 0 || int(pc.Sig) >= len(ss) {
					return fmt.Errorf("sim: signal %s: bad actual",
						s.Path)
				}
				if err := build(int(pc.Sig)); err != nil {
					return err
				}
				of := k.sigs[pc.Sig].nets
				hi := pc.First + pc.N
				if pc.N < 0 {
					hi = len(of)
				}
				if pc.First < 0 || hi > len(of) || hi < pc.First {
					return fmt.Errorf("sim: signal %s: elements %d..%d "+
						"outside %s", s.Path, pc.First, hi-1,
						ss[pc.Sig].Path)
				}
				st.nets = append(st.nets, of[pc.First:hi]...)
			}
			if len(st.nets) != n {
				return fmt.Errorf("sim: signal %s: pieces give %d of %d "+
					"elements", s.Path, len(st.nets), n)
			}
		} else if s.Collapsed {
			if int(s.Of) < 0 || int(s.Of) >= len(ss) {
				return fmt.Errorf("sim: signal %s: bad actual", s.Path)
			}
			if err := build(int(s.Of)); err != nil {
				return err
			}
			of := k.sigs[s.Of].nets
			if s.Offset < 0 || s.Offset+n > len(of) {
				return fmt.Errorf("sim: signal %s: elements %d..%d "+
					"outside %s", s.Path, s.Offset, s.Offset+n-1,
					ss[s.Of].Path)
			}
			// Signal nets never change: a collapsed signal shares
			// its actual's.
			st.nets = of[s.Offset : s.Offset+n : s.Offset+n]
		} else {
			if s.LeafResolve != nil && len(s.LeafResolve) != n {
				return fmt.Errorf("sim: signal %s: %d leaf resolvers "+
					"for %d elements", s.Path, len(s.LeafResolve), n)
			}
			st.nets, arena = arena[:n:n], arena[n:]
			j := 0
			eachLeaf(s.Init, 0, n, func(v eval.Value) {
				st.nets[j] = int32(len(k.nets))
				r := s.Resolve
				if s.LeafResolve != nil {
					r = s.LeafResolve[j]
				}
				k.nets = append(k.nets, net{
					cur: v, last: v, lastEvent: -1,
					lastActive: -1, actProc: -1, resolve: r,
				})
				j++
			})
		}
		state[i] = 2
		return nil
	}
	for i := range ss {
		if err := build(i); err != nil {
			return err
		}
	}
	// The users of every net, in signal order, out of one exactly
	// sized allocation: count, then fill.
	cnt := make([]int32, len(k.nets)+1)
	for i := range ss {
		for _, n := range k.sigs[i].nets {
			cnt[n+1]++
		}
	}
	for i := 1; i < len(cnt); i++ {
		cnt[i] += cnt[i-1]
	}
	users := make([]int32, cnt[len(k.nets)])
	for n := range k.nets {
		k.nets[n].users = users[cnt[n]:cnt[n]:cnt[n+1]]
	}
	for i := range ss {
		for _, n := range k.sigs[i].nets {
			k.nets[n].users = append(k.nets[n].users, int32(i))
		}
	}
	return nil
}

// allocDrivers gives every net room for its drivers out of one
// allocation, and the driver table room for all of them: one slot per
// process target leaf and implicit signal leaf (an upper bound; a
// process naming a leaf twice has one driver). Targets outside their
// signal are skipped here and reported by New.
func (k *Kernel) allocDrivers() {
	cnt := make([]int32, len(k.nets)+1)
	for _, p := range k.m.Processes {
		for _, t := range p.Drivers {
			lo, hi, err := k.span(t)
			if err != nil {
				continue
			}
			for _, n := range k.sigs[t.Sig].nets[lo:hi] {
				cnt[n+1]++
			}
		}
	}
	for i, s := range k.m.Signals {
		if s.Implicit != ImplicitNone {
			for _, n := range k.sigs[i].nets {
				cnt[n+1]++
			}
		}
	}
	for i := 1; i < len(cnt); i++ {
		cnt[i] += cnt[i-1]
	}
	total := cnt[len(k.nets)]
	slots := make([]int32, total)
	for n := range k.nets {
		k.nets[n].drivers = slots[cnt[n]:cnt[n]:cnt[n+1]]
	}
	k.drivers = make([]driver, 0, total)
}

// span returns the leaf range [lo, hi) of target t.
func (k *Kernel) span(t Target) (int, int, error) {
	if int(t.Sig) < 0 || int(t.Sig) >= len(k.sigs) {
		return 0, 0, errors.New("no such signal")
	}
	n := len(k.sigs[t.Sig].nets)
	hi := t.First + t.N
	if t.N < 0 {
		hi = n
	}
	if t.First < 0 || hi > n || t.First > hi {
		return 0, 0, fmt.Errorf("elements %d..%d outside %s",
			t.First, hi-1, k.m.Signals[t.Sig].Path)
	}
	return t.First, hi, nil
}

// driverFor returns process p's driver of leaf j of signal s, creating
// it (with the signal's initial value) on first use.
func (k *Kernel) driverFor(p int32, s SigID, j int) int32 {
	n := k.sigs[s].nets[j]
	for _, d := range k.nets[n].drivers {
		if k.drivers[d].proc == p {
			return d
		}
	}
	init, _ := leafAt(k.m.Signals[s].Init, j)
	return k.addDriver(p, n, init)
}

// newDriver is driverFor with the template leaf init of leaf j given.
func (k *Kernel) newDriver(p int32, s SigID, j int, init eval.Value) {
	n := k.sigs[s].nets[j]
	for _, d := range k.nets[n].drivers {
		if k.drivers[d].proc == p {
			return
		}
	}
	k.addDriver(p, n, init)
}

func (k *Kernel) addDriver(p, n int32, init eval.Value) int32 {
	id := int32(len(k.drivers))
	k.drivers = append(k.drivers, driver{proc: p, net: n, cur: init})
	k.nets[n].drivers = append(k.nets[n].drivers, id)
	return id
}

// Clone returns an independent copy of the kernel: both continue
// identically. The copy has no trace Writer (SetWriter gives it one)
// and its own step budget with the same count.
func (k *Kernel) Clone() *Kernel {
	c := *k
	c.opts.Writer = nil
	b := *k.budget
	c.budget = &b
	c.opts.Budget = &b
	c.nets = slices.Clone(k.nets)
	for i := range c.nets {
		// Users never change and drivers only grow (driverFor): the
		// copy shares them, clipped so that a new driver of either
		// kernel reallocates instead of writing the other's slot.
		n := &c.nets[i]
		n.drivers = slices.Clip(n.drivers)
		n.waiters = slices.Clone(n.waiters)
	}
	c.drivers = slices.Clone(k.drivers)
	for i := range c.drivers {
		c.drivers[i].wave = slices.Clone(c.drivers[i].wave)
	}
	c.sigs = slices.Clone(k.sigs)
	for i := range c.sigs {
		c.sigs[i].traces = slices.Clone(c.sigs[i].traces)
	}
	c.procs = slices.Clone(k.procs)
	for i := range c.procs {
		p := &c.procs[i]
		p.code = p.code.Clone()
		p.wait.On = slices.Clone(p.wait.On)
	}
	c.queue = slices.Clone(k.queue)
	c.forces = slices.Clone(k.forces)
	c.forced = slices.Clone(k.forced)
	c.post = slices.Clone(k.post)
	c.multi = slices.Clone(k.multi)
	c.traceSigs = slices.Clone(k.traceSigs)
	c.diags = slices.Clone(k.diags)
	c.ctx = Ctx{k: &c}
	return &c
}

// SetWriter sets the trace writer of a kernel that has not started.
func (k *Kernel) SetWriter(w trace.Writer) { k.opts.Writer = w }

// Now returns the current simulated time; Delta the current delta.
func (k *Kernel) Now() Time     { return k.now }
func (k *Kernel) Delta() uint32 { return k.delta }

// Status returns the run's status.
func (k *Kernel) Status() Status { return k.status }

// Steps returns the evaluation steps used so far (elaboration's
// included when it shared the budget).
func (k *Kernel) Steps() int64 { return k.budget.Used() }

// StopStatus is the status passed to std.env.stop or finish.
func (k *Kernel) StopStatus() int64 { return k.stopCode }

// Err returns the trace writer or internal error that failed the run.
func (k *Kernel) Err() error { return k.err }

// Diagnostics returns the run's diagnostics in emission order.
func (k *Kernel) Diagnostics() []diag.Diagnostic {
	return slices.Clone(k.diags)
}

// Cancel stops the run at once (between slices): the trace ends at the
// current time.
func (k *Kernel) Cancel() {
	if !k.status.Done() {
		k.finish(Cancelled)
	}
}

// Run executes at most cycles simulation cycles (at least one) and
// returns the status. Call it again while the status is Running; a
// caller cancels by not calling it again (or Cancel), so a cancel takes
// effect within one slice.
func (k *Kernel) Run(cycles int) Status {
	for range max(cycles, 1) {
		if k.status.Done() {
			break
		}
		if !k.inited {
			k.initialize()
			continue
		}
		k.cycle()
	}
	return k.status
}

// RunAll runs to the end in slices of slice cycles, calling cancelled
// (if non-nil) between slices.
func (k *Kernel) RunAll(slice int, cancelled func() bool) Status {
	for !k.status.Done() {
		if cancelled != nil && cancelled() {
			k.Cancel()
			break
		}
		k.Run(slice)
	}
	return k.status
}

func (k *Kernel) initialize() {
	k.inited = true
	k.cyc = 1
	k.stepNo = 1
	if err := k.beginTrace(); err != nil {
		k.fail(err)
		return
	}
	// Initial effective values (LRM 14.7.5.2): resolved nets resolve
	// their drivers' initial values.
	var ic initCache
	for i := range k.nets {
		n := &k.nets[i]
		if n.resolve == nil || len(n.drivers) == 0 {
			continue
		}
		v, err := ic.resolve(k, n)
		if err != nil {
			k.runtime(-1, err)
			return
		}
		n.cur, n.last = v, v
	}
	k.emitInitial()
	order := make([]int32, 0, len(k.procs))
	var post []int32
	for i := range k.procs {
		if k.m.Processes[i].Postponed {
			post = append(post, int32(i))
		} else {
			order = append(order, int32(i))
		}
	}
	k.runProcs(order)
	if !k.status.Done() {
		k.runProcs(post)
	}
	k.afterProcs()
}

// cycle runs one simulation cycle (LRM 14.7.5.3).
func (k *Kernel) cycle() {
	tn, ok := k.next()
	if (!ok || tn > k.now) && len(k.post) > 0 {
		post := k.post
		k.post = nil
		k.runProcs(post)
		if k.afterProcs() {
			return
		}
		tn, ok = k.next()
		if ok && tn == k.now {
			k.runtime(-1, errors.New("a postponed process scheduled "+
				"a delta cycle"))
			return
		}
	}
	if !ok {
		k.endStep()
		k.finish(Quiet)
		return
	}
	if tn > k.now {
		k.endStep()
		if k.opts.TimeBudget > 0 && tn > k.opts.TimeBudget {
			k.exceeded("time", config.Time(k.opts.TimeBudget).String())
			return
		}
		k.now, k.delta = tn, 0
		k.stepNo++
	} else {
		if k.opts.DeltaBudget > 0 &&
			int64(k.delta)+1 > k.opts.DeltaBudget {
			k.exceeded("delta", strconv.FormatInt(k.opts.DeltaBudget,
				10)+" deltas")
			return
		}
		k.delta++
	}
	k.cyc++
	var touched []int32 // nets
	var timeouts []int32
	for len(k.queue) > 0 && k.queue[0].at == k.now {
		it := k.pop()
		if it.drv >= 0 {
			d := &k.drivers[it.drv]
			if len(d.wave) == 0 || d.wave[0].at != it.at {
				continue // stale
			}
			t := d.wave[0]
			d.wave = d.wave[1:]
			d.cur, d.null = t.v, t.null
			n := &k.nets[d.net]
			if n.activeCyc != k.cyc {
				n.activeCyc = k.cyc
				n.actProc = -1
				touched = append(touched, d.net)
			}
			if d.proc >= 0 && (n.actProc < 0 || d.proc < n.actProc) {
				n.actProc = d.proc
			}
			if k.step(1) {
				return
			}
			continue
		}
		p := &k.procs[it.proc]
		if p.waiting && p.gen == it.gen {
			timeouts = append(timeouts, it.proc)
		}
	}
	for _, f := range k.forces {
		n := &k.nets[f.net]
		if n.forced == 0 && !f.release {
			k.forced = append(k.forced, netForce{})
			n.forced = int32(len(k.forced))
		}
		if n.forced != 0 {
			nf := &k.forced[n.forced-1]
			if f.out {
				nf.oforced, nf.oforce = !f.release, f.v
			} else {
				nf.forced, nf.force = !f.release, f.v
			}
		}
		if n.activeCyc != k.cyc {
			n.activeCyc = k.cyc
			n.actProc = -1
			touched = append(touched, f.net)
		}
	}
	k.forces = k.forces[:0]
	slices.Sort(touched)
	var changed []int32 // signals with an event
	for _, ni := range touched {
		if k.updateNet(ni, &changed) {
			return
		}
	}
	if k.implicit(&changed) {
		return
	}
	k.emitChanges(changed)
	if k.status.Done() {
		return
	}
	ready := k.wake(changed, timeouts)
	if k.status.Done() {
		return
	}
	k.runProcs(ready)
	k.afterProcs()
}

// updateNet computes the effective value of an active net element and
// records an event.
func (k *Kernel) updateNet(ni int32, changed *[]int32) bool {
	n := &k.nets[ni]
	var v eval.Value
	nf := &noForce
	if n.forced != 0 {
		nf = &k.forced[n.forced-1]
	}
	switch {
	case nf.forced:
		v = nf.force
	case nf.oforced:
		v = nf.oforce
	case n.resolve != nil && len(n.drivers) > 0:
		var err error
		v, err = k.resolveNet(n)
		if err != nil {
			k.runtime(-1, err)
			return true
		}
	case len(n.drivers) > 0:
		v = k.drivers[n.drivers[0]].cur
		if len(n.drivers) > 1 {
			// The unresolved multi-driver case is an elaboration
			// error; the last driver applied wins if it gets here.
			for _, d := range n.drivers {
				if k.drivers[d].proc == n.actProc {
					v = k.drivers[d].cur
				}
			}
		}
	default:
		return false
	}
	n.lastActive = int64(k.now)
	for _, s := range n.users {
		k.sigs[s].activeCyc = k.cyc
	}
	if v.Equal(n.cur) {
		return false
	}
	for _, s := range n.users {
		st := &k.sigs[s]
		if st.stepNo != k.stepNo {
			st.stepNo, st.stepEvents = k.stepNo, 0
			st.stepStart = k.value(SigID(s))
		}
	}
	n.last, n.cur = n.cur, v
	n.lastEvent = int64(k.now)
	n.eventCyc = k.cyc
	for _, s := range n.users {
		st := &k.sigs[s]
		st.cacheOK = false
		if st.eventCyc != k.cyc {
			st.eventCyc = k.cyc
			st.stepEvents++
			if st.stepEvents == 2 && !st.glitched {
				k.multi = append(k.multi, s)
			}
			*changed = append(*changed, s)
		}
	}
	return false
}

// initCache remembers the initial resolution of the nets that have one
// driver with a scalar value: a design has hundreds of thousands of
// such nets (memories of std_logic) and a handful of distinct
// (resolver, value) pairs. A resolution function is pure (LRM 4.6), so
// the result depends on the pair alone. The first call of a function
// may cost more than the next ones (it fills the evaluator's layouts
// and constants), so a pair is answered from the cache only once two
// calls in a row returned the same value for the same steps; a hit
// charges those steps, so the step count is the one of calling it
// (the rule of sema's memo of pure calls, memo.go). Every call the
// cache makes is one the uncached kernel makes too.
type initCache struct {
	m map[initKey]initEntry
}

type initKey struct {
	r Memoizable
	t *eval.Type
	i int64
	f uint64
}

type initEntry struct {
	v      eval.Value
	steps  int64
	stable bool // the last two calls agreed
}

// resolve is k.resolveNet(n) through the cache.
func (c *initCache) resolve(k *Kernel, n *net) (eval.Value, error) {
	key, ok := initKeyOf(k, n)
	if !ok {
		return k.resolveNet(n)
	}
	e, seen := c.m[key]
	if lim := k.budget.Limit(); e.stable &&
		(lim <= 0 || k.budget.Used()+e.steps <= lim) {
		return e.v, k.budget.Step(e.steps)
	}
	// Not yet stable, or near the limit, where the call fails as it
	// would uncached.
	u0 := k.budget.Used()
	v, err := k.resolveNet(n)
	if err != nil {
		return v, err
	}
	steps := k.budget.Used() - u0
	if c.m == nil {
		c.m = map[initKey]initEntry{}
	}
	c.m[key] = initEntry{v: v, steps: steps,
		stable: seen && e.steps == steps && e.v.Equal(v) &&
			isLeaf(v) && len(v.E) == 0 && len(v.B) == 0}
	return v, nil
}

// initKeyOf is the cache key of net n: ok when n has exactly one
// driver, it is not null and its value is a scalar, and no observer
// counts the budget's charges (a hit charges them at once), and the
// resolver is Memoizable.
func initKeyOf(k *Kernel, n *net) (initKey, bool) {
	r, ok := n.resolve.(Memoizable)
	if !ok || len(n.drivers) != 1 || k.budget.Observer() != nil {
		return initKey{}, false
	}
	d := &k.drivers[n.drivers[0]]
	v := d.cur
	if d.null || v.T == nil || len(v.E) != 0 || len(v.B) != 0 {
		return initKey{}, false
	}
	switch v.T.Kind {
	case eval.KindEnum, eval.KindInteger, eval.KindPhysical,
		eval.KindFloating:
	default:
		return initKey{}, false
	}
	return initKey{r: r, t: v.T, i: v.I,
		f: math.Float64bits(v.F)}, true
}

func (k *Kernel) resolveNet(n *net) (eval.Value, error) {
	vals := make([]eval.Value, 0, len(n.drivers))
	for _, d := range n.drivers {
		if !k.drivers[d].null {
			vals = append(vals, k.drivers[d].cur)
		}
	}
	return n.resolve.Resolve(k.budget, vals)
}

// implicit updates the implicit signals whose prefix was active this
// cycle, in signal order (a prefix always precedes its implicit
// signals).
func (k *Kernel) implicit(changed *[]int32) bool {
	for i := range k.m.Signals {
		s := &k.m.Signals[i]
		if s.Implicit == ImplicitNone {
			continue
		}
		pre := &k.sigs[s.Prefix]
		ev := pre.eventCyc == k.cyc
		act := pre.activeCyc == k.cyc
		st := &k.sigs[i]
		switch s.Implicit {
		case ImplicitStable, ImplicitQuiet:
			hit := ev
			if s.Implicit == ImplicitQuiet {
				hit = act
			}
			if !hit {
				continue
			}
			id := k.nets[st.nets[0]].drivers[0]
			d := &k.drivers[id]
			d.cur = eval.Bool(false)
			d.wave = []txn{{at: k.now + Time(s.Delay),
				v: eval.Bool(true)}}
			k.push(k.now+Time(s.Delay), id)
			if k.direct(st.nets[0], eval.Bool(false), changed) {
				return true
			}
		case ImplicitTransaction:
			if !act {
				continue
			}
			n := &k.nets[st.nets[0]]
			v := eval.Enum(eval.Bit, 1-n.cur.I)
			k.drivers[n.drivers[0]].cur = v
			if k.direct(st.nets[0], v, changed) {
				return true
			}
		case ImplicitDelayed:
			if !ev {
				continue
			}
			for j, ni := range st.nets {
				id := k.nets[ni].drivers[0]
				d := &k.drivers[id]
				at := k.now + Time(s.Delay)
				d.wave = project(d.wave, []txn{{at: at,
					v: k.nets[pre.nets[j]].cur}}, true, 0)
				k.push(at, id)
			}
		}
	}
	return false
}

// direct sets an implicit net element's value in the current cycle.
func (k *Kernel) direct(ni int32, v eval.Value, changed *[]int32) bool {
	n := &k.nets[ni]
	if n.activeCyc != k.cyc {
		n.activeCyc = k.cyc
		n.actProc = -1
	}
	_ = v // the kernel driver already holds v
	return k.updateNet(ni, changed)
}

// wake returns the processes to resume this cycle in process order:
// those sensitive to a signal with an event (whose until condition
// holds) and those whose timeout expired.
func (k *Kernel) wake(changed, timeouts []int32) []int32 {
	var cand []int32
	mark := func(p int32) {
		if k.procs[p].readyCyc != k.cyc {
			k.procs[p].readyCyc = k.cyc
			cand = append(cand, p)
		}
	}
	var sens []int32
	for _, s := range changed {
		for _, ni := range k.sigs[s].nets {
			n := &k.nets[ni]
			if n.eventCyc != k.cyc {
				continue
			}
			live := n.waiters[:0]
			for _, w := range n.waiters {
				p := &k.procs[w.proc]
				if !p.waiting || p.gen != w.gen {
					continue
				}
				live = append(live, w)
				sens = append(sens, w.proc)
			}
			n.waiters = live
		}
	}
	slices.Sort(sens)
	sens = slices.Compact(sens)
	for _, p := range sens {
		if k.procs[p].wait.Until {
			ok, err := k.until(p)
			if err != nil {
				k.runtime(p, err)
				return nil
			}
			if !ok {
				continue
			}
		}
		mark(p)
	}
	for _, p := range timeouts {
		mark(p)
	}
	slices.Sort(cand)
	ready := cand[:0]
	for _, p := range cand {
		if k.m.Processes[p].Postponed {
			if !k.procs[p].postReady {
				k.procs[p].postReady = true
				k.post = append(k.post, p)
			}
			k.procs[p].waiting = false
			k.procs[p].gen++
			continue
		}
		ready = append(ready, p)
	}
	return ready
}

func (k *Kernel) until(p int32) (bool, error) {
	c, ok := k.procs[p].code.(Condition)
	if !ok {
		return true, nil
	}
	if k.step(1) {
		return false, nil
	}
	k.current = p
	defer func() { k.current = -1 }()
	return c.Until(&k.ctx)
}

// runProcs resumes the processes in order (process indices ascending),
// permuted by the seed's order.
func (k *Kernel) runProcs(ps []int32) {
	if len(ps) == 0 {
		return
	}
	slices.Sort(ps)
	order := ps
	if k.opts.Order != nil && len(ps) > 1 {
		perm := k.opts.Order.Permute(k.cyc, len(ps))
		order = make([]int32, len(ps))
		for i, j := range perm {
			order[i] = ps[j]
		}
	}
	if k.opts.Observe != nil {
		k.opts.Observe(k.now, k.delta, slices.Clone(order))
	}
	for _, p := range order {
		if k.status.Done() {
			return
		}
		k.resume(p)
	}
}

func (k *Kernel) resume(p int32) {
	ps := &k.procs[p]
	if ps.done {
		return
	}
	if k.step(1) {
		return
	}
	ps.waiting = false
	ps.postReady = false
	ps.gen++
	k.current = p
	w, err := ps.code.Resume(&k.ctx)
	k.current = -1
	if k.status.Done() {
		return
	}
	if err != nil {
		if eval.CodeOf(err) == eval.ErrCodeBudget {
			k.stepExceeded()
			return
		}
		k.runtime(p, err)
		return
	}
	k.suspend(p, w)
}

func (k *Kernel) suspend(p int32, w Wait) {
	ps := &k.procs[p]
	if len(w.On) == 0 && !w.HasFor && !w.Until {
		ps.done = true
		return
	}
	if w.HasFor && w.For < 0 {
		k.runtime(p, errors.New("negative timeout in wait for"))
		return
	}
	if k.m.Processes[p].Postponed && w.HasFor && w.For == 0 {
		k.runtime(p, errors.New("a postponed process waits for 0"))
		return
	}
	ps.waiting = true
	ps.wait = Wait{On: slices.Clone(w.On), Until: w.Until,
		For: w.For, HasFor: w.HasFor}
	for _, s := range w.On {
		if int(s) < 0 || int(s) >= len(k.sigs) {
			k.runtime(p, errors.New("wait on an unknown signal"))
			return
		}
		for _, ni := range k.sigs[s].nets {
			n := &k.nets[ni]
			n.waiters = append(n.waiters, waiter{p, ps.gen})
			if len(n.waiters) > max(16, 2*n.compactAt) {
				live := n.waiters[:0]
				for _, w := range n.waiters {
					q := &k.procs[w.proc]
					if q.gen == w.gen && (q.waiting || w.proc == p) {
						live = append(live, w)
					}
				}
				n.waiters = live
				n.compactAt = len(live)
			}
		}
	}
	if w.HasFor {
		at := k.now + Time(w.For)
		if at < k.now { // overflow: never
			return
		}
		k.seq++
		k.heapPush(qitem{at: at, seq: k.seq, drv: -1, proc: p,
			gen: ps.gen})
	}
}

// afterProcs ends the process phase: a stop or halt request ends the
// run. It reports whether the run ended.
func (k *Kernel) afterProcs() bool {
	if k.status.Done() {
		return true
	}
	if k.stopReq != Running {
		k.finish(k.stopReq)
		return true
	}
	return false
}

// next returns the time of the next cycle: the earliest live queue
// entry, or now when forces are pending.
func (k *Kernel) next() (Time, bool) {
	if len(k.forces) > 0 {
		return k.now, true
	}
	for len(k.queue) > 0 {
		it := k.queue[0]
		if it.drv >= 0 {
			d := &k.drivers[it.drv]
			if len(d.wave) > 0 && d.wave[0].at == it.at {
				return it.at, true
			}
		} else {
			p := &k.procs[it.proc]
			if p.waiting && p.gen == it.gen {
				return it.at, true
			}
		}
		k.pop()
	}
	return 0, false
}

// endStep closes a time step: a signal that had events but ends the
// step at its starting value had a zero-width glitch.
func (k *Kernel) endStep() {
	multi := k.multi
	k.multi = k.multi[:0]
	for _, i := range multi {
		st := &k.sigs[i]
		if st.stepNo != k.stepNo || st.stepEvents < 2 || st.glitched {
			continue
		}
		s := &k.m.Signals[i]
		// An implicit signal ('stable, 'quiet, 'transaction)
		// pulses for one delta by definition: no glitch.
		if s.Implicit != ImplicitNone {
			continue
		}
		if k.value(SigID(i)).Equal(st.stepStart) {
			st.glitched = true
			k.report(s.Src, diag.New(diag.SimGlitch, s.Path,
				timeText(k.now)))
		}
	}
}

func (k *Kernel) step(n int64) bool {
	if err := k.budget.Step(n); err != nil {
		k.stepExceeded()
		return true
	}
	return false
}

func (k *Kernel) stepExceeded() {
	lim := strconv.FormatInt(k.budget.Limit(), 10) + " steps"
	k.exceeded("step", lim)
}

func (k *Kernel) exceeded(which, limit string) {
	src := k.m.Src
	if k.current >= 0 {
		src = k.m.Processes[k.current].Src
	}
	k.report(src, diag.New(diag.SimBudgetExceeded, which, limit,
		timeText(k.now)))
	k.finish(Exceeded)
}

func (k *Kernel) runtime(p int32, err error) {
	src, inst := k.m.Src, k.m.Testbench
	if p < 0 {
		p = k.current
	}
	if p >= 0 {
		src, inst = k.m.Processes[p].Src, k.m.Processes[p].Path
	}
	k.report(src, diag.New(diag.SimRuntimeError, err.Error(), inst,
		timeText(k.now)))
	k.finish(Failed)
}

func (k *Kernel) fail(err error) {
	if k.err == nil {
		k.err = err
	}
	k.status = Failed
}

func (k *Kernel) report(src SrcRef, d diag.Diagnostic) {
	if src.Path != "" {
		d = d.At(src.Path, src.Line, src.Col)
	}
	k.diags = append(k.diags, d)
}

func (k *Kernel) finish(s Status) {
	if k.status.Done() {
		return
	}
	k.status = s
	k.finishCodes(s) // finish.go
	if w := k.opts.Writer; w != nil && k.inited {
		if err := w.End(k.now); err != nil && k.err == nil {
			k.err = err
		}
	}
}

func timeText(t Time) string {
	if t == 0 {
		return "0 fs"
	}
	return config.Time(t).String()
}

// push queues a driver transaction at time at.
func (k *Kernel) push(at Time, drv int32) {
	k.seq++
	k.heapPush(qitem{at: at, seq: k.seq, drv: drv, proc: -1})
}

func qless(a, b qitem) bool {
	return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.seq, b.seq)) < 0
}

func (k *Kernel) heapPush(it qitem) {
	q := append(k.queue, it)
	i := len(q) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !qless(q[i], q[p]) {
			break
		}
		q[i], q[p] = q[p], q[i]
		i = p
	}
	k.queue = q
}

func (k *Kernel) pop() qitem {
	q := k.queue
	top := q[0]
	last := len(q) - 1
	q[0] = q[last]
	q = q[:last]
	i := 0
	for {
		l, r, m := 2*i+1, 2*i+2, i
		if l < len(q) && qless(q[l], q[m]) {
			m = l
		}
		if r < len(q) && qless(q[r], q[m]) {
			m = r
		}
		if m == i {
			break
		}
		q[i], q[m] = q[m], q[i]
		i = m
	}
	k.queue = q
	return top
}
